package main

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sagalore/sagalore/internal/contract"
)

func (s *Store) PlayerByBattleTag(ctx context.Context, battletag string) (contract.Player, error) {
	var p contract.Player
	var created time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, COALESCE(battletag, ''), display_name, COALESCE(bnet_account_id, ''), created_at
		FROM players WHERE lower(battletag)=lower($1)`, strings.TrimSpace(battletag)).Scan(
		&p.ID, &p.BattleTag, &p.DisplayName, &p.BnetAccountID, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return contract.Player{}, ErrNotFound
	}
	if err != nil {
		return contract.Player{}, err
	}
	scanPlayer(&p, created)
	return p, nil
}

const joinCookieName = "sagalore_join"

const joinPageCSSExtra = `
  form { margin: 0; }
  .frame.join-frame {
    aspect-ratio: auto;
    min-height: 22rem;
  }
  /* Section cue above the checklist — quieter than list item titles. */
  .lead.chars-lead {
    color: #b9b1a3;
    font-size: 0.9rem;
    font-weight: 400;
    letter-spacing: 0.02em;
    margin-bottom: 0.2rem;
  }
  .lead.chars-lead strong {
    color: #e6d7a8;
    font-weight: 600;
  }
  .chars {
    list-style: none;
    margin: 0.4rem 0 0;
    padding: 0;
    text-align: left;
  }
  .chars li {
    margin: 0.2rem 0;
  }
  .chars label {
    display: flex;
    align-items: flex-start;
    gap: 0.65rem;
    cursor: pointer;
    padding: 0.42rem 0.5rem;
    border-radius: 0.2rem;
  }
  .chars label:hover {
    background: rgba(201, 162, 74, 0.08);
  }
  .chars input {
    width: 1rem;
    height: 1rem;
    margin-top: 0.32rem;
    flex: 0 0 auto;
    accent-color: var(--gold);
  }
  .char-copy {
    display: flex;
    flex-direction: column;
    gap: 0.1rem;
    min-width: 0;
  }
  .char-name {
    color: #f6f0e6;
    font: 600 1.08rem/1.25 var(--serif);
    letter-spacing: 0.01em;
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.8);
  }
  .char-meta {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 0.3rem 0.5rem;
    font-family: "Avenir Next", "Segoe UI", "Helvetica Neue", sans-serif;
    font-size: 0.78rem;
    letter-spacing: 0.01em;
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.75);
  }
  .char-race {
    color: #a8b8d2;
    font-weight: 500;
  }
  .char-class {
    font-weight: 700;
  }
  .char-realm {
    color: rgba(240, 208, 120, 0.78);
    font-weight: 500;
  }
  .char-realm::before {
    content: "·";
    margin-right: 0.55rem;
    color: rgba(168, 184, 210, 0.45);
  }
  .class-warrior { color: #c79c6e; }
  .class-paladin { color: #f58cba; }
  .class-hunter { color: #abd473; }
  .class-rogue { color: #fff569; }
  .class-priest { color: #f0ebe3; }
  .class-shaman { color: #4f9fff; }
  .class-mage { color: #69ccf0; }
  .class-warlock { color: #9482c9; }
  .class-druid { color: #ff7d0a; }
  .class-deathknight { color: #c41f3b; }
  .class-monk { color: #00ff96; }
  .class-demonhunter { color: #a330c9; }
  .class-evoker { color: #33937f; }
  .flash {
    margin: 0.6rem 0 0;
    text-align: center;
    color: #f0d078;
    font-size: 0.92rem;
  }
  .flash.err { color: #f0b0a8; }
  .hint {
    margin: 0.85rem 0 0;
    text-align: center;
    color: rgba(220, 213, 200, 0.72);
    font-size: 0.85rem;
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.75);
  }
  /* Longer label than Allow/Join — keep btn-allow.png aspect so the
     beveled face stays tall enough for "Install Sagalore". */
  a.btn.install {
    font-size: 0.95rem;
    font-weight: 700;
    letter-spacing: 0.02em;
    min-width: 14.5rem;
    height: 3.5rem;
    padding: 0.1rem 2.6rem 0;
  }
  /* banner.png has ~23% empty pad under the gold rim. Collapse that pad in
     layout so Sign out can sit just under the visible plaque. */
  .who:has(+ .who-actions) {
    margin-bottom: -1.85rem;
  }
  .who-actions {
    display: flex;
    justify-content: center;
    position: relative;
    z-index: 1;
    margin-top: 0.2rem;
  }
  .who-actions + .lead {
    margin-top: 1.7rem;
  }
  a.signout {
    color: rgba(168, 184, 210, 0.72);
    font-family: "Avenir Next", "Segoe UI", "Helvetica Neue", sans-serif;
    font-size: 0.78rem;
    letter-spacing: 0.02em;
    text-decoration: underline;
    text-underline-offset: 3px;
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.55);
  }
  a.signout:hover { color: #c9d6ea; }
`

type joinPageView struct {
	Title       string
	GroupName   string
	Mode        string
	Who         string
	Token       string
	LoginURL    string
	SignOutURL  string
	InstallURL  string
	Message     string
	Error       string
	Done        bool
	Joined      bool
	Pending     bool
	NeedLogin   bool
	EmptyRoster bool
	Characters  []joinCharView
}

type joinCharView struct {
	ID         string
	Name       string
	Realm      string
	Race       string
	Class      string
	ClassSlug  string
}

func joinCharFrom(c contract.Character) joinCharView {
	return joinCharView{
		ID:        c.ID,
		Name:      strings.TrimSpace(c.Name),
		Realm:     strings.TrimSpace(c.Realm),
		Race:      strings.TrimSpace(c.Race),
		Class:     strings.TrimSpace(c.Class),
		ClassSlug: classSlug(c.Class),
	}
}

func classSlug(class string) string {
	s := strings.ToLower(strings.TrimSpace(class))
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "'", "")
	switch s {
	case "deathknight", "dk":
		return "deathknight"
	case "demonhunter", "dh":
		return "demonhunter"
	default:
		return s
	}
}

func joinSessionCookie(publicBase, token string, r *http.Request) *http.Cookie {
	secure := r.TLS != nil || strings.HasPrefix(strings.ToLower(publicBase), "https://")
	return &http.Cookie{
		Name:     joinCookieName,
		Value:    token,
		Path:     "/join",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(oauthConsentTTL.Seconds()),
	}
}

func (s *Server) playerFromJoinCookie(r *http.Request) (contract.Player, bool) {
	c, err := r.Cookie(joinCookieName)
	if err != nil || !strings.HasPrefix(c.Value, oauthConsentPref) {
		return contract.Player{}, false
	}
	p, err := s.store.PlayerByConsentSession(r.Context(), c.Value)
	if err != nil {
		return contract.Player{}, false
	}
	return p, true
}

func (s *Server) joinPageGet(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.PathValue("token"))
	if r.URL.Query().Get("signout") == "1" {
		c := joinSessionCookie(s.publicBaseURL, "", r)
		c.MaxAge = -1
		c.Value = ""
		http.SetCookie(w, c)
		http.Redirect(w, r, "/join/"+url.PathEscape(token), http.StatusFound)
		return
	}
	info, err := s.store.JoinLinkInfo(r.Context(), token, "")
	if err != nil {
		s.renderJoinPage(w, joinPageView{
			Title: "Join link unavailable",
			Error: "This join link is missing, expired, or revoked.",
			Done:  true,
		})
		return
	}
	view := joinPageView{
		Title:      "Join " + info.GroupName,
		GroupName:  info.GroupName,
		Mode:       info.Mode,
		Token:      token,
		LoginURL:   "/join/" + url.PathEscape(token) + "/login",
		SignOutURL: "/join/" + url.PathEscape(token) + "?signout=1",
	}
	player, ok := s.playerFromJoinCookie(r)
	if !ok {
		view.NeedLogin = true
		s.renderJoinPage(w, view)
		return
	}
	view.Who = player.BattleTag
	if view.Who == "" {
		view.Who = player.DisplayName
	}
	full, err := s.store.JoinLinkInfo(r.Context(), token, player.ID)
	if err != nil {
		s.renderJoinPage(w, joinPageView{
			Title: "Join link unavailable",
			Error: "This join link is missing, expired, or revoked.",
			Done:  true,
		})
		return
	}
	if len(full.Characters) == 0 {
		view.EmptyRoster = true
		s.renderJoinPage(w, view)
		return
	}
	for _, c := range full.Characters {
		view.Characters = append(view.Characters, joinCharFrom(c))
	}
	s.renderJoinPage(w, view)
}

func (s *Server) joinPageLogin(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.PathValue("token"))
	if _, err := s.store.joinLinkByToken(r.Context(), token); err != nil {
		s.renderJoinPage(w, joinPageView{
			Title: "Join link unavailable",
			Error: "This join link is missing, expired, or revoked.",
			Done:  true,
		})
		return
	}
	authURL, err := s.beginBnetJoinLogin(r.Context(), token)
	if err != nil {
		if errors.Is(err, errBnetNotConfigured) {
			s.renderJoinPage(w, joinPageView{
				Title: "Sign-in unavailable",
				Error: "Battle.net login is not configured on this server.",
				Done:  true,
			})
			return
		}
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, authURL, http.StatusFound)
}

// joinPageDevLogin is local-only: when SAGALORE_DEV_TOKEN is set, mint a join
// consent cookie for an existing BattleTag and return to /join/{token}. Used to
// preview the character checklist without a Battle.net app.
func (s *Server) joinPageDevLogin(w http.ResponseWriter, r *http.Request) {
	got := bearer(r)
	if got == "" {
		got = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	if strings.TrimSpace(s.devToken) == "" || subtleConstantTimeString(got, s.devToken) != 1 {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid dev token")
		return
	}
	token := strings.TrimSpace(r.PathValue("token"))
	if _, err := s.store.joinLinkByToken(r.Context(), token); err != nil {
		s.fail(w, err)
		return
	}
	tag := strings.TrimSpace(r.URL.Query().Get("battletag"))
	if tag == "" {
		writeErr(w, http.StatusBadRequest, "invalid", "battletag is required")
		return
	}
	p, err := s.store.PlayerByBattleTag(r.Context(), tag)
	if err != nil {
		s.fail(w, err)
		return
	}
	raw, err := s.store.IssueConsentSession(r.Context(), p.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	http.SetCookie(w, joinSessionCookie(s.publicBaseURL, raw, r))
	http.Redirect(w, r, "/join/"+url.PathEscape(token), http.StatusFound)
}

func (s *Server) joinPagePost(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.PathValue("token"))
	player, ok := s.playerFromJoinCookie(r)
	if !ok {
		http.Redirect(w, r, "/join/"+url.PathEscape(token), http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderJoinPage(w, joinPageView{
			Title: "Join failed", Error: "Could not read the form.", Done: true,
		})
		return
	}
	ids := r.Form["character_id"]
	out, err := s.store.RedeemJoinLink(r.Context(), player.ID, token, ids)
	if err == nil {
		s.announceJoin(r.Context(), out.Mode, out, player.BattleTag)
	}
	if err != nil {
		info, _ := s.store.JoinLinkInfo(r.Context(), token, player.ID)
		view := joinPageView{
			Title:      "Join " + info.GroupName,
			GroupName:  info.GroupName,
			Mode:       info.Mode,
			Token:      token,
			Who:        player.BattleTag,
			Error:      "Could not join with those characters.",
			LoginURL:   "/join/" + url.PathEscape(token) + "/login",
			SignOutURL: "/join/" + url.PathEscape(token) + "?signout=1",
		}
		for _, c := range info.Characters {
			view.Characters = append(view.Characters, joinCharFrom(c))
		}
		if len(view.Characters) == 0 {
			view.EmptyRoster = true
		}
		s.renderJoinPage(w, view)
		return
	}
	joined := out.Status == "approved"
	pending := out.Status == "pending"
	msg := "You are in."
	if pending {
		msg = "Request sent. An owner will review it."
	}
	s.renderJoinPage(w, joinPageView{
		Title:      "Join " + out.GroupName,
		GroupName:  out.GroupName,
		Message:    msg,
		InstallURL: s.installURL(),
		Done:       true,
		Joined:     joined,
		Pending:    pending,
		Who:        player.BattleTag,
	})
}

func (s *Server) renderJoinPage(w http.ResponseWriter, view joinPageView) {
	if view.InstallURL == "" {
		view.InstallURL = s.installURL()
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.WriteHeader(http.StatusOK)
	if err := joinTmpl.Execute(w, view); err != nil {
		return
	}
}

var joinTmpl = template.Must(template.New("join").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sagalore — {{.Title}}</title>
<style>` + oauthPageCSS + joinPageCSSExtra + `</style>
</head>
<body>
` + oauthChromeOpenFrame("join-frame") + `
    <h1>{{.Title}}</h1>
    <div class="divider" aria-hidden="true"><span class="line"></span><span class="gem"></span><span class="line"></span></div>
    {{if .Error}}<p class="flash err">{{.Error}}</p>{{end}}
    {{if .Message}}<p class="flash">{{.Message}}</p>{{end}}
    {{if .Done}}
      {{if or .Joined .Pending}}
        <p class="body">{{if .Joined}}Download Sagalore, sign in, and jump in-game.{{else}}While you wait, install Sagalore so you are ready when they approve.{{end}}</p>
        <div class="actions"><a class="btn allow install" href="{{.InstallURL}}">Install Sagalore</a></div>
      {{end}}
    {{else if .NeedLogin}}
        <p class="lead">Sign in with Battle.net to choose which characters join <strong>{{.GroupName}}</strong>.</p>
        {{if eq .Mode "gated"}}<p class="body">This link asks an owner to approve new members.</p>{{else}}<p class="body">This link lets you join immediately.</p>{{end}}
        <div class="actions">
          <form method="get" action="{{.LoginURL}}">
            <button class="allow" type="submit">Sign in</button>
          </form>
        </div>
    {{else if .EmptyRoster}}
        <div class="who"><strong>Signed in as</strong><span class="tag">{{.Who}}</span></div>
        {{if .SignOutURL}}<div class="who-actions"><a class="signout" href="{{.SignOutURL}}">Sign out</a></div>{{end}}
        <p class="lead">No characters yet.</p>
        <p class="body">Install Sagalore, then log into World of Warcraft with Sagalore active at least once so your characters show up here. Reload this page after.</p>
        <div class="actions"><a class="btn allow install" href="{{.InstallURL}}">Install Sagalore</a></div>
    {{else}}
        <div class="who"><strong>Signed in as</strong><span class="tag">{{.Who}}</span></div>
        {{if .SignOutURL}}<div class="who-actions"><a class="signout" href="{{.SignOutURL}}">Sign out</a></div>{{end}}
        <p class="lead chars-lead">Choose characters for <strong>{{.GroupName}}</strong></p>
        {{if eq .Mode "gated"}}<p class="body">An owner will confirm before they are added.</p>{{end}}
        <form method="post" action="/join/{{.Token}}">
          <ul class="chars">
            {{range .Characters}}
            <li>
              <label>
                <input type="checkbox" name="character_id" value="{{.ID}}">
                <span class="char-copy">
                  <span class="char-name">{{.Name}}</span>
                  <span class="char-meta">
                    {{if .Race}}<span class="char-race">{{.Race}}</span>{{end}}
                    {{if .Class}}<span class="char-class{{if .ClassSlug}} class-{{.ClassSlug}}{{end}}">{{.Class}}</span>{{end}}
                    {{if .Realm}}<span class="char-realm">{{.Realm}}</span>{{end}}
                  </span>
                </span>
              </label>
            </li>
            {{end}}
          </ul>
          <p class="hint">Don't see your character? Log into World of Warcraft on that character while Sagalore is active, then reload this page.</p>
          <div class="actions">
            <button class="allow" type="submit">{{if eq .Mode "gated"}}Request join{{else}}Join{{end}}</button>
          </div>
        </form>
    {{end}}
` + oauthChromeClose + `
</body>
</html>
`))

// oauthChromeOpenFrame is like oauthChromeOpen but allows an extra class on .frame.
func oauthChromeOpenFrame(frameClass string) string {
	return `<div class="map" aria-hidden="true"></div>
<div class="stage">
  <div class="brand">
    <img class="emblem" src="/oauth/static/emblem.png" width="88" height="88" alt="">
    <img class="wordmark" src="/oauth/static/wordmark.png" width="280" height="73" alt="Sagalore">
  </div>
  <div class="frame ` + frameClass + `">
    <div class="frame-inner">
`
}
