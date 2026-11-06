// Package app runs the Sagalore companion: install the addon, sync quests, and
// report completions from the chat log and SavedVariables. It never sends input
// to the game.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sagalore/sagalore/internal/backend"
	"github.com/sagalore/sagalore/internal/chatlog"
	"github.com/sagalore/sagalore/internal/config"
	"github.com/sagalore/sagalore/internal/contract"
	"github.com/sagalore/sagalore/internal/i18n"
	"github.com/sagalore/sagalore/internal/install"
	"github.com/sagalore/sagalore/internal/notify"
	"github.com/sagalore/sagalore/internal/queue"
	"github.com/sagalore/sagalore/internal/savedvars"
	"github.com/sagalore/sagalore/internal/screenshot"
	"github.com/sagalore/sagalore/internal/syncdata"
	"github.com/sagalore/sagalore/internal/update"
	"github.com/sagalore/sagalore/internal/version"
	"github.com/sagalore/sagalore/internal/wowpath"
)

type App struct {
	Config     config.Config
	Log        *slog.Logger
	Client     *backend.Client
	Queue      *queue.Queue
	Flavors    []wowpath.Flavor // every install that gets the addon and sync
	NoUpdate     bool
	UpdateDir    string         // tests; empty uses the config directory
	UpdateClient *update.Client // tests; nil uses GitHub with Update.Token
	Prompt       func() (string, error)
	sync       contract.SyncResponse
	player     contract.Player
	follows    []follower // chat logs, same order as Flavors
	mu         sync.Mutex
	loginMu    sync.Mutex
	loginTried bool
	signedOut  bool // user chose Log out; do not open the browser until they log in again
	notice     string
	noticeID   string
	// Notify shows the desktop banner. Tests set it. Nil uses the OS.
	Notify func(title, body string)
	// OnAccountChange runs after login, logout, or sync so the tray can refresh.
	OnAccountChange func()
	// OnUpdateAvailable runs once when a newer release has been downloaded.
	OnUpdateAvailable func(tag string)
	pendingTag        string
	pendingBinary     string
	pendingAddon      string
	notifiedTag       string
}

type follower struct {
	path    string
	offset  int64
	partial string
}

func New(cfg config.Config, queuePath string, log *slog.Logger) (*App, error) {
	if log == nil {
		log = slog.Default()
	}
	q, err := queue.Open(queuePath)
	if err != nil {
		return nil, err
	}
	cfg.Normalize()
	return &App{
		Config: cfg,
		Log:    log,
		Client: &backend.Client{BaseURL: cfg.APIBaseURL, APIKey: cfg.Token()},
		Queue:  q,
		player: contract.Player{
			BattleTag:   cfg.BattleTag,
			DisplayName: cfg.PlayerName,
		},
	}, nil
}

func (a *App) notifyAccount() {
	if a.OnAccountChange != nil {
		a.OnAccountChange()
	}
}

// AccountStatus is the account line the tray shows.
func (a *App) AccountStatus() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return accountStatus(a.Config.Token() != "", a.player, a.Config, a.sync, "en")
}

func accountStatus(loggedIn bool, player contract.Player, cfg config.Config, sync contract.SyncResponse, lang string) string {
	if !loggedIn {
		return i18n.T(lang, "app.logged_out")
	}
	who := strings.TrimSpace(sync.BattleTag)
	if who == "" {
		who = strings.TrimSpace(player.BattleTag)
	}
	if who == "" {
		who = strings.TrimSpace(cfg.BattleTag)
	}
	if who == "" {
		who = strings.TrimSpace(sync.DisplayName)
	}
	if who == "" {
		who = strings.TrimSpace(player.DisplayName)
	}
	if who == "" {
		who = strings.TrimSpace(cfg.PlayerName)
	}
	if who == "" {
		return i18n.T(lang, "app.logged_out")
	}
	return fmt.Sprintf(i18n.T(lang, "app.account"), who)
}

