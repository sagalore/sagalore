package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/sagalore/sagalore/internal/contract"
	"github.com/sagalore/sagalore/internal/screenshot"
	"github.com/sagalore/sagalore/internal/webhook"
)

func (s *Server) announceCreated(ctx context.Context, q contract.Quest, flag *bool) {
	if !announceCreate(flag, q.Active) || s.webhooks == nil {
		return
	}
	url, group, ok := s.groupWebhookForQuest(ctx, q.ID)
	if !ok {
		return
	}
	s.webhooks.Enqueue(url, q.ID, webhook.Event{
		Type:       webhook.TypeSagaCreated,
		OccurredAt: time.Now().UTC(),
		Group:      group,
		Saga:       sagaRef(q),
		Actor:      actorRef(q.CreatedBy, ""),
	})
}

// announceEdited posts an update or retirement. actor is a short display name.
func (s *Server) announceEdited(ctx context.Context, before, after contract.Quest, flag *bool, actor string, changes []string) {
	if before.ID == "" || after.ID == "" || !announceUpdate(flag) || s.webhooks == nil {
		return
	}
	var typ string
	switch {
	case before.Active && !after.Active:
		typ = webhook.TypeSagaRetired
	case after.Active && after.Version > before.Version:
		typ = webhook.TypeSagaUpdated
	default:
		return
	}
	url, group, ok := s.groupWebhookForQuest(ctx, after.ID)
	if !ok {
		return
	}
	name := strings.TrimSpace(actor)
	if name == "" {
		name = displayActor("", "", after.CreatedBy)
	}
	s.webhooks.Enqueue(url, after.ID, webhook.Event{
		Type:       typ,
		OccurredAt: time.Now().UTC(),
		Group:      group,
		Saga:       sagaRef(after),
		Actor:      actorRef(after.CreatedBy, name),
		Changes:    changes,
	})
}

func (s *Server) announceCompleted(ctx context.Context, questID string, res contract.CompletionResponse, in contract.CompletionRequest, when time.Time) bool {
	if s.webhooks == nil || !res.Completed || res.AlreadyCompleted {
		return false
	}
	url, group, ok := s.groupWebhookForQuest(ctx, questID)
	if !ok {
		return false
	}
	ev := webhook.Event{
		Type:       webhook.TypeSagaCompleted,
		OccurredAt: when,
		Group:      group,
		Saga: &webhook.SagaRef{
			ID:      questID,
			Title:   res.QuestTitle,
			Version: res.QuestVersion,
			Active:  true,
		},
		Character: &webhook.CharRef{
			Name:  res.CharacterName,
			Race:  strings.TrimSpace(in.Race),
			Class: strings.TrimSpace(in.Class),
			Level: in.Level,
		},
	}
	if detail, err := s.store.GetQuest(ctx, questID); err == nil {
		ev.Saga.Description = detail.Description
		ev.Saga.Active = detail.Active
	}
	return s.webhooks.Enqueue(url, res.ProgressID, ev)
}

func (s *Server) announceInviteAccepted(ctx context.Context, inv contract.Invite, playerBattleTag string) {
	if s.webhooks == nil || inv.GroupID == "" {
		return
	}
	url, group, ok := s.groupWebhook(ctx, inv.GroupID)
	if !ok {
		return
	}
	if group.Name == "" {
		group.Name = inv.GroupName
	}
	s.webhooks.Enqueue(url, inv.ID, webhook.Event{
		Type:       webhook.TypeInviteAccepted,
		OccurredAt: time.Now().UTC(),
		Group:      group,
		Invite:     &webhook.InviteRef{ID: inv.ID},
		Character:  &webhook.CharRef{Name: inv.Name, Realm: inv.Realm},
		Actor:      actorRef(playerBattleTag, displayActor("", "", playerBattleTag)),
	})
}

func sagaRef(q contract.Quest) *webhook.SagaRef {
	return &webhook.SagaRef{
		ID:          q.ID,
		Title:       q.Title,
		Version:     q.Version,
		Description: q.Description,
		Active:      q.Active,
	}
}

