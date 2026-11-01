package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/sagalore/sagalore/internal/app"
	"github.com/sagalore/sagalore/internal/config"
	"github.com/sagalore/sagalore/internal/envcompat"
	"github.com/sagalore/sagalore/internal/notify"
	"github.com/sagalore/sagalore/internal/tray"
	"github.com/sagalore/sagalore/internal/version"
)

type options struct {
	APIURL   string
	Client   string
	WowPath  string
	Headless bool
	NoUpdate bool
}

func main() {
	opts, cmd, err := parseArgs(os.Args[1:])
	if err != nil {
		usage()
		os.Exit(2)
	}
	var code int
	switch cmd {
	case "run":
		code = run(opts)
	case "setup":
		code = setupCmd()
	case "sync":
		code = syncOnce(opts)
	case "version", "-version", "--version":
		fmt.Println("Sagalore", version.Version)
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		code = 2
	}
	os.Exit(code)
}

func parseArgs(args []string) (options, string, error) {
	var opts options
	var positionals []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--headless":
			opts.Headless = true
		case a == "--no-update":
			opts.NoUpdate = true
		case a == "--api-url" || strings.HasPrefix(a, "--api-url="):
			v, err := flagValue(a, args, &i)
			if err != nil {
				return opts, "", err
			}
			opts.APIURL = v
		case a == "--client" || strings.HasPrefix(a, "--client="):
			v, err := flagValue(a, args, &i)
			if err != nil {
				return opts, "", err
			}
			opts.Client = v
		case a == "--wow-path" || strings.HasPrefix(a, "--wow-path="):
			v, err := flagValue(a, args, &i)
			if err != nil {
				return opts, "", err
			}
			opts.WowPath = v
		case strings.HasPrefix(a, "-"):
			return opts, "", fmt.Errorf("ukjent flagg %s", a)
		default:
			positionals = append(positionals, a)
		}
	}
	if opts.APIURL == "" {
		opts.APIURL = envcompat.Get("SAGALORE_API_URL")
	}
	if opts.Client == "" {
		opts.Client = envcompat.Get("SAGALORE_CLIENT")
	}
	if opts.WowPath == "" {
		opts.WowPath = envcompat.Get("SAGALORE_WOW_PATH")
	}
	cmd := "run"
	if len(positionals) > 0 {
		cmd = positionals[0]
	}
	return opts, cmd, nil
}

func flagValue(a string, args []string, i *int) (string, error) {
	if eq := strings.IndexByte(a, '='); eq >= 0 {
		return a[eq+1:], nil
	}
	if *i+1 >= len(args) {
		return "", fmt.Errorf("%s trenger en verdi", a)
	}
	*i++
	return args[*i], nil
}

func usage() {
	fmt.Fprintf(os.Stderr, `Sagalore — a saga log for a World of Warcraft group

  sagalore                       start, install the addon, log in with Battle.net
  sagalore --client retail       use _retail_
  sagalore --client classic_era  use _classic_era_
  sagalore --client classic      use _classic_
  sagalore --api-url http://localhost:8080
  sagalore sync                  fetch sagas once
  sagalore setup                 optional folder and update repo
  sagalore --headless            run without a tray icon

Environment: SAGALORE_API_URL, SAGALORE_CLIENT (forever|retail|classic_era|classic), SAGALORE_WOW_PATH, SAGALORE_DEV_TOKEN.
Settings live in config.json. The program reads the chat log. It never sends keystrokes to the game.
`)
}

