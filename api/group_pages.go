package main

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sagalore/sagalore/internal/contract"
)

const (
	groupsCookieName     = "sagalore_groups"
	groupsMintCookieName = "sagalore_groups_mint"
)

const groupPageCSS = `
  /* Tall consoles must start at the top. Centering a flex child taller than
     the viewport clips the top on some browsers. */
  body:has(.stage.console) {
    align-items: flex-start;
  }
  .stage.console { width: min(52rem, 100%); }
  .frame.console-frame {
    aspect-ratio: auto;
    min-height: 18rem;
    justify-content: flex-start;
    padding-top: 0.85rem;
  }
  form { margin: 0; }
  .nav {
    margin: 0.35rem 0 0;
    text-align: center;
  }
  .nav a, a.textlink {
    color: rgba(168, 184, 210, 0.82);
    font-family: "Avenir Next", "Segoe UI", "Helvetica Neue", sans-serif;
    font-size: 0.82rem;
    letter-spacing: 0.02em;
    text-decoration: underline;
    text-underline-offset: 3px;
  }
  .nav a:hover, a.textlink:hover { color: #c9d6ea; }
  .slug {
    margin: 0.15rem 0 0;
    text-align: center;
    color: rgba(220, 213, 200, 0.72);
    font-size: 0.82rem;
  }
  .slug code {
    font: inherit;
    color: #e6d7a8;
  }
  .section { margin-top: 1.15rem; text-align: left; }
  h2 {
    margin: 0 0 0.45rem;
    text-align: left;
    font: 600 0.78rem/1.2 "Avenir Next", "Segoe UI", "Helvetica Neue", sans-serif;
    letter-spacing: 0.16em;
    text-transform: uppercase;
    color: #e6d7a8;
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.75);
  }
  .note, .empty {
    margin: 0.2rem 0 0;
    text-align: left;
    color: rgba(220, 213, 200, 0.78);
    font-size: 0.9rem;
  }
  .grid {
    display: flex;
    flex-wrap: wrap;
    gap: 0.65rem 0.75rem;
    align-items: flex-end;
  }
  label.field {
    display: flex;
    flex-direction: column;
    gap: 0.22rem;
    min-width: 9rem;
    flex: 1 1 9rem;
    color: #c9c2b2;
    font-family: "Avenir Next", "Segoe UI", "Helvetica Neue", sans-serif;
    font-size: 0.75rem;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }
  input, select {
    appearance: none;
    background: rgba(6, 10, 18, 0.72);
    color: #f6f0e6;
    border: 1px solid rgba(201, 162, 74, 0.45);
    border-radius: 0.2rem;
    padding: 0.48rem 0.6rem;
    font: 1rem/1.3 var(--serif);
    text-transform: none;
    letter-spacing: 0;
  }
  input:focus, select:focus {
    outline: 1px solid var(--gold);
    border-color: var(--gold-bright);
  }
  select {
    background-image: linear-gradient(45deg, transparent 50%, #e6d7a8 50%), linear-gradient(135deg, #e6d7a8 50%, transparent 50%);
    background-position: calc(100% - 14px) 55%, calc(100% - 8px) 55%;
    background-size: 6px 6px, 6px 6px;
    background-repeat: no-repeat;
    padding-right: 1.6rem;
  }
  .actions.left { justify-content: flex-start; margin-top: 0.75rem; }
  button.mini, .btn.mini {
    min-width: 6.6rem;
    height: 2.15rem;
    font-size: 0.92rem;
    padding: 0.16rem 0.7rem 0;
  }
  .cards, .list { list-style: none; margin: 0.35rem 0 0; padding: 0; }
  .cards li, .list li {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    align-items: center;
    gap: 0.55rem 0.8rem;
    padding: 0.55rem 0.15rem;
    border-bottom: 1px solid rgba(201, 162, 74, 0.18);
  }
  .item-copy { min-width: 0; }
  .item-title {
    color: #f6f0e6;
    font-weight: 600;
  }
  .item-meta {
    color: rgba(220, 213, 200, 0.72);
    font-family: "Avenir Next", "Segoe UI", "Helvetica Neue", sans-serif;
    font-size: 0.78rem;
  }
  .badge {
    display: inline-block;
    margin-left: 0.4rem;
    padding: 0.05rem 0.4rem;
    border: 1px solid rgba(201, 162, 74, 0.45);
    border-radius: 999px;
    color: #f0d078;
    font-family: "Avenir Next", "Segoe UI", "Helvetica Neue", sans-serif;
    font-size: 0.72rem;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }
  .mint {
    margin: 0.75rem 0 0;
    padding: 0.7rem 0.8rem;
    text-align: left;
    background: rgba(201, 162, 74, 0.08);
    border: 1px solid rgba(201, 162, 74, 0.35);
    border-radius: 0.25rem;
  }
  .mint strong { color: #f0d078; font-weight: 600; }
  .mint code {
    display: block;
    margin-top: 0.35rem;
    color: #f6f0e6;
    font-family: ui-monospace, "SFMono-Regular", Menlo, Consolas, monospace;
    font-size: 0.82rem;
    word-break: break-all;
  }
  .who:has(+ .who-actions) { margin-bottom: -1.85rem; }
  .who-actions {
    display: flex;
    justify-content: center;
    position: relative;
    z-index: 1;
    margin-top: 0.2rem;
  }
  a.signout {
    color: rgba(168, 184, 210, 0.72);
    font-family: "Avenir Next", "Segoe UI", "Helvetica Neue", sans-serif;
    font-size: 0.78rem;
    letter-spacing: 0.02em;
    text-decoration: underline;
    text-underline-offset: 3px;
  }
  a.signout:hover { color: #c9d6ea; }
`