// applySyncIdentityLocked copies name and battletag from sync onto the
// in-memory player and config. Caller holds a.mu.
func (a *App) applySyncIdentityLocked(sync contract.SyncResponse) {
	a.sync = sync
	if name := strings.TrimSpace(sync.DisplayName); name != "" {
		a.player.DisplayName = name
		a.Config.PlayerName = name
	}
	if tag := strings.TrimSpace(sync.BattleTag); tag != "" {
		a.Config.BattleTag = tag
		a.player.BattleTag = tag
	}
}

func (a *App) Run(ctx context.Context) {
	a.bootstrap(ctx)
	if !a.NoUpdate {
		a.maybeUpdate(ctx)
	}
	wake := make(chan struct{}, 1)
	unauth := make(chan struct{}, 1)
	go a.watchRealtime(ctx, wake, unauth)
	tailTick := time.NewTicker(time.Second)
	flushTick := time.NewTicker(5 * time.Second)
	syncTick := time.NewTicker(10 * time.Minute)
	updateTick := time.NewTicker(4 * time.Hour)
	loginRemindTick := time.NewTicker(loginRemindInterval)
	defer tailTick.Stop()
	defer flushTick.Stop()
	defer syncTick.Stop()
	defer updateTick.Stop()
	defer loginRemindTick.Stop()
	debounce := time.NewTimer(time.Hour)
	debounce.Stop()
	defer debounce.Stop()
	var pending bool
	a.Flush(ctx)
	for {
		select {
		case <-ctx.Done():
			if err := a.Queue.Save(); err != nil {
				a.Log.Error("kunne ikke lagre køen", "err", err)
			}
			return
		case <-tailTick.C:
			a.pollChat()
			a.pollSavedVars()
		case <-flushTick.C:
			a.Flush(ctx)
		case <-syncTick.C:
			a.bootstrap(ctx)
		case <-loginRemindTick.C:
			a.maybeRemindLogin()
		case <-updateTick.C:
			if !a.NoUpdate {
				a.maybeUpdate(ctx)
			}
		case <-wake:
			pending = true
			if !debounce.Stop() {
				select {
				case <-debounce.C:
				default:
				}
			}
			debounce.Reset(200 * time.Millisecond)
		case <-debounce.C:
			if !pending {
				continue
			}
			pending = false
			if err := a.pullSync(ctx); err != nil {
				a.Log.Warn("sanntidssynk feilet", "err", err)
			}
		case <-unauth:
			a.clearSession()
			if err := a.bootstrap(ctx); err != nil {
				a.Log.Warn("innlogging etter sanntid feilet", "err", err)
			}
		}
	}
}

func (a *App) SyncNow(ctx context.Context) error {
	return a.bootstrap(ctx)
}

func (a *App) bootstrap(ctx context.Context) error {
	a.ensureFlavors()
	for _, f := range a.Flavors {
		if err := install.Install(f.Path, f.Client); err != nil {
			a.Log.Error("addon install failed", "path", f.Path, "client", f.Client, "err", err)
		}
		if err := install.WriteLanguage(f.Path, "en"); err != nil {
			a.Log.Warn("could not write addon language", "path", f.Path, "err", err)
		}
	}
	if a.Client.APIKey == "" {
		if err := a.ensureLogin(ctx); err != nil {
			a.Log.Warn("not logged in", "err", err)
			a.mu.Lock()
			tried := a.loginTried
			a.mu.Unlock()
			if !tried {
				// Config/API error before the browser opened — surface it.
				if a.Notify != nil {
					a.Notify("Sagalore", "Could not start Battle.net login. Check the API, then use Log in with Battle.net in the tray.")
				} else {
					_ = notify.Show("Sagalore", "Could not start Battle.net login. Check the API, then use Log in with Battle.net in the tray.")
				}
			}
			return err
		}
	}
	if err := a.pullSync(ctx); err != nil {
		a.Log.Warn("synkronisering feilet", "err", err)
		return err
	}
	return nil
}

