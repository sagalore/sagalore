package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/sagalore/sagalore/internal/config"
	"github.com/sagalore/sagalore/internal/contract"
	"github.com/sagalore/sagalore/internal/envcompat"
	"github.com/sagalore/sagalore/internal/install"
	"github.com/sagalore/sagalore/internal/notify"
)

// errLoginTimedOut is returned when the browser flow runs out of time. The
// companion keeps running and reminds the user from the tray / notifications.
var errLoginTimedOut = errors.New("Battle.net login timed out")

const loginRemindInterval = 5 * time.Minute

// loginTimeout is how long Login waits for the browser flow. Tests shorten it.
var loginTimeout = 5 * time.Minute

func (a *App) ensureLogin(ctx context.Context) error {
	a.mu.Lock()
	if a.Client.APIKey != "" {
		a.mu.Unlock()
		return nil
	}
	if a.signedOut || a.loginTried {
		a.mu.Unlock()
		return errors.New("not logged in")
	}
	a.mu.Unlock()
	err := a.Login(ctx)
	a.mu.Lock()
	// Only burn the auto-prompt after the browser flow actually starts.
	// A 503 (API missing BNET_*) must not block tray / next sync retries.
	if err == nil || !isBnetConfigError(err) {
		a.loginTried = true
	}
	a.mu.Unlock()
	if errors.Is(err, errLoginTimedOut) {
		a.announceLogin("Battle.net login timed out. Use Log in with Battle.net in the tray when you are ready.")
	}
	return err
}

// NeedsLogin reports whether the companion is running without a session and
// the user has not explicitly logged out (tray / reminders should nudge).
func (a *App) NeedsLogin() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.Config.Token() == "" && !a.signedOut
}

// maybeRemindLogin shows a desktop notice when auto-login already ran (or
// timed out) and the user still has no session. Skips explicit logout.
func (a *App) maybeRemindLogin() {
	a.mu.Lock()
	need := a.Config.Token() == "" && !a.signedOut && a.loginTried
	a.mu.Unlock()
	if !need {
		return
	}
	a.announceLogin("Still not logged in. Use Log in with Battle.net in the tray to sync your sagas.")
}

func isBnetConfigError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "503") || strings.Contains(msg, "bnet_not_configured")
}

// ConfigLanguage is the companion UI language. Always English for now.
func (a *App) ConfigLanguage() string {
	return "en"
}

// LoggedIn reports whether the companion has a Sagalore API credential.
func (a *App) LoggedIn() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.Config.Token() != ""
}