func groupsSessionCookie(publicBase, token string, r *http.Request) *http.Cookie {
	secure := r.TLS != nil || strings.HasPrefix(strings.ToLower(publicBase), "https://")
	return &http.Cookie{
		Name:     groupsCookieName,
		Value:    token,
		Path:     "/groups",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(oauthConsentTTL.Seconds()),
	}
}

func (s *Server) playerFromGroupsCookie(r *http.Request) (contract.Player, bool) {
	c, err := r.Cookie(groupsCookieName)
	if err != nil || !strings.HasPrefix(c.Value, oauthConsentPref) {
		return contract.Player{}, false
	}
	p, err := s.store.PlayerByConsentSession(r.Context(), c.Value)
	if err != nil {
		return contract.Player{}, false
	}
	return p, true
}

func groupsLoginURL(next string) string {
	next = strings.TrimSpace(next)
	if validUUID(next) != nil {
		return "/groups/login"
	}
	return "/groups/login?next=" + url.QueryEscape(next)
}

func groupsQueryFlash(r *http.Request, key string) string {
	s := strings.TrimSpace(r.URL.Query().Get(key))
	if len([]rune(s)) > 180 {
		return ""
	}
	return s
}

func (s *Server) groupsIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("signout") == "1" {
		c := groupsSessionCookie(s.publicBaseURL, "", r)
		c.MaxAge = -1
		c.Value = ""
		http.SetCookie(w, c)
		http.Redirect(w, r, "/groups", http.StatusFound)
		return
	}
	player, ok := s.playerFromGroupsCookie(r)
	if !ok {
		s.renderGroups(w, http.StatusOK, groupsPage{
			Title: "Groups", Kind: "login", LoginURL: "/groups/login",
		})
		return
	}
	page, err := s.loadGroupsIndex(r.Context(), player)
	if err != nil {
		slog.Error("group console", "err", err)
		s.renderGroups(w, http.StatusInternalServerError, groupsPage{
			Title: "Groups", Kind: "message", Error: "Something went wrong. Try again.",
		})
		return
	}
	page.Message = groupsQueryFlash(r, "ok")
	page.Error = groupsQueryFlash(r, "err")
	s.renderGroups(w, http.StatusOK, page)
}

func (s *Server) loadGroupsIndex(ctx context.Context, player contract.Player) (groupsPage, error) {
	rows, err := s.store.GroupsForPlayer(ctx, player.ID)
	if err != nil {
		return groupsPage{}, err
	}
	page := groupsPage{
		Title: "Groups", Kind: "index", Who: groupsWho(player),
		SignOutURL: "/groups?signout=1",
	}
	for _, g := range rows {
		card := groupsCard{
			ID: g.ID, Name: g.Name, Slug: g.Slug, Role: g.Role, Locale: g.Locale,
			Owner: g.Role == "owner", Personal: g.Personal,
		}
		if card.Owner && !card.Personal {
			reqs, err := s.store.ListJoinRequests(ctx, player.ID, g.ID, "pending")
			if err != nil {
				return groupsPage{}, err
			}
			card.Pending = len(reqs)
		}
		page.Groups = append(page.Groups, card)
	}
	return page, nil
}