func (a *App) pullSync(ctx context.Context) error {
	if a.Client == nil || a.Config.APIBaseURL == "" {
		return errors.New("API er ikke satt opp")
	}
	sync, err := a.Client.Sync(ctx)
	var httpErr *backend.HTTPError
	if errors.As(err, &httpErr) && httpErr.Status == 401 {
		a.clearSession()
		if err = a.ensureLogin(ctx); err != nil {
			return err
		}
		sync, err = a.Client.Sync(ctx)
	}
	if err != nil {
		return err
	}
	prev := a.sync
	desktop, addonNote := syncdata.ReloadNews(prev, sync)
	notice, noticeID := a.notice, a.noticeID
	if addonNote != "" {
		notice, noticeID = addonNote, sync.GeneratedAt
	}
	if len(a.Flavors) == 0 {
		a.mu.Lock()
		a.applySyncIdentityLocked(sync)
		a.notice, a.noticeID = notice, noticeID
		cfg := a.Config
		a.mu.Unlock()
		_ = config.Save(cfg)
		a.Log.Info("synkronisert, men WoW-mappen er ukjent")
		a.announce(desktop)
		a.notifyAccount()
		return nil
	}
	entries := syncdata.Merge(sync)
	aspects, images, err := a.fetchSagaImages(ctx, sync)
	if err != nil {
		a.Log.Warn("kunne ikke hente saga-bilder", "err", err)
	}
	for i := range entries {
		if ar, ok := aspects[entries[i].QuestID]; ok && ar > 0.05 {
			entries[i].ImageAspect = ar
		}
	}
	body := syncdata.WriteSyncLua(sync.BattleTag, sync.GeneratedAt, entries, notice, noticeID, sync.Groups, sync.Invites, sync.Characters, sync.Leaderboards)
	for _, f := range a.Flavors {
		addonDir := wowpath.AddonDir(f.Path)
		if err := writeSyncPayload(addonDir, body, images); err != nil {
			return err
		}
	}
	a.mu.Lock()
	a.applySyncIdentityLocked(sync)
	a.notice, a.noticeID = notice, noticeID
	cfg := a.Config
	a.mu.Unlock()
	_ = config.Save(cfg)
	a.Log.Info("oppdrag oppdatert. Skriv /reload i spillet hvis du er innlogget.", "antall", len(entries), "flavors", len(a.Flavors))
	a.announce(desktop)
	a.notifyAccount()
	return nil
}

func writeSyncPayload(addonDir, body string, images map[string][]byte) error {
	path := filepath.Join(addonDir, "SagaloreData.lua")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return err
	}
	media := filepath.Join(addonDir, "media")
	if err := os.MkdirAll(media, 0o755); err != nil {
		return err
	}
	keep := map[string]bool{}
	for name, raw := range images {
		if err := os.WriteFile(filepath.Join(media, name), raw, 0o644); err != nil {
			return err
		}
		keep[name] = true
	}
	entries, err := os.ReadDir(media)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !keep[e.Name()] {
			_ = os.Remove(filepath.Join(media, e.Name()))
		}
	}
	return nil
}

func (a *App) announce(desktop string) {
	if desktop == "" {
		return
	}
	// Log only — group webhooks carry the player-facing alert. Notify is for tests.
	a.Log.Info(desktop)
	if a.Notify != nil {
		a.Notify("Sagalore", desktop)
	}
}

func (a *App) fetchSagaImages(ctx context.Context, sync contract.SyncResponse) (aspects map[string]float64, images map[string][]byte, err error) {
	aspects = map[string]float64{}
	images = map[string][]byte{}
	for _, q := range sync.Quests {
		if !q.HasImage {
			continue
		}
		_, raw, err := a.Client.SagaImage(ctx, q.ID)
		if err != nil {
			a.Log.Warn("saga-bilde", "quest", q.ID, "err", err)
			continue
		}
		tga, w, h, err := screenshot.ToAddonTexture(raw)
		if err != nil {
			a.Log.Warn("saga-bilde konvertering", "quest", q.ID, "err", err)
			continue
		}
		name := q.ID + ".tga"
		images[name] = tga
		if h > 0 {
			aspects[q.ID] = float64(w) / float64(h)
		}
	}
	return aspects, images, nil
}

