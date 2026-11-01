package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/sagalore/sagalore/internal/contract"
)

// ErrLocked is a new completion of a saga whose prerequisites are not done.
var ErrLocked = errors.New("locked")

type lockError struct{ message string }

func (e lockError) Error() string { return e.message }
func (e lockError) Unwrap() error { return ErrLocked }

func fillQuestInput(body questBody) (QuestInput, error) {
	slug, rule, err := parseSagaGroup(body.Group)
	if err != nil {
		return QuestInput{}, fmt.Errorf("%w: group", ErrInvalid)
	}
	in := QuestInput{
		Title: body.Title, Description: body.Description, Objectives: body.Objectives,
		ObjectivesOrdered: body.ObjectivesOrdered, When: body.When, Group: rule, GroupSlug: slug,
		MinLevel:   body.MinLevel,
		PartyQuest: body.PartyQuest, Active: body.Active, CreatedBy: body.CreatedBy,
		AssignedCharacterIDs: body.AssignedCharacterIDs, Requires: []string{},
	}
	ids, set, err := contract.ParseRequires(body.Requires)
	if err != nil {
		return QuestInput{}, fmt.Errorf("%w: requires", ErrInvalid)
	}
	if set {
		in.Requires = ids
	}
	chain, chainSet, err := contract.ParseChain(body.Chain)
	if err != nil {
		return QuestInput{}, fmt.Errorf("%w: chain", ErrInvalid)
	}
	if chainSet {
		in.Chain = chain
	}
	return in, nil
}

// parseSagaGroup accepts a tenant slug string or the party-size object.
// An object may include "slug" when the player is in more than one group.
func parseSagaGroup(raw json.RawMessage) (string, *contract.GroupRule, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil, nil
	}
	if raw[0] == '"' {
		var slug string
		if err := json.Unmarshal(raw, &slug); err != nil {
			return "", nil, err
		}
		return strings.TrimSpace(slug), nil, nil
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return "", nil, err
	}
	var slug string
	if v, ok := probe["slug"]; ok {
		_ = json.Unmarshal(v, &slug)
		delete(probe, "slug")
	}
	encoded, err := json.Marshal(probe)
	if err != nil {
		return "", nil, err
	}
	var rule contract.GroupRule
	if err := json.Unmarshal(encoded, &rule); err != nil {
		return "", nil, err
	}
	if rule.Size == 0 && rule.MinLevel == nil && strings.TrimSpace(rule.Instance) == "" {
		return strings.TrimSpace(slug), nil, nil
	}
	return strings.TrimSpace(slug), &rule, nil
}