func (s *Server) groupsLogin(w http.ResponseWriter, r *http.Request) {
	next := strings.TrimSpace(r.URL.Query().Get("next"))
	authURL, err := s.beginBnetGroupsLogin(r.Context(), next)
	if err != nil {
		if errors.Is(err, errBnetNotConfigured) {
			s.renderGroups(w, http.StatusOK, groupsPage{
				Title: "Sign-in unavailable", Kind: "message",
				Error: "Battle.net login is not configured on this server.",
			})
			return
		}
		if errors.Is(err, ErrInvalid) {
			s.renderGroups(w, http.StatusBadRequest, groupsPage{
				Title: "Sign-in unavailable", Kind: "message",
				Error: "That return link is not a group.",
			})
			return
		}
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (s *Server) groupsDevLogin(w http.ResponseWriter, r *http.Request) {
	got := bearer(r)
	if got == "" {
		got = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	if strings.TrimSpace(s.devToken) == "" || subtleConstantTimeString(got, s.devToken) != 1 {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid dev token")
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
	http.SetCookie(w, groupsSessionCookie(s.publicBaseURL, raw, r))
	next := strings.TrimSpace(r.URL.Query().Get("next"))
	dest := "/groups"
	if validUUID(next) == nil {
		dest = "/groups/" + url.PathEscape(next)
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

func (s *Server) groupsCreate(w http.ResponseWriter, r *http.Request) {
	player, ok := s.playerFromGroupsCookie(r)
	if !ok {
		http.Redirect(w, r, "/groups/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.groupsRedirect(w, r, "/groups", "err", "Could not read the form.")
		return
	}
	out, err := s.store.CreateGroup(r.Context(), player.ID,
		r.FormValue("slug"), r.FormValue("name"), r.FormValue("locale"))
	if err != nil {
		msg := groupsActionError(err)
		if errors.Is(err, ErrConflict) {
			msg = "A group with that slug already exists."
		}
		if !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrConflict) && !errors.Is(err, ErrForbidden) {
			slog.Error("group console", "err", err)
			msg = "Something went wrong. Try again."
		}
		s.groupsRedirect(w, r, "/groups", "err", msg)
		return
	}
	s.groupsRedirect(w, r, "/groups/"+url.PathEscape(out.ID), "ok", "Group created.")
}

func (s *Server) groupsConsole(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	player, ok := s.playerFromGroupsCookie(r)
	if !ok {
		http.Redirect(w, r, groupsLoginURL(id), http.StatusFound)
		return
	}
	page, err := s.loadGroupsConsole(r.Context(), player, id)
	if err != nil {
		if errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalid) {
			s.renderGroups(w, http.StatusForbidden, groupsPage{
				Title: "Groups", Kind: "message", Who: groupsWho(player),
				SignOutURL: "/groups?signout=1",
				Error:      "You cannot manage this group.",
			})
			return
		}
		slog.Error("group console", "err", err)
		s.renderGroups(w, http.StatusInternalServerError, groupsPage{
			Title: "Groups", Kind: "message", Error: "Something went wrong. Try again.",
		})
		return
	}
	page.Message = groupsQueryFlash(r, "ok")
	page.Error = groupsQueryFlash(r, "err")
	if minted := s.takeMintedURL(w, r, id); minted != "" {
		page.MintedURL = minted
	}
	s.renderGroups(w, http.StatusOK, page)
}

func (s *Server) groupsConsolePost(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	back := "/groups/" + url.PathEscape(id)
	player, ok := s.playerFromGroupsCookie(r)
	if !ok {
		http.Redirect(w, r, groupsLoginURL(id), http.StatusSeeOther)
		return
	}
	if err := s.store.requireOwner(r.Context(), player.ID, id); err != nil {
		s.renderGroups(w, http.StatusForbidden, groupsPage{
			Title: "Groups", Kind: "message", Who: groupsWho(player),
			SignOutURL: "/groups?signout=1",
			Error:      "You cannot manage this group.",
		})
		return
	}
	if err := r.ParseForm(); err != nil {
		s.groupsRedirect(w, r, back, "err", "Could not read the form.")
		return
	}
	action := strings.TrimSpace(r.FormValue("action"))
	var minted string
	var err error
	switch action {
	case "save":
		_, err = s.store.UpdateGroup(r.Context(), player.ID, id, r.FormValue("name"), r.FormValue("locale"))
	case "invite":
		_, err = s.store.CreateInvite(r.Context(), player.ID, id,
			r.FormValue("name"), r.FormValue("realm"), r.FormValue("battletag"))
	case "revoke-invite":
		err = s.store.RevokeInvite(r.Context(), player.ID, id, r.FormValue("invite_id"))
	case "mint-link":
		var maxUses *int
		if raw := strings.TrimSpace(r.FormValue("max_uses")); raw != "" {
			n, nerr := strconv.Atoi(raw)
			if nerr != nil {
				s.groupsRedirect(w, r, back, "err", "Max uses must be a number, or left blank.")
				return
			}
			maxUses = &n
		}
		sec, nerr := strconv.Atoi(strings.TrimSpace(r.FormValue("expires_in_sec")))
		if nerr != nil || sec <= 0 {
			sec = int(defaultJoinLinkTTL.Seconds())
		}
		_, raw, cerr := s.store.CreateJoinLink(r.Context(), player.ID, id, r.FormValue("mode"), sec, maxUses)
		err = cerr
		if err == nil {
			minted = s.joinLinkURL(raw)
		}
	case "revoke-link":
		err = s.store.RevokeJoinLink(r.Context(), player.ID, id, r.FormValue("link_id"))
	case "approve", "reject":
		status := "approved"
		if action == "reject" {
			status = "rejected"
		}
		var out contract.JoinRequest
		out, err = s.store.AnswerJoinRequest(r.Context(), player.ID, id, r.FormValue("request_id"), status)
		if err == nil {
			s.announceJoin(r.Context(), out.Mode, out, out.BattleTag)
		}
	case "kick":
		err = s.store.RemoveMember(r.Context(), player.ID, id, r.FormValue("player_id"))
	default:
		err = fmt.Errorf("%w: action", ErrInvalid)
	}
	if err != nil {
		if errors.Is(err, ErrForbidden) && !strings.Contains(err.Error(), "personal") {
			s.renderGroups(w, http.StatusForbidden, groupsPage{
				Title: "Groups", Kind: "message", Who: groupsWho(player),
				SignOutURL: "/groups?signout=1",
				Error:      "You cannot manage this group.",
			})
			return
		}
		msg := groupsActionError(err)
		if !knownGroupsErr(err) {
			slog.Error("group console", "err", err)
			msg = "Something went wrong. Try again."
		}
		s.groupsRedirect(w, r, back, "err", msg)
		return
	}
	if minted != "" {
		http.SetCookie(w, groupsMintCookie(s.publicBaseURL, id, minted, r))
	}
	s.groupsRedirect(w, r, back, "ok", groupsActionOK(action))
}

func knownGroupsErr(err error) bool {
	return errors.Is(err, ErrInvalid) || errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict)
}

func groupsActionOK(action string) string {
	switch action {
	case "save":
		return "Group updated."
	case "invite":
		return "Invite sent."
	case "revoke-invite":
		return "Invite revoked."
	case "mint-link":
		return "Join link created. Copy it now. It is shown once."
	case "revoke-link":
		return "Join link revoked."
	case "approve":
		return "Request approved."
	case "reject":
		return "Request rejected."
	case "kick":
		return "Member removed."
	default:
		return "Saved."
	}
}

func groupsActionError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrForbidden) && strings.Contains(err.Error(), "personal") {
		return "Personal groups cannot use invites or join links."
	}
	if errors.Is(err, ErrForbidden) {
		return "Only the owner can do that."
	}
	if errors.Is(err, ErrNotFound) {
		return "That no longer exists. Refresh the page."
	}
	if errors.Is(err, ErrConflict) {
		switch {
		case strings.Contains(err.Error(), "revoked"):
			return "That link is already revoked."
		case strings.Contains(err.Error(), "request"):
			return "That request was already answered."
		default:
			return "That conflicts with the current state. Refresh and try again."
		}
	}
	if errors.Is(err, ErrInvalid) {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "character name"):
			return "Character names are 2–24 characters with no spaces."
		case strings.Contains(msg, "realm"):
			return "Enter the realm name."
		case strings.Contains(msg, "battletag"):
			return "BattleTag looks like Name#1234, or leave it blank."
		case strings.Contains(msg, "group name"):
			return "Enter a group name (80 characters or fewer)."
		case strings.Contains(msg, "expires_in_sec"):
			return "Join links last between 1 hour and 90 days."
		case strings.Contains(msg, "max_uses"):
			return "Max uses must be at least 1, or left blank."
		case strings.Contains(msg, "mode"):
			return "Choose an open or gated link."
		case strings.Contains(msg, "owner leaves"):
			return "Remove another member. The owner stays until they leave."
		default:
			return "Check the form and try again."
		}
	}
	return "Something went wrong. Try again."
}