// ensureFlavors discovers every installed WoW flavor once per process.
func (a *App) ensureFlavors() {
	if len(a.Flavors) > 0 {
		if len(a.follows) != len(a.Flavors) {
			a.follows = make([]follower, len(a.Flavors))
			for i, f := range a.Flavors {
				a.follows[i].path = wowpath.ChatLog(f.Path)
			}
		}
		return
	}
	list, err := wowpath.DetectAll(a.Config.WowPath, wowpath.Candidates())
	if err != nil {
		a.Log.Warn(err.Error())
		return
	}
	a.Flavors = list
	a.follows = make([]follower, len(list))
	for i, f := range list {
		a.follows[i].path = wowpath.ChatLog(f.Path)
		a.Log.Info("fant WoW", "path", f.Path, "client", f.Client)
	}
	root := wowpath.InstallRoot(a.Config.WowPath)
	if a.Config.WowPath == "" && len(list) > 0 {
		root = wowpath.InstallRoot(list[0].Path)
	}
	if root != "" && root != a.Config.WowPath {
		a.Config.WowPath = root
		if err := config.Save(a.Config); err != nil {
			a.Log.Warn("kunne ikke lagre wow_path", "err", err)
		}
	}
}

// HandleLine enqueues a parsed chat line. Duplicate events return false.
func (a *App) HandleLine(line string) bool {
	ev, ok := chatlog.Parse(line)
	if !ok {
		return false
	}
	switch ev.Kind {
	case chatlog.KindComplete:
		return a.Queue.Enqueue(queue.Item{
			Kind: "completion", Key: queue.CompletionKey(ev.QuestID, ev.Character, ev.Realm),
			QuestID: ev.QuestID, QuestVersion: ev.QuestVersion, Character: ev.Character, Realm: ev.Realm,
			Level: ev.Level, CompletedAt: ev.At.UTC().Format(time.RFC3339),
		})
	case chatlog.KindCharacter:
		ok := a.Queue.Enqueue(queue.Item{
			Key:  queue.CharacterKey(ev.Character, ev.Realm, ev.Level, ev.Race, ev.Class, ev.BattleTag),
			Kind: "character", Character: ev.Character, Realm: ev.Realm,
			Level: ev.Level, Race: ev.Race, Class: ev.Class, BattleTag: ev.BattleTag,
		})
		if ok {
			a.Log.Info("karakter", "character", ev.Character, "realm", ev.Realm, "level", ev.Level)
		}
		return ok
	case chatlog.KindInvite:
		return a.Queue.Enqueue(queue.Item{
			Kind: "invite", Key: "invite\n" + ev.Action + "\n" + ev.InviteID,
			Action: ev.Action, InviteID: ev.InviteID,
		})
	case chatlog.KindLevel:
		return a.Queue.Enqueue(queue.Item{
			Kind: "level", Key: queue.LevelKey(ev.Character, ev.Realm, ev.Level),
			Character: ev.Character, Realm: ev.Realm, Level: ev.Level,
			CompletedAt: ev.At.UTC().Format(time.RFC3339),
		})
	default:
		return false
	}
}

func (a *App) pollChat() {
	if len(a.follows) == 0 && len(a.Flavors) > 0 {
		a.follows = make([]follower, len(a.Flavors))
		for i, f := range a.Flavors {
			a.follows[i].path = wowpath.ChatLog(f.Path)
		}
	}
	for i := range a.follows {
		for _, line := range a.follows[i].poll() {
			if a.HandleLine(line) {
				a.Log.Info("lest fra chatloggen", "line", trim(line))
			}
		}
	}
}

