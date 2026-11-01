// Package config stores Sagalore settings in the user config directory.
package config

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sagalore/sagalore/internal/wowpath"
)

// DefaultUpdateRepo is the public Sagalore repo. Releases live there.
const DefaultUpdateRepo = "sagalore/sagalore" // github.com/sagalore/sagalore

type Update struct {
	Repo    string `json:"repo"`
	Token   string `json:"token,omitempty"`
	Enabled *bool  `json:"enabled,omitempty"` // nil means on
}

// On reports whether automatic update checks are enabled. Unset means on.
func (u Update) On() bool {
	if u.Enabled == nil {
		return true
	}
	return *u.Enabled
}

// SetEnabled stores an explicit on/off choice.
func (u *Update) SetEnabled(on bool) {
	u.Enabled = &on
}

type Config struct {
	BattleTag    string `json:"battletag,omitempty"`
	PlayerName   string `json:"player_name,omitempty"`
	PlayerEmail  string `json:"player_email,omitempty"`
	APIBaseURL   string `json:"api_base_url"`
	APIKey       string `json:"api_key,omitempty"`
	SessionToken string `json:"session_token,omitempty"`
	Language     string `json:"language,omitempty"`
	WowPath      string `json:"wow_path"`
	Client       string `json:"client"`
	Update       Update `json:"update"`
}

// Token is the credential sent to the Sagalore API. A session wins over a personal token stored in APIKey.
func (c Config) Token() string {
	if strings.TrimSpace(c.SessionToken) != "" {
		return strings.TrimSpace(c.SessionToken)
	}
	return strings.TrimSpace(c.APIKey)
}

func Dir() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", "Sagalore"), nil
	case "windows":
		if app := os.Getenv("APPDATA"); app != "" {
			return filepath.Join(app, "Sagalore"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "AppData", "Roaming", "Sagalore"), nil
	default:
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, "Sagalore"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "share", "Sagalore"), nil
	}
}

func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}
	return LoadPath(path)
}

func LoadPath(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, err
	}
	cfg.Normalize()
	return cfg, nil
}

func (cfg *Config) Normalize() {
	cfg.BattleTag = strings.TrimSpace(cfg.BattleTag)
	cfg.PlayerName = strings.TrimSpace(cfg.PlayerName)
	cfg.PlayerEmail = strings.TrimSpace(cfg.PlayerEmail)
	cfg.APIBaseURL = strings.TrimRight(strings.TrimSpace(cfg.APIBaseURL), "/")
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.SessionToken = strings.TrimSpace(cfg.SessionToken)
	cfg.Language = strings.ToLower(strings.TrimSpace(cfg.Language))
	if cfg.Language != "" && cfg.Language != "en" && cfg.Language != "nb" {
		cfg.Language = "en"
	}
	cfg.WowPath = strings.TrimSpace(cfg.WowPath)
	cfg.Client = wowpath.NormalizeClient(cfg.Client)
	if cfg.SessionToken == "" && strings.HasPrefix(cfg.APIKey, "gss_") {
		cfg.SessionToken = cfg.APIKey
	}
	if cfg.APIKey == "" {
		cfg.APIKey = cfg.SessionToken
	}
	cfg.Update.Repo = strings.TrimSpace(cfg.Update.Repo)
	cfg.Update.Token = strings.TrimSpace(cfg.Update.Token)
	if cfg.Update.Repo == "" {
		cfg.Update.Repo = DefaultUpdateRepo
	}
}

func Save(cfg Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	return SavePath(path, cfg)
}

func SavePath(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	cfg.Client = wowpath.NormalizeClient(cfg.Client)
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		_ = os.Remove(path)
	}
	return os.Rename(tmp, path)
}

func Validate(cfg Config) error {
	if cfg.APIBaseURL != "" && !strings.HasPrefix(cfg.APIBaseURL, "http://") && !strings.HasPrefix(cfg.APIBaseURL, "https://") {
		return errors.New("api_base_url må starte med http:// eller https://")
	}
	if !wowpath.ValidClient(cfg.Client) {
		return errors.New("client må være forever, retail, classic_era eller classic")
	}
	return nil
}

// Setup asks for the fields friends need. Prompts are Norwegian.
func Setup(r io.Reader, w io.Writer, current Config) (Config, error) {
	in := bufio.NewReader(r)
	ask := func(prompt, fallback string) (string, error) {
		if fallback != "" {
			fmt.Fprintf(w, "%s [%s]: ", prompt, fallback)
		} else {
			fmt.Fprintf(w, "%s: ", prompt)
		}
		line, err := in.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return fallback, nil
		}
		return line, nil
	}
	var err error
	if current.WowPath, err = ask("Mappe til WoW (tom = let automatisk, _retail_ når klient er retail)", current.WowPath); err != nil {
		return current, err
	}
	client, err := ask("Klient: forever, retail, classic_era eller classic", orDefault(current.Client, "forever"))
	if err != nil {
		return current, err
	}
	current.Client = client
	if current.Update.Repo == "" {
		current.Update.Repo = DefaultUpdateRepo
	}
	if current.Update.Repo, err = ask("GitHub-repo for oppdateringer (eier/navn)", current.Update.Repo); err != nil {
		return current, err
	}
	if current.Update.Token, err = ask("GitHub-token (tom hvis repoet er offentlig)", current.Update.Token); err != nil {
		return current, err
	}
	autoDefault := "ja"
	if current.Update.Enabled != nil && !*current.Update.Enabled {
		autoDefault = "nei"
	}
	current.Language = "en"
	auto, err := ask("Automatic updates (yes/no)", autoDefault)
	if err != nil {
		return current, err
	}
	switch strings.ToLower(auto) {
	case "nei", "no", "av", "false", "0":
		current.Update.SetEnabled(false)
	default:
		current.Update.SetEnabled(true)
	}
	current.Normalize()
	current.APIBaseURL = strings.TrimRight(strings.TrimSpace(current.APIBaseURL), "/")
	if err := Validate(current); err != nil {
		return current, err
	}
	return current, nil
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