func actorRef(battleTag, name string) *webhook.ActorRef {
	name = strings.TrimSpace(name)
	battleTag = strings.TrimSpace(battleTag)
	if name == "" && battleTag == "" {
		return nil
	}
	if name == "" {
		name = displayActor("", "", battleTag)
	}
	return &webhook.ActorRef{Name: name, BattleTag: battleTag}
}

func (s *Server) groupWebhookForQuest(ctx context.Context, questID string) (url string, group webhook.GroupRef, ok bool) {
	if s == nil || s.store == nil || questID == "" {
		return "", webhook.GroupRef{}, false
	}
	hook, g, err := s.store.GroupWebhookForQuest(ctx, questID)
	if err != nil || strings.TrimSpace(hook) == "" {
		return "", webhook.GroupRef{}, false
	}
	return hook, g, true
}

func (s *Server) groupWebhook(ctx context.Context, groupID string) (url string, group webhook.GroupRef, ok bool) {
	if s == nil || s.store == nil || groupID == "" {
		return "", webhook.GroupRef{}, false
	}
	hook, g, err := s.store.GroupWebhook(ctx, groupID)
	if err != nil || strings.TrimSpace(hook) == "" {
		return "", webhook.GroupRef{}, false
	}
	return hook, g, true
}

// displayActor picks a short name. Prefer a real display name, then BattleTag name.
func displayActor(displayName, email, battleTag string) string {
	name := strings.TrimSpace(displayName)
	if name != "" && !strings.Contains(name, "@") {
		return name
	}
	if tag := strings.TrimSpace(battleTag); tag != "" {
		if i := strings.IndexByte(tag, '#'); i > 0 {
			return tag[:i]
		}
		return tag
	}
	if name != "" {
		if i := strings.IndexByte(name, '@'); i > 0 {
			return name[:i]
		}
		return name
	}
	if mail := strings.TrimSpace(email); mail != "" {
		if i := strings.IndexByte(mail, '@'); i > 0 {
			return mail[:i]
		}
		return mail
	}
	return ""
}

func announceCreate(flag *bool, active bool) bool {
	if flag != nil {
		return *flag
	}
	return active
}

func announceUpdate(flag *bool) bool {
	return flag == nil || *flag
}

func significantQuestChange(before, after contract.Quest) bool {
	return questContentKey(before) != questContentKey(after)
}

func questContentKey(q contract.Quest) string {
	raw, _ := json.Marshal(struct {
		Title       string               `json:"title"`
		Description string               `json:"description"`
		Objectives  []contract.Objective `json:"objectives"`
		Ordered     bool                 `json:"ordered"`
		MinLevel    *int                 `json:"min_level"`
		Party       bool                 `json:"party"`
	}{
		Title: q.Title, Description: q.Description, Objectives: q.Objectives,
		Ordered: q.ObjectivesOrdered, MinLevel: q.MinLevel, Party: q.PartyQuest,
	})
	return string(raw)
}

func decodeCover(b64 string) ([]byte, string, error) {
	b64 = strings.TrimSpace(b64)
	if b64 == "" {
		return nil, "", nil
	}
	compact := strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', ' ', '\t':
			return -1
		default:
			return r
		}
	}, b64)
	raw, err := base64.StdEncoding.DecodeString(compact)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(compact)
	}
	if err != nil {
		return nil, "", errors.New("image_base64")
	}
	return screenshot.NormalizeUpload(raw)
}

// SetQuestCover stores a cover without bumping the saga version.
func (s *Store) SetQuestCover(ctx context.Context, questID, contentType string, image []byte) error {
	if err := validUUID(questID); err != nil {
		return err
	}
	if len(image) == 0 {
		return errors.New("empty image")
	}
	ct := strings.TrimSpace(contentType)
	if ct == "" {
		ct = "application/octet-stream"
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE quests SET image_bytes=$2, image_content_type=$3, updated_at=now() WHERE id=$1`,
		questID, image, ct)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