// pollSavedVars enqueues completions the addon wrote locally. On Classic Era,
// AddMessage lines never reach WoWChatLog.txt, so this is the reliable path.
// It also applies /sagalore and /sl reset requests, invite answers, and the
// pending character snapshot from SavedVariables. Every installed flavor is polled.
func (a *App) pollSavedVars() {
	if len(a.Flavors) == 0 {
		return
	}
	a.pollPendingResets()
	for _, f := range a.Flavors {
		a.pollPendingInvitesDir(f.Path)
		a.pollPendingCharacterDir(f.Path)
		list, err := savedvars.ReadCompletions(f.Path)
		if err != nil {
			continue
		}
		for _, c := range list {
			if _, ok := syncdata.QuestByID(a.sync, c.QuestID); !ok {
				continue
			}
			key := queue.CompletionKey(c.QuestID, c.Character, c.Realm)
			if !a.serverHasCompletion(c.QuestID, c.Character, c.Realm) {
				a.Queue.ForgetDone(key)
			}
			item := queue.Item{
				Kind: "completion", Key: key,
				QuestID: c.QuestID, QuestVersion: c.QuestVersion, Character: c.Character, Realm: c.Realm,
				Race: c.Race, Class: c.Class, Level: c.Level, CompletedAt: c.At.UTC().Format(time.RFC3339),
				Instance: c.Instance, GroupMembers: c.GroupMembers,
			}
			if !c.Started.IsZero() {
				item.StartedAt = c.Started.UTC().Format(time.RFC3339)
			}
			ok := a.Queue.Enqueue(item)
			if !ok && item.StartedAt != "" {
				a.Queue.SetStartedAt(key, item.StartedAt)
			}
			if !ok && len(c.GroupMembers) > 0 && !a.Queue.SetGroup(key, c.Instance, c.GroupMembers) {
				item.Key = "group\n" + key
				ok = a.Queue.Enqueue(item)
			}
			if ok {
				a.Log.Info("lest fra SavedVariables", "character", c.Character, "quest", c.QuestID, "client", f.Client)
			}
		}
	}
}

// pollPendingResets deletes API progress for /sagalore wipe|reset after /reload
// wrote SagaloreDB.pending_resets. Without this, the next sync brings completed
// status back and the local wipe looks broken.
func (a *App) pollPendingResets() {
	if a.Client == nil || a.Client.APIKey == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	changed := false
	for _, f := range a.Flavors {
		list, err := savedvars.ReadResets(f.Path)
		if err != nil || len(list) == 0 {
			continue
		}
		for _, r := range list {
			key := queue.ResetKey(r.QuestID, r.All, r.At)
			if a.Queue.IsDone(key) {
				continue
			}
			questID := ""
			if !r.All {
				questID = r.QuestID
			}
			deleted, err := a.Client.ResetProgress(ctx, questID)
			if err != nil {
				a.Log.Warn("kunne ikke tilbakestille progress", "quest", questID, "err", err)
				continue
			}
			if r.All {
				a.Queue.ForgetDonePrefix("completion\n")
			} else {
				a.Queue.ForgetDonePrefix("completion\n" + questID + "\n")
			}
			a.Queue.MarkDone(key)
			changed = true
			a.Log.Info("tilbakestilte progress", "quest", questID, "all", r.All, "deleted", deleted)
		}
	}
	if !changed {
		return
	}
	if err := a.Queue.Save(); err != nil {
		a.Log.Error("kunne ikke lagre køen", "err", err)
	}
	if err := a.pullSync(ctx); err != nil {
		a.Log.Warn("synk etter reset feilet", "err", err)
	}
}

func (a *App) pollPendingInvitesDir(wowDir string) {
	list, err := savedvars.ReadPendingInvites(wowDir)
	if err != nil || len(list) == 0 {
		return
	}
	for _, inv := range list {
		ok := a.Queue.Enqueue(queue.Item{
			Kind: "invite", Key: "invite\n" + inv.Action + "\n" + inv.ID,
			Action: inv.Action, InviteID: inv.ID,
		})
		if ok {
			a.Log.Info("lest invite fra SavedVariables", "action", inv.Action, "id", inv.ID)
		}
	}
}