func fillQuestPatch(body questPatchBody) (QuestPatch, error) {
	patch := QuestPatch{
		Title: body.Title, Description: body.Description, Objectives: body.Objectives,
		ObjectivesOrdered: body.ObjectivesOrdered, Changes: body.Changes,
		PartyQuest: body.PartyQuest, Active: body.Active, CreatedBy: body.CreatedBy,
		AssignedCharacterIDs: body.AssignedCharacterIDs,
	}
	if err := applyRulePatch(&patch, body.When, body.Group); err != nil {
		return QuestPatch{}, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	if len(bytes.TrimSpace(body.MinLevel)) > 0 {
		patch.MinLevelSet = true
		if string(bytes.TrimSpace(body.MinLevel)) != "null" {
			var n int
			if err := json.Unmarshal(body.MinLevel, &n); err != nil {
				return QuestPatch{}, fmt.Errorf("%w: min_level", ErrInvalid)
			}
			if n != 0 {
				patch.MinLevel = &n
			}
		}
	}
	ids, set, err := contract.ParseRequires(body.Requires)
	if err != nil {
		return QuestPatch{}, fmt.Errorf("%w: requires", ErrInvalid)
	}
	if set {
		patch.RequiresSet = true
		if ids == nil {
			ids = []string{}
		}
		patch.Requires = ids
	}
	chain, chainSet, err := contract.ParseChain(body.Chain)
	if err != nil {
		return QuestPatch{}, fmt.Errorf("%w: chain", ErrInvalid)
	}
	if chainSet {
		patch.ChainSet = true
		patch.Chain = chain
	}
	return patch, nil
}

func prepareRequires(ids []string) ([]string, error) {
	out, err := contract.NormalizeRequires(ids)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	return out, nil
}

func prepareChain(c *contract.Chain) (*contract.Chain, error) {
	out, err := contract.NormalizeChain(c)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	return out, nil
}

func rejectSelfRequire(self string, ids []string) error {
	self = strings.ToLower(strings.TrimSpace(self))
	if self == "" {
		return nil
	}
	for _, id := range ids {
		if id == self {
			return fmt.Errorf("%w: saga cannot require itself", ErrInvalid)
		}
	}
	return nil
}

func ensureQuests(ctx context.Context, q querier, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	var n int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM quests WHERE id::text = ANY($1::text[])`, ids).Scan(&n); err != nil {
		return err
	}
	if n != len(ids) {
		return fmt.Errorf("%w: unknown prerequisite", ErrInvalid)
	}
	return nil
}

func requiresJSON(ids []string) (string, error) {
	if ids == nil {
		ids = []string{}
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func decodeRequires(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return []string{}, nil
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil, err
	}
	if ids == nil {
		return []string{}, nil
	}
	return ids, nil
}

func chainPersist(q contract.Quest) (reqJSON, chainID, chainName string, chainOrder any, err error) {
	reqJSON, err = requiresJSON(q.Requires)
	if err != nil {
		return "", "", "", nil, err
	}
	if q.Chain == nil || q.Chain.ID == "" {
		return reqJSON, "", "", nil, nil
	}
	return reqJSON, q.Chain.ID, q.Chain.Name, q.Chain.Order, nil
}

// assertSagaLinks checks the stored prerequisite graph and chain order.
// rename updates the live chain_name of every saga that shares the chain id
// without bumping their versions.
func chainOrderAvailable(ctx context.Context, q querier, self string, chain *contract.Chain) error {
	if chain == nil || chain.ID == "" {
		return nil
	}
	var other string
	err := q.QueryRow(ctx, `
		SELECT id::text FROM quests
		WHERE chain_id=$1 AND chain_order=$2 AND ($3 = '' OR id::text <> $3)
		LIMIT 1`, chain.ID, chain.Order, self).Scan(&other)
	if err == nil {
		return fmt.Errorf("%w: chain order already used", ErrInvalid)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	return nil
}

func mapChainWrite(err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" && pe.ConstraintName == "quests_chain_order_uidx" {
		return fmt.Errorf("%w: chain order already used", ErrInvalid)
	}
	return err
}

func assertSagaLinks(ctx context.Context, q querier, self string, chain *contract.Chain, rename bool) error {
	if err := assertNoRequireCycle(ctx, q); err != nil {
		return err
	}
	if err := chainOrderAvailable(ctx, q, self, chain); err != nil {
		return err
	}
	if chain == nil || chain.ID == "" || !rename {
		return nil
	}
	_, err := q.Exec(ctx, `UPDATE quests SET chain_name=$2 WHERE chain_id=$1 AND chain_name <> $2`, chain.ID, chain.Name)
	return err
}

func assertNoRequireCycle(ctx context.Context, q querier) error {
	rows, err := q.Query(ctx, `SELECT id::text, requires::text FROM quests`)
	if err != nil {
		return err
	}
	defer rows.Close()
	graph := map[string][]string{}
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return err
		}
		ids, err := decodeRequires(raw)
		if err != nil {
			return fmt.Errorf("%w: requires", ErrInvalid)
		}
		graph[id] = ids
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if contract.HasRequireCycle(graph) {
		return fmt.Errorf("%w: requires cycle", ErrInvalid)
	}
	return nil
}

func applyChainPositions(ctx context.Context, q querier, quests []contract.Quest) error {
	if len(quests) == 0 {
		return nil
	}
	rows, err := q.Query(ctx, `
		SELECT id::text, chain_id, chain_order
		FROM quests
		WHERE chain_id <> ''
		ORDER BY chain_id, chain_order, title, id::text`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type row struct {
		id, chain string
		order     int
	}
	var all []row
	for rows.Next() {
		var r row
		var order sql.NullInt32
		if err := rows.Scan(&r.id, &r.chain, &order); err != nil {
			return err
		}
		if order.Valid {
			r.order = int(order.Int32)
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	type pos struct{ index, total int }
	positions := map[string]pos{}
	for i := 0; i < len(all); {
		j := i + 1
		for j < len(all) && all[j].chain == all[i].chain {
			j++
		}
		total := j - i
		for k := i; k < j; k++ {
			positions[all[k].id] = pos{index: k - i + 1, total: total}
		}
		i = j
	}
	for i := range quests {
		if quests[i].Chain == nil {
			continue
		}
		if p, ok := positions[quests[i].ID]; ok {
			quests[i].Chain.Index = p.index
			quests[i].Chain.Total = p.total
		}
	}
	return nil
}

func loadRequires(ctx context.Context, q querier, questID string) ([]string, error) {
	var raw string
	err := q.QueryRow(ctx, `SELECT requires::text FROM quests WHERE id=$1`, questID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: unknown quest", ErrInvalid)
	}
	if err != nil {
		return nil, err
	}
	return decodeRequires(raw)
}

// completedQuestIDs is every saga this character has finished, at any version.
func completedQuestIDs(ctx context.Context, q querier, characterID string) (map[string]bool, error) {
	rows, err := q.Query(ctx, `
		SELECT quest_id::text FROM progress
		WHERE character_id=$1 AND status='completed'`, characterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	done := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		done[id] = true
	}
	return done, rows.Err()
}

func lockMessage(ctx context.Context, q querier, missing []string) (string, error) {
	if len(missing) == 0 {
		return "", nil
	}
	rows, err := q.Query(ctx, `SELECT id::text, title FROM quests WHERE id::text = ANY($1::text[])`, missing)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	titles := map[string]string{}
	for rows.Next() {
		var id, title string
		if err := rows.Scan(&id, &title); err != nil {
			return "", err
		}
		titles[id] = title
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	ordered := make([]string, len(missing))
	for i, id := range missing {
		ordered[i] = titles[id]
	}
	return contract.LockMessage(ordered), nil
}
