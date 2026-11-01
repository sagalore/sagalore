package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sagalore/sagalore/internal/envcompat"
	"github.com/sagalore/sagalore/internal/realtime"
	"github.com/sagalore/sagalore/internal/webhook"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd = args[0]
		args = args[1:]
	}
	switch cmd {
	case "serve":
		os.Exit(serve(args))
	case "seed":
		os.Exit(seedCmd())
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `Sagalore API

  sagalore-api                 start the HTTP server
  sagalore-api seed            insert the local Elwynn tutorial sagas
  sagalore-api help

GET /v1/ws is the player WebSocket (same bearer as /v1/sync).

OAuth 2.1 for external clients (PKCE, discovery, dynamic registration):
  /.well-known/oauth-authorization-server
  /.well-known/oauth-protected-resource
  /oauth/register  /oauth/authorize  /oauth/continue  /oauth/token  /oauth/revoke
  Access tokens are gst_ and work on the API. Refresh tokens are gsr_.

Environment:
  DATABASE_URL                 Postgres connection string (required)
  ADMIN_TOKEN                  ops only: seed is a command; HTTP hard-delete of a saga or a group
  BNET_CLIENT_ID               Battle.net OAuth client id
  BNET_CLIENT_SECRET           Battle.net OAuth client secret (stays on the API)
  PUBLIC_BASE_URL              public API origin, also the OAuth redirect base (no trailing slash)
  IMAGE_URL_SECRET             HMAC secret for signed cover URLs (openssl rand -hex 32)
  IMAGE_URL_TTL                signed URL lifetime (Go duration, default 1h)
  PORT                         listen port (default 8080)
  SAGALORE_DEV_TOKEN            dev/CI login bypass. Not a stand-in for Battle.net.
  SAGALORE_BOOTSTRAP_GROUP      slug:Name, required only when sagas have no group yet
  SAGALORE_OWNER_BATTLETAG      owner of the bootstrap group (else the oldest BattleTag)
  SAGALORE_BOOTSTRAP_WEBHOOK_URL  copied once onto the bootstrap group when its webhook is empty
  SAGALORE_ALLOW_DEV_SECRETS    set to 1 for local docker-compose only
`)
}

// docker-compose.yml ships weak local defaults. Refuse them unless the
// operator explicitly opts in with SAGALORE_ALLOW_DEV_SECRETS=1.
var knownDevSecrets = map[string]string{
	"ADMIN_TOKEN":      "dev-admin-token",
	"IMAGE_URL_SECRET": "dev-image-url-secret",
}

func rejectDevSecrets() error {
	if envcompat.Get("SAGALORE_ALLOW_DEV_SECRETS") == "1" {
		return nil
	}
	for key, weak := range knownDevSecrets {
		if strings.TrimSpace(os.Getenv(key)) == weak {
			return fmt.Errorf("%s is the docker-compose default %q; set a real secret (openssl rand -hex 32) or SAGALORE_ALLOW_DEV_SECRETS=1 for local only", key, weak)
		}
	}
	return nil
}

func openPool(ctx context.Context) (*pgxpool.Pool, error) {
	url := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if url == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	return pgxpool.NewWithConfig(ctx, cfg)
}

func serve(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "", "listen address (default :$PORT or :8080)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := rejectDevSecrets(); err != nil {
		slog.Error("secrets", "err", err)
		return 1
	}
	ctx := context.Background()
	pool, err := openPool(ctx)
	if err != nil {
		slog.Error("database", "err", err)
		return 1
	}
	defer pool.Close()
	if err := migrate(ctx, pool); err != nil {
		slog.Error("migrate", "err", err)
		return 1
	}
	store := NewStore(pool)
	if err := store.Bootstrap(ctx, envcompat.Get("SAGALORE_BOOTSTRAP_GROUP"), envcompat.Get("SAGALORE_OWNER_BATTLETAG"), envcompat.Get("SAGALORE_BOOTSTRAP_WEBHOOK_URL")); err != nil {
		slog.Error("bootstrap", "err", err)
		return 1
	}
	q := webhook.NewQueue(&webhook.Client{})
	q.After = func(res webhook.Result) {
		if res.Err != nil || res.Type != webhook.TypeSagaCompleted || res.Ref == "" {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := store.MarkWebhookPosted(ctx, res.Ref); err != nil {
			slog.Error("mark webhook", "err", err)
		}
	}
	if strings.TrimSpace(os.Getenv("ADMIN_TOKEN")) == "" {
		slog.Warn("ADMIN_TOKEN is empty; admin routes are disabled")
	}
	imageTTL := time.Hour
	if raw := strings.TrimSpace(os.Getenv("IMAGE_URL_TTL")); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			slog.Warn("IMAGE_URL_TTL is invalid; using 1h", "value", raw)
		} else {
			imageTTL = d
		}
	}
	hub := realtime.NewHub()
	defer hub.Close()
	go hub.Listen(context.Background(), strings.TrimSpace(os.Getenv("DATABASE_URL")))
	publicBase := strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")), "/")
	imageSecret := strings.TrimSpace(os.Getenv("IMAGE_URL_SECRET"))
	if (publicBase == "") != (imageSecret == "") {
		slog.Warn("signed saga image URLs need both PUBLIC_BASE_URL and IMAGE_URL_SECRET")
	}
	srv := &Server{
		store:            store,
		adminToken:       os.Getenv("ADMIN_TOKEN"),
		publicBaseURL:    publicBase,
		imageURLSecret:   imageSecret,
		imageURLTTL:      imageTTL,
		webhooks:         q,
		events:           hub,
		devToken:         envcompat.Get("SAGALORE_DEV_TOKEN"),
		bnetClientID:     strings.TrimSpace(os.Getenv("BNET_CLIENT_ID")),
		bnetClientSecret: strings.TrimSpace(os.Getenv("BNET_CLIENT_SECRET")),
		bnet:             newBnetFlow(),
	}
	listen := *addr
	if listen == "" {
		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
		}
		listen = ":" + port
	}
	go func() {
		ticker := time.NewTicker(15 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := store.CleanupOAuth(ctx); err != nil {
				slog.Error("oauth cleanup", "err", err)
			}
			cancel()
		}
	}()
	httpSrv := &http.Server{
		Addr:              listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.ListenAndServe() }()
	slog.Info("listening", "addr", listen)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		slog.Error("server", "err", err)
		return 1
	case <-sig:
	}
	hub.Close()
	shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shut)
	return 0
}

func seedCmd() int {
	ctx := context.Background()
	pool, err := openPool(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer pool.Close()
	if err := migrate(ctx, pool); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	store := NewStore(pool)
	created, updated, err := seed(ctx, store)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := store.Bootstrap(ctx, envcompat.Get("SAGALORE_BOOTSTRAP_GROUP"), envcompat.Get("SAGALORE_OWNER_BATTLETAG"), envcompat.Get("SAGALORE_BOOTSTRAP_WEBHOOK_URL")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("seed: %d new sagas, %d updated\n", created, updated)
	return 0
}