func (a *App) pollPendingCharacterDir(wowDir string) {
	list, err := savedvars.ReadPendingCharacters(wowDir)
	if err != nil || len(list) == 0 {
		return
	}
	for _, ch := range list {
		level := ch.Level
		if level < 1 {
			level = 1
		}
		key := queue.CharacterKey(ch.Name, ch.Realm, level, ch.Race, ch.Class, ch.BattleTag)
		ok := a.Queue.Enqueue(queue.Item{
			Kind: "character", Key: key,
			Character: ch.Name, Realm: ch.Realm, Level: level,
			Race: ch.Race, Class: ch.Class, BattleTag: ch.BattleTag,
		})
		if ok {
			a.Log.Info("lest character fra SavedVariables", "character", ch.Name, "realm", ch.Realm)
		}
	}
}

func (a *App) serverHasCompletion(questID, character, realm string) bool {
	for _, c := range a.sync.Completions {
		if c.QuestID != questID {
			continue
		}
		if !strings.EqualFold(c.Character, character) {
			continue
		}
		if realm != "" && c.Realm != "" && !strings.EqualFold(c.Realm, realm) {
			continue
		}
		return true
	}
	for _, p := range a.sync.Progress {
		if p.QuestID != questID || p.Status != "completed" {
			continue
		}
		ch, ok := characterByID(a.sync, p.CharacterID)
		if !ok {
			continue
		}
		if !strings.EqualFold(ch.Name, character) {
			continue
		}
		if realm != "" && ch.Realm != "" && !strings.EqualFold(ch.Realm, realm) {
			continue
		}
		return true
	}
	return false
}

func characterByID(sync contract.SyncResponse, id string) (contract.Character, bool) {
	for _, ch := range sync.Characters {
		if ch.ID == id {
			return ch, true
		}
	}
	return contract.Character{}, false
}

func (a *App) Flush(ctx context.Context) {
	now := time.Now()
	for _, item := range a.Queue.Due(now) {
		if err := a.send(ctx, item); err != nil {
			var httpErr *backend.HTTPError
			permanent := errors.As(err, &httpErr) && httpErr.Permanent()
			a.Queue.Fail(item.Key, err.Error(), permanent, now)
			a.Log.Warn("kunne ikke sende hendelse", "key", item.Kind, "err", err)
			continue
		}
		a.Queue.Succeed(item.Key)
		a.Log.Info("sendt til API", "kind", item.Kind, "character", item.Character, "quest", item.QuestID)
	}
	if err := a.Queue.Save(); err != nil {
		a.Log.Error("kunne ikke lagre køen", "err", err)
	}
}