// Login uses SAGALORE_DEV_TOKEN when it is set.
// Otherwise it opens the browser on the API Battle.net start URL and polls
// until the API stores a session.
func (a *App) Login(ctx context.Context) error {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	a.mu.Lock()
	a.signedOut = false
	a.mu.Unlock()
	if dev := envcompat.Get("SAGALORE_DEV_TOKEN"); dev != "" {
		out, err := a.Client.DevLogin(ctx, dev)
		if err != nil {
			return err
		}
		return a.storeSession(out)
	}
	attempt, err := randomAttempt()
	if err != nil {
		return err
	}
	start := strings.TrimRight(a.Config.APIBaseURL, "/") + "/v1/auth/bnet/start?attempt=" + attempt
	// Register the attempt on the API before opening the browser. /start only
	// creates the row when hit; polling first would race and get "unknown attempt".
	if err := registerBnetAttempt(ctx, a.Client.HTTP, start); err != nil {
		return err
	}
	a.announceLogin("Log in with Battle.net in your browser")
	if err := openBrowser(start); err != nil {
		a.Log.Warn("could not open the browser", "err", err, "url", start)
		a.announceLogin("Could not open the browser. Use Log in with Battle.net in the tray, or open: " + start)
	}
	deadline := time.Now().Add(loginTimeout)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return errLoginTimedOut
		}
		out, pending, err := a.Client.PollBnet(ctx, attempt)
		if err != nil {
			return err
		}
		if !pending {
			return a.storeSession(out)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// registerBnetAttempt hits /v1/auth/bnet/start without following the Battle.net
// redirect so the poll endpoint knows the attempt id before the browser opens.
func registerBnetAttempt(ctx context.Context, httpClient *http.Client, startURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, startURL, nil)
	if err != nil {
		return err
	}
	client := httpClient
	if client == nil {
		client = http.DefaultClient
	}
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Battle.net start failed: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (a *App) storeSession(out contract.SessionResponse) error {
	a.mu.Lock()
	a.Config.SessionToken = out.SessionToken
	a.Config.APIKey = out.SessionToken
	a.storePlayerLocked(out.Player)
	a.Config.Normalize()
	a.Client.APIKey = a.Config.Token()
	cfg := a.Config
	a.mu.Unlock()
	if err := config.Save(cfg); err != nil {
		a.Log.Warn("could not save the session", "err", err)
	}
	a.Log.Info("logged in", "player", out.Player.DisplayName, "battletag", out.Player.BattleTag)
	a.notifyAccount()
	return nil
}

func (a *App) clearSession() {
	a.mu.Lock()
	a.clearSessionLocked(false)
	cfg := a.Config
	a.mu.Unlock()
	_ = config.Save(cfg)
	a.notifyAccount()
}

// Logout clears the Sagalore session. Sync will not open the browser again
// until the user chooses Log in.
func (a *App) Logout() {
	a.mu.Lock()
	a.signedOut = true
	a.loginTried = true
	a.sync = contract.SyncResponse{}
	a.clearSessionLocked(true)
	cfg := a.Config
	a.mu.Unlock()
	if err := config.Save(cfg); err != nil {
		a.Log.Warn("could not save logout", "err", err)
	}
	a.Log.Info("logged out")
	a.notifyAccount()
}

func (a *App) clearSessionLocked(keepSignedOut bool) {
	a.Config.SessionToken = ""
	if strings.HasPrefix(a.Config.APIKey, "gss_") || strings.HasPrefix(a.Config.APIKey, "gst_") {
		a.Config.APIKey = ""
	}
	a.Config.PlayerName = ""
	a.Config.PlayerEmail = ""
	a.Config.BattleTag = ""
	a.player = contract.Player{}
	a.Client.APIKey = a.Config.Token()
	if !keepSignedOut {
		a.loginTried = false
	}
}

// SetLanguage is kept for callers that still flip language; it forces English
// until multi-language shipping returns.
func (a *App) SetLanguage(lang string) error {
	_ = lang
	a.mu.Lock()
	a.Config.Language = "en"
	cfg := a.Config
	wow := a.WowDir
	a.mu.Unlock()
	if err := config.Save(cfg); err != nil {
		return err
	}
	if wow != "" {
		return install.WriteLanguage(wow, "en")
	}
	return nil
}

func (a *App) storePlayerLocked(p contract.Player) {
	a.player = p
	if tag := strings.TrimSpace(p.BattleTag); tag != "" {
		a.Config.BattleTag = tag
	}
	a.Config.PlayerName = strings.TrimSpace(p.DisplayName)
}

func randomAttempt() (string, error) {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// DefaultCreateSagasURL is the official marketing site's create section.
// Override with SAGALORE_CREATE_URL (e.g. your own tools URL when self-hosting).
const DefaultCreateSagasURL = "https://sagalore.app/#create"

// CreateSagasURL returns where the tray "Create sagas…" item should open.
func CreateSagasURL() string {
	if v := strings.TrimSpace(os.Getenv("SAGALORE_CREATE_URL")); v != "" {
		return v
	}
	return DefaultCreateSagasURL
}

// OpenCreateSagas opens the public MCP listing in the browser.
func (a *App) OpenCreateSagas() error {
	url := CreateSagasURL()
	if err := openBrowser(url); err != nil {
		a.Log.Warn("could not open create-sagas URL", "err", err, "url", url)
		return err
	}
	return nil
}

func (a *App) announceLogin(body string) {
	if body == "" {
		return
	}
	a.Log.Info(body)
	if a.Notify != nil {
		a.Notify("Sagalore", body)
		return
	}
	_ = notify.Show("Sagalore", body)
}

func openBrowser(raw string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", raw)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", raw)
	default:
		cmd = exec.Command("xdg-open", raw)
	}
	return cmd.Start()
}