func logger() *slog.Logger {
	var w io.Writer = os.Stdout
	if dir, err := config.Dir(); err == nil {
		_ = os.MkdirAll(dir, 0o755)
		if f, err := os.OpenFile(filepath.Join(dir, "sagalore.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			w = io.MultiWriter(os.Stdout, f)
		}
	}
	return slog.New(slog.NewTextHandler(w, nil))
}

func loadOrSetup(log *slog.Logger, opts options) (config.Config, int) {
	cfg, err := config.Load()
	if os.IsNotExist(err) {
		cfg = config.Config{Client: "forever"}
		err = nil
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return cfg, 1
	}
	cfg = applyOptions(cfg, opts)
	if strings.TrimSpace(cfg.APIBaseURL) == "" {
		fmt.Fprintln(os.Stderr, "--api-url or SAGALORE_API_URL is required")
		return cfg, 1
	}
	if err := config.Validate(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return cfg, 1
	}
	if err := config.Save(cfg); err != nil {
		log.Warn("kunne ikke lagre config", "err", err)
	}
	return cfg, 0
}

func applyOptions(cfg config.Config, opts options) config.Config {
	if opts.APIURL != "" {
		cfg.APIBaseURL = strings.TrimRight(strings.TrimSpace(opts.APIURL), "/")
	} else if cfg.APIBaseURL == "" {
		cfg.APIBaseURL = strings.TrimRight(version.DefaultAPIBaseURL, "/")
	}
	if opts.Client != "" {
		cfg.Client = strings.ToLower(strings.TrimSpace(opts.Client))
	}
	if opts.WowPath != "" {
		cfg.WowPath = opts.WowPath
	}
	cfg.Normalize()
	return cfg
}

func setupCmd() int {
	log := logger()
	current, _ := config.Load()
	if current.Client == "" {
		current.Client = "forever"
	}
	cfg, err := config.Setup(os.Stdin, os.Stdout, current)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := config.Save(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	path, _ := config.Path()
	log.Info("oppsett lagret", "path", path)
	fmt.Println("Lagret", path)
	return 0
}

func run(opts options) int {
	log := logger()
	cfg, code := loadOrSetup(log, opts)
	if code != 0 {
		return code
	}
	dir, err := config.Dir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	application, err := app.New(cfg, filepath.Join(dir, "queue.json"), log)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	application.NoUpdate = opts.NoUpdate
	var trayMenu *tray.Menu
	application.OnUpdateAvailable = func(tag string) {
		if trayMenu != nil {
			refreshUpdate(trayMenu, application, log)
		}
		_ = notify.Show("Sagalore", "New version "+tag+" — restart to update")
	}
	if isTerminal(os.Stdin) {
		application.Prompt = func() (string, error) {
			fmt.Fprint(os.Stdout, "Lim inn koden fra nettleseren hvis vinduet ikke kom tilbake: ")
			var line string
			_, err := fmt.Fscanln(os.Stdin, &line)
			return strings.TrimSpace(line), err
		}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go application.Run(ctx)
	if opts.Headless || !tray.Supported() {
		fmt.Println("Sagalore is running. Log in with Battle.net if asked. Quit with Ctrl+C.")
		if application.NeedsLogin() {
			fmt.Println("If the browser did not finish login, re-run without --headless and use Log in with Battle.net in the tray.")
		}
		<-ctx.Done()
		return 0
	}
	refreshTray := func(m *tray.Menu) {
		m.SetStatus(tray.Status{User: application.AccountStatus()})
		m.SetVersion(version.Version)
		if application.LoggedIn() {
			m.SetAuthAction(true, func() {
				go application.Logout()
			})
			return
		}
		m.SetAuthAction(false, func() {
			go func() {
				if err := application.Login(context.Background()); err != nil {
					log.Warn("login failed", "err", err)
					_ = notify.Show("Sagalore", "Login failed. Try again from the tray.")
					return
				}
				if err := application.SyncNow(context.Background()); err != nil {
					log.Warn("sync failed", "err", err)
				}
			}()
		})
	}
	tray.Run(ctx, "Sagalore "+version.Version, []tray.Item{
		{Title: "Create sagas…", Tooltip: "Open sagalore.app to create sagas with AI", OnClick: func() {
			go func() {
				if err := application.OpenCreateSagas(); err != nil {
					log.Warn("create sagas", "err", err)
				}
			}()
		}},
		{Title: "Sync now", Tooltip: "Fetch sagas from the API", OnClick: func() {
			go func() {
				if err := application.SyncNow(context.Background()); err != nil {
					log.Warn("sync failed", "err", err)
				}
			}()
		}},
		{Title: "Quit", Tooltip: "Quit Sagalore", OnClick: cancel},
	}, func(m *tray.Menu) {
		trayMenu = m
		refreshTray(m)
		refreshUpdate(m, application, log)
		application.OnAccountChange = func() { refreshTray(m) }
	})
	return 0
}

func refreshUpdate(m *tray.Menu, application *app.App, log *slog.Logger) {
	m.SetAutoUpdate(application.AutoUpdateOn(), func(on bool) {
		if err := application.SetAutoUpdate(on); err != nil {
			log.Warn("kunne ikke lagre oppdateringsvalg", "err", err)
		}
	})
	tag := application.PendingUpdate()
	if tag == "" {
		m.SetPendingUpdate("", nil)
		return
	}
	m.SetPendingUpdate(tag, func() {
		go func() {
			if err := application.ApplyUpdate(); err != nil {
				log.Warn("oppdatering feilet", "err", err)
			}
		}()
	})
}

func syncOnce(opts options) int {
	log := logger()
	cfg, code := loadOrSetup(log, opts)
	if code != 0 {
		return code
	}
	dir, err := config.Dir()
	if err != nil {
		return 1
	}
	application, err := app.New(cfg, filepath.Join(dir, "queue.json"), log)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	application.NoUpdate = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := application.SyncNow(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println("Synkronisert. Skriv /reload i spillet hvis du er innlogget.")
	return 0
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