func (a *App) send(ctx context.Context, item queue.Item) error {
	if item.Kind == "invite" {
		var err error
		if item.Action == "decline" {
			err = a.Client.DeclineInvite(ctx, item.InviteID)
		} else {
			err = a.Client.AcceptInvite(ctx, item.InviteID)
		}
		if err != nil {
			return err
		}
		return a.pullSync(ctx)
	}
	if item.Kind == "character" {
		level := item.Level
		if level < 1 {
			level = 1
		}
		_, err := a.Client.UpsertCharacter(ctx, contract.CharacterUpsert{
			Name: item.Character, Realm: item.Realm, Level: level,
			Race: item.Race, Class: item.Class, BattleTag: item.BattleTag,
		})
		if err != nil {
			return err
		}
		return a.pullSync(ctx)
	}
	if len(a.sync.Characters) == 0 {
		_ = a.pullSync(ctx)
	}
	if item.CharacterID == "" {
		ch, ok := syncdata.FindCharacter(a.sync, item.Character, item.Realm)
		if !ok {
			return errors.New("fant ikke karakteren " + item.Character + " på " + item.Realm + " i synken")
		}
		item.CharacterID = ch.ID
	}
	tag := a.sync.BattleTag
	if tag == "" {
		tag = a.Config.BattleTag
	}
	if item.Kind == "level" {
		_, err := a.Client.Level(ctx, contract.LevelRequest{
			CharacterID: item.CharacterID, BattleTag: tag, Level: item.Level,
		})
		return err
	}
	if item.QuestTitle == "" {
		if q, ok := syncdata.QuestByID(a.sync, item.QuestID); ok {
			item.QuestTitle = q.Title
		}
	}
	a.Queue.Update(item)
	_, err := a.Client.Complete(ctx, contract.CompletionRequest{
		CharacterID: item.CharacterID, QuestID: item.QuestID, QuestVersion: item.QuestVersion,
		BattleTag: tag, Level: item.Level, Race: item.Race, Class: item.Class,
		CompletedAt: item.CompletedAt, StartedAt: item.StartedAt,
		Instance: item.Instance, GroupMembers: item.GroupMembers,
	}, nil)
	if err != nil {
		return err
	}
	_ = a.pullSync(ctx)
	return nil
}

func (a *App) maybeUpdate(ctx context.Context) {
	if a.NoUpdate || !a.Config.Update.On() || containsDev(version.Version) {
		return
	}
	repo := strings.TrimSpace(a.Config.Update.Repo)
	if repo == "" {
		return
	}
	a.offerUpdate(ctx, a.updateClient(), repo, version.Version)
}

// CheckUpdate looks for a newer GitHub release and stages it when found.
// Unlike the background timer, this ignores the Automatic updates preference
// so the tray "Check for updates" / "Update now" path always works.
// ready is true when a newer build is staged (including one already pending).
func (a *App) CheckUpdate(ctx context.Context) (tag string, ready bool) {
	if pending := a.PendingUpdate(); pending != "" {
		return pending, true
	}
	if a.NoUpdate || containsDev(version.Version) {
		return "", false
	}
	repo := strings.TrimSpace(a.Config.Update.Repo)
	if repo == "" {
		return "", false
	}
	a.offerUpdate(ctx, a.updateClient(), repo, version.Version)
	tag = a.PendingUpdate()
	return tag, tag != ""
}

func (a *App) updateClient() *update.Client {
	if a.UpdateClient != nil {
		return a.UpdateClient
	}
	return &update.Client{Token: a.Config.Update.Token}
}

// offerUpdate downloads a newer release into the pending directory and asks
// the tray to offer a restart. It does not replace the running binary.
func (a *App) offerUpdate(ctx context.Context, client *update.Client, repo, current string) {
	rel, err := client.Latest(ctx, repo)
	if err != nil {
		a.Log.Warn("fant ikke oppdatering", "err", err)
		return
	}
	if !update.Newer(rel.Tag, current) {
		return
	}
	a.mu.Lock()
	if a.pendingTag == rel.Tag && a.pendingBinary != "" {
		if _, err := os.Stat(a.pendingBinary); err == nil {
			a.mu.Unlock()
			return
		}
	}
	a.mu.Unlock()

	dir := a.updateDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		a.Log.Warn("kunne ikke lage oppdateringsmappe", "err", err)
		return
	}
	a.Log.Info("laster ned oppdatering", "versjon", rel.Tag)

	var addonPath string
	if asset, ok := update.FindAsset(rel, update.AddonZip); ok && len(a.Flavors) > 0 {
		body, err := client.Download(ctx, asset)
		if err != nil {
			a.Log.Warn("kunne ikke laste ned tillegget", "err", err)
		} else {
			addonPath = filepath.Join(dir, update.AddonZip)
			if err := os.WriteFile(addonPath, body, 0o644); err != nil {
				a.Log.Warn("kunne ikke lagre tillegget", "err", err)
				addonPath = ""
			}
		}
	}
	name := update.AssetName(osName(), archName())
	asset, ok := update.FindAsset(rel, name)
	if !ok {
		a.Log.Warn("utgivelsen mangler programfil", "fil", name)
		return
	}
	body, err := client.Download(ctx, asset)
	if err != nil {
		a.Log.Warn("kunne ikke laste ned programmet", "err", err)
		return
	}
	binPath := filepath.Join(dir, name)
	if err := os.WriteFile(binPath, body, 0o755); err != nil {
		a.Log.Warn("kunne ikke lagre programmet", "err", err)
		return
	}

	a.mu.Lock()
	first := a.notifiedTag != rel.Tag
	a.pendingTag = rel.Tag
	a.pendingBinary = binPath
	a.pendingAddon = addonPath
	a.notifiedTag = rel.Tag
	fn := a.OnUpdateAvailable
	a.mu.Unlock()
	a.Log.Info("ny versjon klar", "versjon", rel.Tag)
	if first && fn != nil {
		fn(rel.Tag)
	}
}

