package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sagalore/sagalore/internal/contract"
	"github.com/sagalore/sagalore/internal/realtime"
	"github.com/sagalore/sagalore/internal/screenshot"
	"github.com/sagalore/sagalore/internal/webhook"
)

type Server struct {
	store            *Store
	adminToken       string
	publicBaseURL    string
	imageURLSecret   string
	imageURLTTL      time.Duration
	webhooks         *webhook.Queue
	events           *realtime.Hub
	devToken         string
	bnetClientID     string
	bnetClientSecret string
	bnet             *bnetFlow
}

type ctxKey int

const playerCtx ctxKey = 1

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.oauthASMetadata)
	mux.HandleFunc("OPTIONS /.well-known/oauth-authorization-server", s.oauthOptions)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", s.oauthResourceMetadata)
	mux.HandleFunc("OPTIONS /.well-known/oauth-protected-resource", s.oauthOptions)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/{rest...}", s.oauthResourceMetadata)
	mux.HandleFunc("OPTIONS /.well-known/oauth-protected-resource/{rest...}", s.oauthOptions)
	mux.HandleFunc("POST /oauth/register", s.oauthRegister)
	mux.HandleFunc("OPTIONS /oauth/register", s.oauthOptions)
	mux.HandleFunc("GET /oauth/authorize", s.oauthAuthorize)
	mux.HandleFunc("POST /oauth/authorize", s.oauthAuthorizeDecision)
	mux.HandleFunc("GET /oauth/continue", s.oauthContinue)
	mux.HandleFunc("GET /oauth/static/{name}", s.oauthStatic)
	mux.HandleFunc("POST /oauth/token", s.oauthToken)
	mux.HandleFunc("OPTIONS /oauth/token", s.oauthOptions)
	mux.HandleFunc("POST /oauth/revoke", s.oauthRevoke)
	mux.HandleFunc("OPTIONS /oauth/revoke", s.oauthOptions)
	mux.HandleFunc("GET /v1/auth/bnet/start", s.bnetStart)
	mux.HandleFunc("GET /v1/auth/bnet/callback", s.bnetCallback)
	mux.HandleFunc("GET /v1/auth/bnet/poll", s.bnetPoll)
	mux.HandleFunc("POST /v1/auth/dev", s.devLogin)
	mux.HandleFunc("PUT /v1/characters", s.withPlayer(s.upsertCharacter))
	mux.HandleFunc("PATCH /v1/characters/{id}", s.withPlayer(s.patchCharacterParticipating))
	mux.HandleFunc("GET /v1/sync", s.withPlayer(s.sync))
	mux.HandleFunc("GET /v1/ws", s.serveWS)
	mux.HandleFunc("POST /v1/complete", s.withPlayer(s.complete))
	mux.HandleFunc("DELETE /v1/progress", s.withPlayer(s.deleteProgress))
	mux.HandleFunc("POST /v1/level", s.withPlayer(s.level))
	mux.HandleFunc("POST /v1/sagas", s.withMember(s.createSaga))
	mux.HandleFunc("GET /v1/sagas", s.withPlayer(s.listSagas))
	mux.HandleFunc("GET /v1/sagas/{id}", s.withPlayer(s.getSaga))
	mux.HandleFunc("PATCH /v1/sagas/{id}", s.withMember(s.patchSaga))
	mux.HandleFunc("PUT /v1/sagas/{id}/image", s.withPlayer(s.putSagaImage))
	mux.HandleFunc("DELETE /v1/sagas/{id}/image", s.withPlayer(s.deleteSagaImage))
	mux.HandleFunc("GET /v1/sagas/{id}/image", s.withPlayer(s.getSagaImage))
	mux.HandleFunc("GET /v1/public/sagas/{id}/image", s.getPublicSagaImage)
	mux.HandleFunc("GET /v1/public/placeholder.png", servePlaceholderPNG)
	mux.HandleFunc("GET /v1/public/empty.png", servePlaceholderPNG)
	mux.HandleFunc("POST /v1/groups", s.withMember(s.createGroup))
	mux.HandleFunc("GET /v1/groups", s.withPlayer(s.listGroups))
	mux.HandleFunc("GET /v1/groups/{id}/members", s.withPlayer(s.listGroupMembers))
	mux.HandleFunc("GET /v1/groups/{id}/leaderboard", s.withPlayer(s.groupLeaderboard))
	mux.HandleFunc("GET /v1/groups/{id}/invites", s.withPlayer(s.listGroupInvites))
	mux.HandleFunc("POST /v1/groups/{id}/invites", s.withMember(s.createInvite))
	mux.HandleFunc("DELETE /v1/groups/{id}/invites/{inviteId}", s.withMember(s.revokeInvite))
	mux.HandleFunc("GET /v1/me/invites", s.withPlayer(s.listMyInvites))
	mux.HandleFunc("POST /v1/invites/{id}/accept", s.withPlayer(s.acceptInvite))
	mux.HandleFunc("POST /v1/invites/{id}/decline", s.withPlayer(s.declineInvite))
	mux.HandleFunc("POST /v1/groups/{id}/leave", s.withPlayer(s.leaveGroup))
	mux.HandleFunc("DELETE /v1/groups/{id}/members/{playerID}", s.withMember(s.removeMember))
	mux.HandleFunc("PUT /v1/groups/{id}/webhook", s.withMember(s.putGroupWebhook))
	mux.HandleFunc("PUT /v1/groups/{id}/locale", s.withMember(s.putGroupLocale))
	mux.HandleFunc("POST /v1/me/tokens", s.withPlayer(s.mintToken))
	mux.HandleFunc("DELETE /v1/me/tokens/{id}", s.withPlayer(s.revokeToken))
	mux.HandleFunc("POST /v1/admin/players", s.withAdmin(s.createPlayer))
	mux.HandleFunc("GET /v1/admin/players", s.withAdmin(s.listPlayers))
	mux.HandleFunc("DELETE /v1/admin/sagas/{id}", s.withAdmin(s.deleteAdminSaga))
	mux.HandleFunc("DELETE /v1/admin/groups/{id}", s.withAdmin(s.deleteAdminGroup))
	mux.HandleFunc("POST /v1/admin/characters", s.withAdmin(s.createCharacter))
	mux.HandleFunc("GET /v1/admin/characters", s.withAdmin(s.listCharacters))
	mux.HandleFunc("GET /v1/admin/characters/{id}", s.withAdmin(s.getCharacter))
	mux.HandleFunc("PATCH /v1/admin/characters/{id}", s.withAdmin(s.patchCharacter))
	mux.HandleFunc("DELETE /v1/admin/characters/{id}", s.withAdmin(s.deleteCharacter))
	mux.HandleFunc("POST /v1/admin/sagas", s.rejectAdminWrite)
	mux.HandleFunc("GET /v1/admin/sagas", s.withAdmin(s.listQuests))
	mux.HandleFunc("GET /v1/admin/sagas/{id}", s.withAdmin(s.getQuest))
	mux.HandleFunc("GET /v1/admin/sagas/{id}/image", s.withAdmin(s.getAdminQuestImage))
	mux.HandleFunc("PATCH /v1/admin/sagas/{id}", s.rejectAdminWrite)
	mux.HandleFunc("PUT /v1/admin/sagas/{id}/image", s.withAdmin(s.putAdminQuestImage))
	mux.HandleFunc("DELETE /v1/admin/sagas/{id}/image", s.withAdmin(s.deleteAdminQuestImage))
	mux.HandleFunc("GET /v1/admin/log", s.withAdmin(s.listLog))
	mux.HandleFunc("POST /v1/admin/log", s.withAdmin(s.createLog))
	mux.HandleFunc("DELETE /v1/admin/progress", s.withAdmin(s.deleteAdminProgress))
	return mux
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "unhealthy", "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) writePlayerUnauthorized(w http.ResponseWriter, msg string, invalidToken bool) {
	var challenge strings.Builder
	challenge.WriteString("Bearer")
	wrote := false
	if invalidToken {
		challenge.WriteString(` error="invalid_token"`)
		wrote = true
	}
	if meta := s.resourceMetadataURL(); meta != "" && !strings.ContainsAny(meta, "\"\\") {
		if wrote {
			challenge.WriteString(",")
		}
		challenge.WriteString(` resource_metadata="`)
		challenge.WriteString(meta)
		challenge.WriteString(`"`)
		wrote = true
	}
	if wrote {
		w.Header().Set("WWW-Authenticate", challenge.String())
	}
	writeErr(w, http.StatusUnauthorized, "unauthorized", msg)
}