func (s *Server) groupsRedirect(w http.ResponseWriter, r *http.Request, path, key, msg string) {
	if msg != "" {
		q := url.Values{}
		q.Set(key, msg)
		path += "?" + q.Encode()
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
}

func groupsMintCookie(publicBase, groupID, linkURL string, r *http.Request) *http.Cookie {
	c := groupsSessionCookie(publicBase, groupID+"|"+linkURL, r)
	c.Name = groupsMintCookieName
	c.MaxAge = 120
	return c
}

func (s *Server) takeMintedURL(w http.ResponseWriter, r *http.Request, groupID string) string {
	c, err := r.Cookie(groupsMintCookieName)
	if err != nil || strings.TrimSpace(c.Value) == "" {
		return ""
	}
	clear := groupsSessionCookie(s.publicBaseURL, "", r)
	clear.Name = groupsMintCookieName
	clear.MaxAge = -1
	http.SetCookie(w, clear)
	id, link, ok := strings.Cut(c.Value, "|")
	if !ok || id != groupID {
		return ""
	}
	return safeJoinURL(link)
}

func safeJoinURL(link string) string {
	link = strings.TrimSpace(link)
	if strings.HasPrefix(link, "/join/") && !strings.ContainsAny(link, "\\\r\n") && !strings.Contains(link, "..") {
		return link
	}
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	if !strings.HasPrefix(u.Path, "/join/") || strings.Contains(u.Path, "..") {
		return ""
	}
	return u.String()
}

func (s *Server) loadGroupsConsole(ctx context.Context, player contract.Player, groupID string) (groupsPage, error) {
	rows, err := s.store.GroupsForPlayer(ctx, player.ID)
	if err != nil {
		return groupsPage{}, err
	}
	var g *contract.GroupSummary
	for i := range rows {
		if rows[i].ID == groupID && rows[i].Role == "owner" {
			g = &rows[i]
			break
		}
	}
	if g == nil {
		return groupsPage{}, ErrForbidden
	}
	members, err := s.store.ListGroupMembers(ctx, player.ID, groupID)
	if err != nil {
		return groupsPage{}, err
	}
	page := groupsPage{
		Title: g.Name, Kind: "console", Who: groupsWho(player),
		SignOutURL: "/groups?signout=1",
		GroupID:    g.ID, Name: g.Name, Slug: g.Slug, Locale: g.Locale, Personal: g.Personal,
	}
	for _, m := range members {
		page.Members = append(page.Members, groupsMember{
			PlayerID: m.PlayerID,
			Label:    memberLabel(m),
			Role:     m.Role,
			Joined:   formatWhen(m.JoinedAt),
			CanKick:  m.Role != "owner" && m.PlayerID != player.ID,
		})
	}
	if g.Personal {
		return page, nil
	}
	invites, err := s.store.ListGroupInvites(ctx, player.ID, groupID, "pending")
	if err != nil {
		return groupsPage{}, err
	}
	for _, inv := range invites {
		page.Invites = append(page.Invites, groupsInvite{
			ID: inv.ID, Name: inv.Name, Realm: inv.Realm, BattleTag: inv.BattleTag,
			When: formatWhen(inv.InvitedAt),
		})
	}
	links, err := s.store.ListJoinLinks(ctx, player.ID, groupID)
	if err != nil {
		return groupsPage{}, err
	}
	now := time.Now()
	for _, link := range links {
		page.Links = append(page.Links, groupsLinkFrom(link, now))
	}
	reqs, err := s.store.ListJoinRequests(ctx, player.ID, groupID, "pending")
	if err != nil {
		return groupsPage{}, err
	}
	var charIDs []string
	for _, req := range reqs {
		charIDs = append(charIDs, req.CharacterIDs...)
	}
	labels, err := s.store.characterLabels(ctx, charIDs)
	if err != nil {
		return groupsPage{}, err
	}
	for _, req := range reqs {
		var parts []string
		for _, id := range req.CharacterIDs {
			if label := labels[id]; label != "" {
				parts = append(parts, label)
			}
		}
		who := strings.TrimSpace(req.BattleTag)
		if who == "" {
			who = strings.TrimSpace(req.DisplayName)
		}
		page.Requests = append(page.Requests, groupsRequest{
			ID: req.ID, Who: who, When: formatWhen(req.CreatedAt),
			Characters: strings.Join(parts, ", "),
		})
	}
	return page, nil
}

func groupsLinkFrom(link contract.JoinLink, now time.Time) groupsLink {
	mode := "Gated"
	if link.Mode == "open" {
		mode = "Open"
	}
	uses := fmt.Sprintf("%d uses", link.UseCount)
	if link.MaxUses != nil {
		uses = fmt.Sprintf("%d / %d uses", link.UseCount, *link.MaxUses)
	}
	status := "Active"
	active := link.RevokedAt == ""
	if link.RevokedAt != "" {
		status = "Revoked"
		active = false
	} else if t, err := time.Parse(time.RFC3339, link.ExpiresAt); err == nil && !t.After(now) {
		status = "Expired"
	}
	return groupsLink{
		ID: link.ID, Mode: mode, Expires: formatWhen(link.ExpiresAt),
		Uses: uses, Status: status, Active: active,
	}
}

func (s *Store) characterLabels(ctx context.Context, ids []string) (map[string]string, error) {
	out := map[string]string{}
	seen := map[string]struct{}{}
	clean := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if validUUID(id) != nil {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		clean = append(clean, id)
	}
	if len(clean) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, name, realm, COALESCE(class, '')
		FROM characters WHERE id::text = ANY($1)`, clean)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, realm, class string
		if err := rows.Scan(&id, &name, &realm, &class); err != nil {
			return nil, err
		}
		label := strings.TrimSpace(name)
		if class = strings.TrimSpace(class); class != "" {
			label += " · " + class
		}
		if realm = strings.TrimSpace(realm); realm != "" {
			label += " · " + realm
		}
		out[id] = label
	}
	return out, rows.Err()
}

func groupsWho(p contract.Player) string {
	if tag := strings.TrimSpace(p.BattleTag); tag != "" {
		return tag
	}
	return strings.TrimSpace(p.DisplayName)
}

func memberLabel(m contract.GroupMemberRow) string {
	tag := strings.TrimSpace(m.BattleTag)
	name := strings.TrimSpace(m.DisplayName)
	switch {
	case tag != "" && name != "" && !strings.EqualFold(tag, name):
		return tag + " (" + name + ")"
	case tag != "":
		return tag
	default:
		return name
	}
}

func formatWhen(rfc string) string {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(rfc))
	if err != nil {
		return strings.TrimSpace(rfc)
	}
	return t.UTC().Format("2 Jan 2006 15:04 UTC")
}

type groupsPage struct {
	Title      string
	Kind       string
	Who        string
	SignOutURL string
	LoginURL   string
	Message    string
	Error      string
	MintedURL  string
	Groups     []groupsCard
	GroupID    string
	Name       string
	Slug       string
	Locale     string
	Personal   bool
	Members    []groupsMember
	Invites    []groupsInvite
	Links      []groupsLink
	Requests   []groupsRequest
}

type groupsCard struct {
	ID, Name, Slug, Role, Locale string
	Owner, Personal              bool
	Pending                      int
}

type groupsMember struct {
	PlayerID, Label, Role, Joined string
	CanKick                       bool
}

type groupsInvite struct {
	ID, Name, Realm, BattleTag, When string
}

type groupsLink struct {
	ID, Mode, Expires, Uses, Status string
	Active                          bool
}

type groupsRequest struct {
	ID, Who, When, Characters string
}

func (s *Server) renderGroups(w http.ResponseWriter, status int, page groupsPage) {
	if page.Title == "" {
		page.Title = "Groups"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.WriteHeader(status)
	if err := groupsTmpl.Execute(w, page); err != nil {
		slog.Error("group console render", "err", err)
	}
}

const groupsChromeOpen = `<div class="map" aria-hidden="true"></div>
<div class="stage console">
  <div class="brand">
    <img class="emblem" src="/oauth/static/emblem.png" width="88" height="88" alt="">
    <img class="wordmark" src="/oauth/static/wordmark.png" width="280" height="73" alt="Sagalore">
  </div>
  <div class="frame console-frame">
    <div class="frame-inner">
`

var groupsTmpl = template.Must(template.New("groups").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sagalore — {{.Title}}</title>
<style>` + oauthPageCSS + groupPageCSS + `</style>
</head>
<body>
` + groupsChromeOpen + `
    <h1>{{.Title}}</h1>
    <div class="divider" aria-hidden="true"><span class="line"></span><span class="gem"></span><span class="line"></span></div>
    {{if eq .Kind "login"}}
      <p class="lead">Sign in with Battle.net to manage groups you own.</p>
      <p class="body">Create a group, invite a character by name and realm, mint or revoke join links, approve requests, and remove members. Characters come from Battle.net. This page does not create or edit them.</p>
      <div class="actions">
        <form method="get" action="{{.LoginURL}}">
          <button class="allow" type="submit">Sign in</button>
        </form>
      </div>
    {{else if eq .Kind "message"}}
      {{if .Error}}<p class="flash err">{{.Error}}</p>{{end}}
      {{if .Message}}<p class="flash">{{.Message}}</p>{{end}}
      <p class="nav"><a href="/groups">Back to groups</a></p>
    {{else if eq .Kind "index"}}
      {{if .Who}}<div class="who"><strong>Signed in as</strong><span class="tag">{{.Who}}</span></div>{{end}}
      {{if .SignOutURL}}<div class="who-actions"><a class="signout" href="{{.SignOutURL}}">Sign out</a></div>{{end}}
      {{if .Error}}<p class="flash err">{{.Error}}</p>{{end}}
      {{if .Message}}<p class="flash">{{.Message}}</p>{{end}}
      <p class="lead">Groups you belong to. Only an owner can manage a group.</p>
      {{if .Groups}}
      <ul class="cards">
        {{range .Groups}}
        <li>
          <div class="item-copy">
            <div class="item-title">{{.Name}}{{if .Personal}}<span class="badge">Personal</span>{{end}}{{if gt .Pending 0}}<span class="badge">{{.Pending}} waiting</span>{{end}}</div>
            <div class="item-meta">{{.Slug}} · {{if .Owner}}owner{{else}}member{{end}} · {{.Locale}}</div>
          </div>
          {{if .Owner}}<a class="btn allow mini" href="/groups/{{.ID}}">Manage</a>{{else}}<span class="item-meta">Owner only</span>{{end}}
        </li>
        {{end}}
      </ul>
      {{else}}
      <p class="empty">You are not in a group yet.</p>
      {{end}}
      <section class="section">
        <h2>Create a group</h2>
        <form method="post" action="/groups">
          <div class="grid">
            <label class="field">Name<input name="name" required maxlength="80" autocomplete="off"></label>
            <label class="field">Slug<input name="slug" maxlength="42" autocapitalize="off" autocomplete="off" placeholder="optional"></label>
            <label class="field">Locale
              <select name="locale">
                <option value="en" selected>English</option>
                <option value="nb">Norsk</option>
              </select>
            </label>
          </div>
          <div class="actions left"><button class="allow mini" type="submit">Create group</button></div>
        </form>
      </section>
    {{else if eq .Kind "console"}}
      <p class="slug">Slug <code>{{.Slug}}</code> stays as it is. Saga writes use it.</p>
      <p class="nav"><a href="/groups">All groups</a></p>
      {{if .Who}}<div class="who"><strong>Signed in as</strong><span class="tag">{{.Who}}</span></div>{{end}}
      {{if .SignOutURL}}<div class="who-actions"><a class="signout" href="{{.SignOutURL}}">Sign out</a></div>{{end}}
      {{if .Error}}<p class="flash err">{{.Error}}</p>{{end}}
      {{if .Message}}<p class="flash">{{.Message}}</p>{{end}}
      {{if .MintedURL}}
      <div class="mint">
        <strong>Copy this join link now.</strong> It is not shown again.
        <code>{{.MintedURL}}</code>
      </div>
      {{end}}
      <section class="section">
        <h2>Group</h2>
        <form method="post" action="/groups/{{.GroupID}}">
          <input type="hidden" name="action" value="save">
          <div class="grid">
            <label class="field">Name<input name="name" required maxlength="80" value="{{.Name}}"></label>
            <label class="field">Locale
              <select name="locale">
                <option value="en" {{if eq .Locale "en"}}selected{{end}}>English</option>
                <option value="nb" {{if eq .Locale "nb"}}selected{{end}}>Norsk</option>
              </select>
            </label>
          </div>
          <div class="actions left"><button class="allow mini" type="submit">Save</button></div>
        </form>
      </section>
      <section class="section">
        <h2>Members</h2>
        <ul class="list">
          {{range .Members}}
          <li>
            <div class="item-copy">
              <div class="item-title">{{.Label}}</div>
              <div class="item-meta">{{.Role}}{{if .Joined}} · joined {{.Joined}}{{end}}</div>
            </div>
            {{if .CanKick}}
            <form method="post" action="/groups/{{$.GroupID}}">
              <input type="hidden" name="action" value="kick">
              <input type="hidden" name="player_id" value="{{.PlayerID}}">
              <button class="deny mini" type="submit">Remove</button>
            </form>
            {{end}}
          </li>
          {{end}}
        </ul>
      </section>
      {{if .Personal}}
      <section class="section">
        <h2>Invites and join links</h2>
        <p class="note">This is your personal group. Invites and join links stay off. Characters on your Battle.net account already belong here.</p>
      </section>
      {{else}}
      <section class="section">
        <h2>Invite a character</h2>
        <p class="note">Name and realm only. This does not create a character. The player accepts in game, or the name matches a character already synced from Battle.net.</p>
        <form method="post" action="/groups/{{.GroupID}}">
          <input type="hidden" name="action" value="invite">
          <div class="grid">
            <label class="field">Character<input name="name" required maxlength="24" autocomplete="off"></label>
            <label class="field">Realm<input name="realm" required maxlength="64" autocomplete="off"></label>
            <label class="field">BattleTag<input name="battletag" maxlength="64" autocomplete="off" placeholder="optional"></label>
          </div>
          <div class="actions left"><button class="allow mini" type="submit">Send invite</button></div>
        </form>
        {{if .Invites}}
        <ul class="list">
          {{range .Invites}}
          <li>
            <div class="item-copy">
              <div class="item-title">{{.Name}} · {{.Realm}}</div>
              <div class="item-meta">Pending{{if .BattleTag}} · {{.BattleTag}}{{end}}{{if .When}} · {{.When}}{{end}}</div>
            </div>
            <form method="post" action="/groups/{{$.GroupID}}">
              <input type="hidden" name="action" value="revoke-invite">
              <input type="hidden" name="invite_id" value="{{.ID}}">
              <button class="deny mini" type="submit">Revoke</button>
            </form>
          </li>
          {{end}}
        </ul>
        {{else}}
        <p class="empty">No pending invites.</p>
        {{end}}
      </section>
      <section class="section">
        <h2>Join links</h2>
        <p class="note">Open links join immediately after Battle.net sign-in. Gated links wait for you to approve. The player picks characters the companion already synced.</p>
        <form method="post" action="/groups/{{.GroupID}}">
          <input type="hidden" name="action" value="mint-link">
          <div class="grid">
            <label class="field">Mode
              <select name="mode">
                <option value="gated" selected>Gated</option>
                <option value="open">Open</option>
              </select>
            </label>
            <label class="field">Duration
              <select name="expires_in_sec">
                <option value="3600">1 hour</option>
                <option value="86400">1 day</option>
                <option value="604800" selected>7 days</option>
                <option value="2592000">30 days</option>
                <option value="7776000">90 days</option>
              </select>
            </label>
            <label class="field">Max uses<input name="max_uses" inputmode="numeric" placeholder="unlimited"></label>
          </div>
          <div class="actions left"><button class="allow mini" type="submit">Create join link</button></div>
        </form>
        {{if .Links}}
        <ul class="list">
          {{range .Links}}
          <li>
            <div class="item-copy">
              <div class="item-title">{{.Mode}} · {{.Status}}</div>
              <div class="item-meta">{{.Uses}}{{if .Expires}} · until {{.Expires}}{{end}}</div>
            </div>
            {{if .Active}}
            <form method="post" action="/groups/{{$.GroupID}}">
              <input type="hidden" name="action" value="revoke-link">
              <input type="hidden" name="link_id" value="{{.ID}}">
              <button class="deny mini" type="submit">Revoke</button>
            </form>
            {{end}}
          </li>
          {{end}}
        </ul>
        {{else}}
        <p class="empty">No join links yet.</p>
        {{end}}
      </section>
      <section class="section">
        <h2>Join requests</h2>
        {{if .Requests}}
        <ul class="list">
          {{range .Requests}}
          <li>
            <div class="item-copy">
              <div class="item-title">{{.Who}}</div>
              <div class="item-meta">{{if .Characters}}{{.Characters}}{{else}}No characters listed{{end}}{{if .When}} · {{.When}}{{end}}</div>
            </div>
            <form method="post" action="/groups/{{$.GroupID}}">
              <input type="hidden" name="action" value="approve">
              <input type="hidden" name="request_id" value="{{.ID}}">
              <button class="allow mini" type="submit">Approve</button>
            </form>
            <form method="post" action="/groups/{{$.GroupID}}">
              <input type="hidden" name="action" value="reject">
              <input type="hidden" name="request_id" value="{{.ID}}">
              <button class="deny mini" type="submit">Reject</button>
            </form>
          </li>
          {{end}}
        </ul>
        {{else}}
        <p class="empty">No pending requests.</p>
        {{end}}
      </section>
      {{end}}
    {{end}}
` + oauthChromeClose + `
</body>
</html>
`))