// PendingUpdate is the staged release tag, or empty.
func (a *App) PendingUpdate() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pendingTag
}

// AutoUpdateOn reports the saved preference.
func (a *App) AutoUpdateOn() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.Config.Update.On()
}

// SetAutoUpdate persists whether checks run. A download already staged stays available.
func (a *App) SetAutoUpdate(on bool) error {
	a.mu.Lock()
	a.Config.Update.SetEnabled(on)
	cfg := a.Config
	a.mu.Unlock()
	return config.Save(cfg)
}

// ApplyUpdate installs the staged addon and binary, then restarts.
func (a *App) ApplyUpdate() error {
	a.mu.Lock()
	bin := a.pendingBinary
	addon := a.pendingAddon
	flavors := append([]wowpath.Flavor(nil), a.Flavors...)
	a.mu.Unlock()
	if bin == "" {
		return errors.New("ingen oppdatering")
	}
	body, err := os.ReadFile(bin)
	if err != nil {
		return err
	}
	if addon != "" && len(flavors) > 0 {
		if z, err := os.ReadFile(addon); err == nil {
			for _, f := range flavors {
				if err := installZip(f.Path, z); err != nil {
					a.Log.Warn("kunne ikke pakke ut tillegget", "path", f.Path, "err", err)
					continue
				}
				if err := install.ApplyClientTOCs(f.Path, f.Client); err != nil {
					a.Log.Warn("kunne ikke tilpasse TOC", "path", f.Path, "err", err)
				}
			}
		}
	}
	if err := update.ReplaceExecutable(body); err != nil {
		return err
	}
	a.Log.Info("starter Sagalore på nytt")
	restart()
	return nil
}

func (a *App) updateDir() string {
	if a.UpdateDir != "" {
		return a.UpdateDir
	}
	dir, err := config.Dir()
	if err != nil {
		return filepath.Join(os.TempDir(), "sagalore-updates")
	}
	return filepath.Join(dir, "updates", "pending")
}

func (f *follower) poll() []string {
	if f.path == "" {
		return nil
	}
	file, err := os.Open(f.path)
	if err != nil {
		return nil
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil
	}
	if info.Size() < f.offset {
		f.offset = 0
		f.partial = ""
	}
	if _, err := file.Seek(f.offset, io.SeekStart); err != nil {
		return nil
	}
	buf, err := io.ReadAll(file)
	if err != nil || len(buf) == 0 && f.partial == "" {
		return nil
	}
	f.offset += int64(len(buf))
	text := f.partial + string(buf)
	parts := splitLines(text)
	f.partial = parts[len(parts)-1]
	return parts[:len(parts)-1]
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			out = append(out, line)
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func trim(s string) string {
	if len(s) > 180 {
		return s[:180]
	}
	return s
}

func containsDev(v string) bool {
	return len(v) >= 3 && (v == "dev" || has(v, "dev"))
}

func has(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && contains(s, sub))
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