func (s *Server) withPlayer(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := bearer(r)
		if key == "" {
			s.writePlayerUnauthorized(w, "missing API key", false)
			return
		}
		p, err := s.store.PlayerByKey(r.Context(), key)
		if errors.Is(err, ErrForbidden) {
			s.writePlayerUnauthorized(w, "invalid API key", true)
			return
		}
		if err != nil {
			s.fail(w, err)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), playerCtx, p)))
	}
}

func (s *Server) rejectAdminWrite(w http.ResponseWriter, r *http.Request) {
	writeErr(w, http.StatusForbidden, "admin_cannot_author", "ADMIN_TOKEN cannot author sagas or change groups")
}

func (s *Server) withAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.adminToken == "" {
			writeErr(w, http.StatusServiceUnavailable, "admin_disabled", "ADMIN_TOKEN is not set")
			return
		}
		got := bearer(r)
		if got == "" || subtleConstantTimeString(got, s.adminToken) == 0 {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid admin token")
			return
		}
		next(w, r)
	}
}

func playerFrom(r *http.Request) contract.Player {
	p, _ := r.Context().Value(playerCtx).(contract.Player)
	return p
}

func (s *Server) sync(w http.ResponseWriter, r *http.Request) {
	p := playerFrom(r)
	if q := strings.TrimSpace(r.URL.Query().Get("battletag")); q != "" && !strings.EqualFold(q, p.BattleTag) {
		writeErr(w, http.StatusForbidden, "forbidden", "battletag does not match the API key")
		return
	}
	out, err := s.store.Sync(r.Context(), p.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	out.DisplayName = p.DisplayName
	if out.BattleTag == "" {
		out.BattleTag = p.BattleTag
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) complete(w http.ResponseWriter, r *http.Request) {
	p := playerFrom(r)
	in, image, contentType, err := readCompletion(w, r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	if len(image) > 0 {
		image, contentType, err = screenshot.NormalizeUpload(image)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid", err.Error())
			return
		}
	}
	res, err := s.store.Complete(r.Context(), p.ID, in, image, contentType)
	if err != nil {
		if errors.Is(err, ErrForbidden) {
			writeJSON(w, http.StatusForbidden, res)
			return
		}
		s.fail(w, err)
		return
	}
	if res.Completed && !res.AlreadyCompleted {
		when := time.Now().UTC()
		if strings.TrimSpace(in.CompletedAt) != "" {
			if t, err := time.Parse(time.RFC3339, in.CompletedAt); err == nil {
				when = t.UTC()
			}
		}
		if s.announceCompleted(r.Context(), in.QuestID, res, in, when) {
			res.WebhookPosted = true
		}
	}
	writeJSON(w, http.StatusOK, res)
}

// deleteProgress clears API progress for the signed-in player so a local
// /sagalore reset is not undone by the next companion sync.
// Optional query saga_id limits to one saga; omit it to clear all.
func (s *Server) deleteProgress(w http.ResponseWriter, r *http.Request) {
	p := playerFrom(r)
	questID := strings.TrimSpace(r.URL.Query().Get("saga_id"))
	n, err := s.store.DeletePlayerProgress(r.Context(), p.ID, questID)
	if err != nil {
		if errors.Is(err, ErrInvalid) {
			writeErr(w, http.StatusBadRequest, "invalid", err.Error())
			return
		}
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": n, "saga_id": questID})
}

func (s *Server) deleteAdminProgress(w http.ResponseWriter, r *http.Request) {
	characterID := strings.TrimSpace(r.URL.Query().Get("character_id"))
	questID := strings.TrimSpace(r.URL.Query().Get("saga_id"))
	if characterID == "" && questID == "" {
		writeErr(w, http.StatusBadRequest, "invalid", "character_id or saga_id is required")
		return
	}
	n, err := s.store.DeleteAdminProgress(r.Context(), characterID, questID)
	if err != nil {
		if errors.Is(err, ErrInvalid) {
			writeErr(w, http.StatusBadRequest, "invalid", err.Error())
			return
		}
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": n, "character_id": characterID, "saga_id": questID})
}

func (s *Server) level(w http.ResponseWriter, r *http.Request) {
	p := playerFrom(r)
	var in contract.LevelRequest
	if err := readJSON(r, &in, 1<<20); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", "bad json")
		return
	}
	res, err := s.store.SetLevel(r.Context(), p.ID, in)
	if err != nil {
		if errors.Is(err, ErrForbidden) {
			writeJSON(w, http.StatusForbidden, res)
			return
		}
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) createPlayer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BattleTag   string `json:"battletag"`
		DisplayName string `json:"display_name"`
	}
	if err := readJSON(r, &body, 1<<20); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", "bad json")
		return
	}
	out, err := s.store.CreatePlayer(r.Context(), body.BattleTag, body.DisplayName)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) listPlayers(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListPlayers(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	if out == nil {
		out = []contract.Player{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"players": out})
}

func (s *Server) createCharacter(w http.ResponseWriter, r *http.Request) {
	var body characterBody
	if err := readJSON(r, &body, 1<<20); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", "bad json")
		return
	}
	level := 0
	if body.Level != nil {
		level = *body.Level
	}
	backstory := ""
	if body.Backstory != nil {
		backstory = *body.Backstory
	}
	out, err := s.store.CreateCharacter(r.Context(), CharacterInput{
		PlayerID: body.PlayerID, BattleTag: body.BattleTag, Name: body.Name, Realm: body.Realm,
		Race: body.Race, Class: body.Class, Level: level, Backstory: backstory,
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) listCharacters(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListCharacters(r.Context(), r.URL.Query().Get("battletag"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if out == nil {
		out = []contract.Character{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"characters": out})
}

func (s *Server) getCharacter(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.GetCharacter(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) patchCharacter(w http.ResponseWriter, r *http.Request) {
	var body characterBody
	if err := readJSON(r, &body, 1<<20); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", "bad json")
		return
	}
	out, err := s.store.UpdateCharacter(r.Context(), r.PathValue("id"), CharacterPatch{
		Name: body.NamePtr, Realm: body.RealmPtr, Race: body.RacePtr, Class: body.ClassPtr,
		Level: body.Level, Backstory: body.Backstory,
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deleteCharacter(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteCharacter(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createQuest(w http.ResponseWriter, r *http.Request) {
	body, image, contentType, err := readQuestCreate(w, r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	cover, coverType := image, contentType
	if len(cover) == 0 {
		cover, coverType, err = decodeCover(body.ImageBase64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid", err.Error())
			return
		}
	} else {
		cover, coverType, err = screenshot.NormalizeUpload(cover)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid", err.Error())
			return
		}
	}
	in, err := fillQuestInput(body)
	if err != nil {
		s.fail(w, err)
		return
	}
	out, err := s.store.CreateQuest(r.Context(), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	if len(cover) > 0 {
		if err := s.store.SetQuestCover(r.Context(), out.ID, coverType, cover); err != nil {
			slog.Error("saga cover", "err", err)
		} else {
			out.HasImage = true
		}
	}
	s.announceCreated(r.Context(), out, body.Announce)
	writeJSON(w, http.StatusCreated, s.withSignedCoverURL(withSagaImageURL(out)))
}

func (s *Server) listQuests(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListQuests(r.Context(), r.URL.Query().Get("character_id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	for i := range out {
		out[i].Quest = s.withSignedCoverURL(out[i].Quest)
	}
	writeJSON(w, http.StatusOK, map[string]any{"sagas": out})
}

func (s *Server) getQuest(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.GetQuest(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	out.Quest = withSagaImageURL(out.Quest)
	out.Quest = s.withSignedCoverURL(out.Quest)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) putAdminQuestImage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	image, contentType, err := readSagaImage(w, r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	image, contentType, err = screenshot.NormalizeUpload(image)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	out, err := s.store.AdminSetQuestImage(r.Context(), id, contentType, image)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.withSignedCoverURL(withSagaImageURL(out)))
}

func (s *Server) deleteAdminQuestImage(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.AdminClearQuestImage(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.withSignedCoverURL(withSagaImageURL(out)))
}

func (s *Server) patchQuest(w http.ResponseWriter, r *http.Request) {
	body, image, contentType, err := readQuestPatch(w, r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	id := r.PathValue("id")
	patch, err := fillQuestPatch(body)
	if err != nil {
		s.fail(w, err)
		return
	}
	if body.EditorEmail != nil || body.UpdatedBy != nil || (body.CreatedBy != nil && strings.TrimSpace(*body.CreatedBy) != "") {
		writeErr(w, http.StatusBadRequest, "invalid", "created_by is not accepted")
		return
	}
	patch.CreatedBy = nil
	hasFields := questPatchHasFields(patch)
	if !hasFields && len(image) == 0 {
		writeErr(w, http.StatusBadRequest, "invalid", "empty patch")
		return
	}
	detail, gerr := s.store.GetQuest(r.Context(), id)
	var out contract.Quest
	if hasFields {
		out, err = s.store.UpdateQuest(r.Context(), id, patch)
		if err != nil {
			s.fail(w, err)
			return
		}
	} else if gerr != nil {
		s.fail(w, gerr)
		return
	} else {
		out = detail.Quest
	}
	out, err = s.applyAdminQuestImage(r.Context(), out, image, contentType)
	if err != nil {
		s.fail(w, err)
		return
	}
	if hasFields && gerr == nil {
		actor := ""
		if body.UpdatedBy != nil {
			actor = strings.TrimSpace(*body.UpdatedBy)
		}
		if actor == "" && body.EditorEmail != nil {
			actor = strings.TrimSpace(*body.EditorEmail)
		}
		s.announceEdited(r.Context(), detail.Quest, out, body.Announce, actor, patch.Changes)
	}
	writeJSON(w, http.StatusOK, s.withSignedCoverURL(withSagaImageURL(out)))
}

func (s *Server) listLog(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	out, err := s.store.ListLog(r.Context(), r.URL.Query().Get("battletag"), r.URL.Query().Get("character_id"), limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}

func (s *Server) createLog(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BattleTag   string `json:"battletag"`
		PlayerID    string `json:"player_id"`
		CharacterID string `json:"character_id"`
		QuestID     string `json:"saga_id"`
		Type        string `json:"type"`
		Title       string `json:"title"`
		Body        string `json:"body"`
	}
	if err := readJSON(r, &body, 1<<20); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", "bad json")
		return
	}
	playerID := body.PlayerID
	if playerID == "" && body.BattleTag != "" {
		id, err := s.store.resolvePlayerID(r.Context(), s.store.pool, "", body.BattleTag)
		if err != nil {
			s.fail(w, err)
			return
		}
		playerID = id
	}
	out, err := s.store.AddLog(r.Context(), playerID, body.CharacterID, body.QuestID, body.Type, body.Title, body.Body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, ErrConflict):
		writeErr(w, http.StatusConflict, "conflict", "already exists")
	case errors.Is(err, ErrInvalid):
		writeErr(w, http.StatusBadRequest, "invalid", err.Error())
	case errors.Is(err, ErrNotSagaAuthor):
		writeErr(w, http.StatusForbidden, "forbidden", "only the saga author can edit this saga")
	case errors.Is(err, ErrForbidden):
		writeErr(w, http.StatusForbidden, "forbidden", "forbidden")
	case errors.Is(err, ErrLocked):
		writeErr(w, http.StatusConflict, "locked", err.Error())
	case errors.Is(err, ErrUnscopedSagas):
		writeErr(w, http.StatusServiceUnavailable, "bootstrap_required", err.Error())
	default:
		slog.Error("request failed", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal", "internal error")
	}
}

type characterBody struct {
	PlayerID  string  `json:"player_id"`
	BattleTag string  `json:"battletag"`
	Name      string  `json:"name"`
	Realm     string  `json:"realm"`
	Race      string  `json:"race"`
	Class     string  `json:"class"`
	Level     *int    `json:"level"`
	Backstory *string `json:"backstory"`
	NamePtr   *string `json:"-"`
	RealmPtr  *string `json:"-"`
	RacePtr   *string `json:"-"`
	ClassPtr  *string `json:"-"`
}

func (b *characterBody) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	assign := func(key string, dst any) error {
		if v, ok := raw[key]; ok {
			return json.Unmarshal(v, dst)
		}
		return nil
	}
	_ = assign("player_id", &b.PlayerID)
	_ = assign("battletag", &b.BattleTag)
	if _, ok := raw["name"]; ok {
		var v string
		if err := json.Unmarshal(raw["name"], &v); err != nil {
			return err
		}
		b.Name = v
		b.NamePtr = &v
	}
	if _, ok := raw["realm"]; ok {
		var v string
		if err := json.Unmarshal(raw["realm"], &v); err != nil {
			return err
		}
		b.Realm = v
		b.RealmPtr = &v
	}
	if _, ok := raw["race"]; ok {
		var v string
		if err := json.Unmarshal(raw["race"], &v); err != nil {
			return err
		}
		b.Race = v
		b.RacePtr = &v
	}
	if _, ok := raw["class"]; ok {
		var v string
		if err := json.Unmarshal(raw["class"], &v); err != nil {
			return err
		}
		b.Class = v
		b.ClassPtr = &v
	}
	if _, ok := raw["level"]; ok {
		var v int
		if err := json.Unmarshal(raw["level"], &v); err != nil {
			return err
		}
		b.Level = &v
	}
	if _, ok := raw["backstory"]; ok {
		var v string
		if err := json.Unmarshal(raw["backstory"], &v); err != nil {
			return err
		}
		b.Backstory = &v
	}
	return nil
}

type questBody struct {
	Title                string               `json:"title"`
	Description          string               `json:"description"`
	Objectives           []contract.Objective `json:"objectives"`
	ObjectivesOrdered    *bool                `json:"objectives_ordered"`
	When                 *contract.TimeWindow `json:"when"`
	Group                json.RawMessage      `json:"group"`
	MinLevel             *int                 `json:"min_level"`
	PartyQuest           bool                 `json:"party"`
	Active               *bool                `json:"active"`
	CreatedBy            string               `json:"created_by"`
	AssignedCharacterIDs []string             `json:"assigned_character_ids"`
	Requires             json.RawMessage      `json:"requires"`
	Chain                json.RawMessage      `json:"chain"`
	// Announce skips the webhook when false. Omitted announces an active saga.
	Announce *bool `json:"announce"`
	// ImageBase64 is an optional cover. The bytes are stored and attached to the announcement.
	ImageBase64 string `json:"image_base64"`
}

type questPatchBody struct {
	Title             *string               `json:"title"`
	Description       *string               `json:"description"`
	Objectives        *[]contract.Objective `json:"objectives"`
	ObjectivesOrdered *bool                 `json:"objectives_ordered"`
	When              json.RawMessage       `json:"when"`
	Group             json.RawMessage       `json:"group"`
	Changes           []string              `json:"changes"`
	MinLevel          json.RawMessage       `json:"min_level"`
	PartyQuest        *bool                 `json:"party"`
	Active            *bool                 `json:"active"`
	CreatedBy         *string               `json:"created_by"`
	// UpdatedBy is who edited the saga (webhook actor / BattleTag ownership). Admin optional.
	UpdatedBy *string `json:"updated_by"`
	// EditorEmail is rejected. Saga authorship is the signed-in player.
	EditorEmail          *string         `json:"editor_email"`
	AssignedCharacterIDs *[]string       `json:"assigned_character_ids"`
	Requires             json.RawMessage `json:"requires"`
	Chain                json.RawMessage `json:"chain"`
	// Announce skips the webhook when false. Omitted posts a version note or a retirement.
	Announce *bool `json:"announce"`
}

func (s *Server) applyAdminQuestImage(ctx context.Context, q contract.Quest, image []byte, contentType string) (contract.Quest, error) {
	if len(image) == 0 {
		return q, nil
	}
	image, contentType, err := screenshot.NormalizeUpload(image)
	if err != nil {
		return contract.Quest{}, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	return s.store.AdminSetQuestImage(ctx, q.ID, contentType, image)
}

func questPatchHasFields(p QuestPatch) bool {
	return p.Title != nil || p.Description != nil || p.Objectives != nil || p.ObjectivesOrdered != nil ||
		p.MinLevelSet || p.PartyQuest != nil || p.Active != nil || p.CreatedBy != nil ||
		p.AssignedCharacterIDs != nil || p.RequiresSet || p.ChainSet || len(p.Changes) > 0
}

// readQuestCreate accepts JSON or multipart. Multipart may include optional file field "image".
func readQuestCreate(w http.ResponseWriter, r *http.Request) (questBody, []byte, string, error) {
	var body questBody
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/") {
		r.Body = http.MaxBytesReader(w, r.Body, 12<<20)
		if err := r.ParseMultipartForm(12 << 20); err != nil {
			return body, nil, "", err
		}
		body.Title = r.FormValue("title")
		body.Description = r.FormValue("description")
		body.CreatedBy = r.FormValue("created_by")
		body.PartyQuest = formBool(r.FormValue("party"))
		if v := r.FormValue("min_level"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return body, nil, "", errors.New("min_level")
			}
			body.MinLevel = &n
		}
		if v := r.FormValue("active"); v != "" {
			b := formBool(v)
			body.Active = &b
		}
		if v := r.FormValue("objectives_ordered"); v != "" {
			b := formBool(v)
			body.ObjectivesOrdered = &b
		}
		if v := r.FormValue("objectives"); v != "" {
			if err := json.Unmarshal([]byte(v), &body.Objectives); err != nil {
				return body, nil, "", errors.New("objectives must be JSON array")
			}
		}
		if v := r.FormValue("assigned_character_ids"); v != "" {
			if err := json.Unmarshal([]byte(v), &body.AssignedCharacterIDs); err != nil {
				return body, nil, "", errors.New("assigned_character_ids must be JSON array")
			}
		}
		if v := r.FormValue("requires"); v != "" {
			body.Requires = json.RawMessage(v)
		}
		if v := r.FormValue("chain"); v != "" {
			body.Chain = json.RawMessage(v)
		}
		if v, ok := r.MultipartForm.Value["announce"]; ok {
			b := formBool(v[0])
			body.Announce = &b
		}
		img, imgCT, err := optionalFormImage(r)
		return body, img, imgCT, err
	}
	if err := readJSON(r, &body, 12<<20); err != nil {
		return body, nil, "", errors.New("bad json")
	}
	return body, nil, "", nil
}

func readQuestPatch(w http.ResponseWriter, r *http.Request) (questPatchBody, []byte, string, error) {
	var body questPatchBody
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/") {
		r.Body = http.MaxBytesReader(w, r.Body, 12<<20)
		if err := r.ParseMultipartForm(12 << 20); err != nil {
			return body, nil, "", err
		}
		if v, ok := r.MultipartForm.Value["title"]; ok {
			body.Title = &v[0]
		}
		if v, ok := r.MultipartForm.Value["description"]; ok {
			body.Description = &v[0]
		}
		if v, ok := r.MultipartForm.Value["changes"]; ok && strings.TrimSpace(v[0]) != "" {
			if err := json.Unmarshal([]byte(v[0]), &body.Changes); err != nil {
				return body, nil, "", errors.New("changes")
			}
		}
		if v, ok := r.MultipartForm.Value["created_by"]; ok {
			body.CreatedBy = &v[0]
		}
		if v, ok := r.MultipartForm.Value["updated_by"]; ok {
			body.UpdatedBy = &v[0]
		}
		if v, ok := r.MultipartForm.Value["editor_email"]; ok {
			body.EditorEmail = &v[0]
		}
		if v, ok := r.MultipartForm.Value["party"]; ok {
			b := formBool(v[0])
			body.PartyQuest = &b
		}
		if v, ok := r.MultipartForm.Value["active"]; ok {
			b := formBool(v[0])
			body.Active = &b
		}
		if v, ok := r.MultipartForm.Value["objectives_ordered"]; ok {
			b := formBool(v[0])
			body.ObjectivesOrdered = &b
		}
		if v, ok := r.MultipartForm.Value["min_level"]; ok {
			body.MinLevel = json.RawMessage(v[0])
		}
		if v, ok := r.MultipartForm.Value["requires"]; ok && v[0] != "" {
			body.Requires = json.RawMessage(v[0])
		}
		if v, ok := r.MultipartForm.Value["chain"]; ok && v[0] != "" {
			body.Chain = json.RawMessage(v[0])
		}
		if v, ok := r.MultipartForm.Value["objectives"]; ok && v[0] != "" {
			var objs []contract.Objective
			if err := json.Unmarshal([]byte(v[0]), &objs); err != nil {
				return body, nil, "", errors.New("objectives must be JSON array")
			}
			body.Objectives = &objs
		}
		if v, ok := r.MultipartForm.Value["assigned_character_ids"]; ok && v[0] != "" {
			var ids []string
			if err := json.Unmarshal([]byte(v[0]), &ids); err != nil {
				return body, nil, "", errors.New("assigned_character_ids must be JSON array")
			}
			body.AssignedCharacterIDs = &ids
		}
		img, imgCT, err := optionalFormImage(r)
		return body, img, imgCT, err
	}
	if err := readJSON(r, &body, 1<<20); err != nil {
		return body, nil, "", errors.New("bad json")
	}
	return body, nil, "", nil
}

func optionalFormImage(r *http.Request) ([]byte, string, error) {
	file, hdr, err := r.FormFile("image")
	if err != nil {
		if errors.Is(err, http.ErrMissingFile) {
			return nil, "", nil
		}
		return nil, "", err
	}
	defer file.Close()
	b, err := io.ReadAll(io.LimitReader(file, 8<<20+1))
	if err != nil {
		return nil, "", err
	}
	if len(b) > 8<<20 {
		return nil, "", errors.New("image larger than 8 MiB")
	}
	contentType := hdr.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return b, contentType, nil
}

func formBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func applyRulePatch(patch *QuestPatch, whenRaw, groupRaw json.RawMessage) error {
	if len(whenRaw) > 0 {
		patch.WhenSet = true
		if string(whenRaw) != "null" {
			var w contract.TimeWindow
			if err := json.Unmarshal(whenRaw, &w); err != nil {
				return err
			}
			patch.When = &w
		}
	}
	if len(groupRaw) > 0 {
		patch.GroupSet = true
		if string(groupRaw) != "null" {
			var g contract.GroupRule
			if err := json.Unmarshal(groupRaw, &g); err != nil {
				return err
			}
			patch.Group = &g
		}
	}
	return nil
}

func readCompletion(w http.ResponseWriter, r *http.Request) (contract.CompletionRequest, []byte, string, error) {
	var in contract.CompletionRequest
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/") {
		r.Body = http.MaxBytesReader(w, r.Body, 12<<20)
		if err := r.ParseMultipartForm(12 << 20); err != nil {
			return in, nil, "", err
		}
		in.CharacterID = r.FormValue("character_id")
		in.QuestID = r.FormValue("saga_id")
		in.QuestVersion, _ = strconv.Atoi(r.FormValue("saga_version"))
		in.BattleTag = r.FormValue("battletag")
		in.Level, _ = strconv.Atoi(r.FormValue("level"))
		in.Race = r.FormValue("race")
		in.Class = r.FormValue("class")
		in.CompletedAt = r.FormValue("completed_at")
		in.StartedAt = r.FormValue("started_at")
		in.Instance = r.FormValue("instance")
		if raw := strings.TrimSpace(r.FormValue("group_members")); raw != "" {
			if err := json.Unmarshal([]byte(raw), &in.GroupMembers); err != nil {
				return in, nil, "", err
			}
		}
		file, hdr, err := r.FormFile("screenshot")
		if err == nil {
			defer file.Close()
			b, err := io.ReadAll(io.LimitReader(file, 8<<20+1))
			if err != nil {
				return in, nil, "", err
			}
			return in, b, hdr.Header.Get("Content-Type"), nil
		}
		return in, nil, "", nil
	}
	if err := readJSON(r, &in, 12<<20); err != nil {
		return in, nil, "", err
	}
	if in.ScreenshotBase64 != "" {
		b, err := base64.StdEncoding.DecodeString(in.ScreenshotBase64)
		if err != nil {
			return in, nil, "", err
		}
		return in, b, "application/octet-stream", nil
	}
	return in, nil, "", nil
}

func readJSON(r *http.Request, dst any, max int64) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, max))
	return dec.Decode(dst)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, contract.ErrorBody{Error: code, Message: msg})
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const p = "Bearer "
	if len(h) < len(p) || !strings.EqualFold(h[:len(p)], p[:len(p)]) {
		return ""
	}
	return strings.TrimSpace(h[len(p):])
}
