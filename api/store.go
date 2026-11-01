package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sagalore/sagalore/internal/contract"
	"github.com/sagalore/sagalore/internal/syncdata"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrForbidden     = errors.New("forbidden")
	ErrNotSagaAuthor = errors.New("not saga author")
	ErrConflict      = errors.New("conflict")
	ErrInvalid       = errors.New("invalid")
	uuidPattern      = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

func newToken(prefix string) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(buf), nil
}

func hashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func validUUID(id string) error {
	if !uuidPattern.MatchString(strings.TrimSpace(id)) {
		return fmt.Errorf("%w: id", ErrInvalid)
	}
	return nil
}

func validateBattleTag(tag string) error {
	tag = strings.TrimSpace(tag)
	if tag == "" || len(tag) > 64 || strings.ContainsAny(tag, " \t") || !strings.Contains(tag, "#") {
		return fmt.Errorf("%w: battletag", ErrInvalid)
	}
	return nil
}

func validateName(name string) error {
	n := []rune(strings.TrimSpace(name))
	if len(n) < 2 || len(n) > 24 {
		return fmt.Errorf("%w: character name", ErrInvalid)
	}
	for _, r := range n {
		if r == ' ' || unicode.IsControl(r) {
			return fmt.Errorf("%w: character name", ErrInvalid)
		}
	}
	return nil
}

func validateRealm(realm string) error {
	n := []rune(strings.TrimSpace(realm))
	if len(n) < 1 || len(n) > 64 {
		return fmt.Errorf("%w: realm", ErrInvalid)
	}
	for _, r := range n {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: realm", ErrInvalid)
		}
	}
	return nil
}

func validateTitle(title string) error {
	n := []rune(strings.TrimSpace(title))
	if len(n) < 1 || len(n) > 200 {
		return fmt.Errorf("%w: title", ErrInvalid)
	}
	return nil
}

func clip(s string, max int) error {
	if len([]rune(s)) > max {
		return fmt.Errorf("%w: text too long", ErrInvalid)
	}
	return nil
}

func nullableInt(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullInt(n sql.NullInt32) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int32)
	return &v
}

func mapPG(err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return fmt.Errorf("%w", ErrConflict)
	}
	return err
}

func (s *Store) CreatePlayer(ctx context.Context, battletag, displayName string) (contract.CreatePlayerResponse, error) {
	battletag = strings.TrimSpace(battletag)
	displayName = strings.TrimSpace(displayName)
	if err := validateBattleTag(battletag); err != nil {
		return contract.CreatePlayerResponse{}, err
	}
	if displayName == "" || len([]rune(displayName)) > 80 {
		return contract.CreatePlayerResponse{}, fmt.Errorf("%w: display name", ErrInvalid)
	}
	var id string
	var created time.Time
	err := s.pool.QueryRow(ctx, `
		INSERT INTO players (battletag, display_name)
		VALUES ($1, $2)
		RETURNING id::text, created_at`, battletag, displayName).Scan(&id, &created)
	if err != nil {
		return contract.CreatePlayerResponse{}, mapPG(err)
	}
	return contract.CreatePlayerResponse{
		Player: contract.Player{ID: id, BattleTag: battletag, DisplayName: displayName, CreatedAt: contract.FormatTime(created)},
	}, nil
}

func (s *Store) PlayerByKey(ctx context.Context, key string) (contract.Player, error) {
	switch {
	case strings.HasPrefix(key, "gss_"):
		return s.playerBySession(ctx, key)
	case strings.HasPrefix(key, "gst_"):
		return s.playerByPersonalToken(ctx, key)
	default:
		return contract.Player{}, ErrForbidden
	}
}

func scanPlayer(p *contract.Player, created time.Time) {
	p.CreatedAt = contract.FormatTime(created)
}

func (s *Store) playerBySession(ctx context.Context, key string) (contract.Player, error) {
	var p contract.Player
	var created time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT p.id::text, COALESCE(p.battletag, ''), p.display_name, COALESCE(p.bnet_account_id, ''), p.created_at
		FROM sessions s
		JOIN players p ON p.id = s.player_id
		WHERE s.token_hash=$1 AND s.expires_at > now()`, hashKey(key)).Scan(&p.ID, &p.BattleTag, &p.DisplayName, &p.BnetAccountID, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return contract.Player{}, ErrForbidden
	}
	if err != nil {
		return contract.Player{}, err
	}
	scanPlayer(&p, created)
	return p, nil
}

func (s *Store) playerByPersonalToken(ctx context.Context, key string) (contract.Player, error) {
	var p contract.Player
	var created time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT p.id::text, COALESCE(p.battletag, ''), p.display_name, COALESCE(p.bnet_account_id, ''), p.created_at,
			COALESCE(t.oauth_client_id, '')
		FROM personal_tokens t
		JOIN players p ON p.id = t.player_id
		WHERE t.token_hash=$1 AND t.revoked_at IS NULL
		  AND (t.expires_at IS NULL OR t.expires_at > now())`, hashKey(key)).Scan(&p.ID, &p.BattleTag, &p.DisplayName, &p.BnetAccountID, &created, &p.OAuthClientID)
	if errors.Is(err, pgx.ErrNoRows) {
		return contract.Player{}, ErrForbidden
	}
	if err != nil {
		return contract.Player{}, err
	}
	scanPlayer(&p, created)
	return p, nil
}

func (s *Store) ListPlayers(ctx context.Context) ([]contract.Player, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text, COALESCE(battletag, ''), display_name, created_at FROM players ORDER BY display_name, battletag`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contract.Player
	for rows.Next() {
		var p contract.Player
		var created time.Time
		if err := rows.Scan(&p.ID, &p.BattleTag, &p.DisplayName, &created); err != nil {
			return nil, err
		}
		p.CreatedAt = contract.FormatTime(created)
		out = append(out, p)
	}
	return out, rows.Err()
}

type CharacterInput struct {
	PlayerID  string
	BattleTag string
	Name      string
	Realm     string
	Race      string
	Class     string
	Level     int
	Backstory string
}

func (s *Store) resolvePlayerID(ctx context.Context, q querier, playerID, battletag string) (string, error) {
	if playerID != "" {
		if err := validUUID(playerID); err != nil {
			return "", err
		}
		var one int
		err := q.QueryRow(ctx, `SELECT 1 FROM players WHERE id=$1`, playerID).Scan(&one)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("%w: player", ErrNotFound)
		}
		return playerID, err
	}
	battletag = strings.TrimSpace(battletag)
	if err := validateBattleTag(battletag); err != nil {
		return "", err
	}
	var id string
	err := q.QueryRow(ctx, `SELECT id::text FROM players WHERE battletag=$1`, battletag).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("%w: player", ErrNotFound)
	}
	return id, err
}

func (s *Store) CreateCharacter(ctx context.Context, in CharacterInput) (contract.Character, error) {
	if err := validateName(in.Name); err != nil {
		return contract.Character{}, err
	}
	if err := validateRealm(in.Realm); err != nil {
		return contract.Character{}, err
	}
	if err := clip(in.Backstory, 8000); err != nil || clip(in.Race, 40) != nil || clip(in.Class, 40) != nil {
		return contract.Character{}, fmt.Errorf("%w: character text", ErrInvalid)
	}
	if in.Level == 0 {
		in.Level = 1
	}
	if in.Level < 1 || in.Level > 1000 {
		return contract.Character{}, fmt.Errorf("%w: level", ErrInvalid)
	}
	if in.PlayerID == "" && strings.TrimSpace(in.BattleTag) != "" {
		display := strings.TrimSpace(in.BattleTag)
		if i := strings.Index(display, "#"); i > 0 {
			display = display[:i]
		}
		if _, err := s.EnsurePlayer(ctx, in.BattleTag, display); err != nil {
			return contract.Character{}, err
		}
	}
	playerID, err := s.resolvePlayerID(ctx, s.pool, in.PlayerID, in.BattleTag)
	if err != nil {
		return contract.Character{}, err
	}
	var id string
	err = s.pool.QueryRow(ctx, `
		INSERT INTO characters (player_id, name, realm, race, class, level, backstory)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING id::text`,
		playerID, strings.TrimSpace(in.Name), strings.TrimSpace(in.Realm), strings.TrimSpace(in.Race), strings.TrimSpace(in.Class), in.Level, in.Backstory,
	).Scan(&id)
	if err != nil {
		return contract.Character{}, mapPG(err)
	}
	return s.GetCharacter(ctx, id)
}

type CharacterPatch struct {
	Name      *string
	Realm     *string
	Race      *string
	Class     *string
	Level     *int
	Backstory *string
}

func (s *Store) UpdateCharacter(ctx context.Context, id string, p CharacterPatch) (contract.Character, error) {
	cur, err := s.GetCharacter(ctx, id)
	if err != nil {
		return contract.Character{}, err
	}
	if p.Name != nil {
		cur.Name = strings.TrimSpace(*p.Name)
	}
	if p.Realm != nil {
		cur.Realm = strings.TrimSpace(*p.Realm)
	}
	if p.Race != nil {
		cur.Race = strings.TrimSpace(*p.Race)
	}
	if p.Class != nil {
		cur.Class = strings.TrimSpace(*p.Class)
	}
	if p.Backstory != nil {
		cur.Backstory = *p.Backstory
	}
	if p.Level != nil {
		cur.Level = *p.Level
	}
	if err := validateName(cur.Name); err != nil {
		return contract.Character{}, err
	}
	if err := validateRealm(cur.Realm); err != nil {
		return contract.Character{}, err
	}
	if cur.Level < 1 || cur.Level > 1000 {
		return contract.Character{}, fmt.Errorf("%w: level", ErrInvalid)
	}
	if err := clip(cur.Backstory, 8000); err != nil {
		return contract.Character{}, err
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE characters
		SET name=$2, realm=$3, race=$4, class=$5, level=$6, backstory=$7, updated_at=now()
		WHERE id=$1`, id, cur.Name, cur.Realm, cur.Race, cur.Class, cur.Level, cur.Backstory)
	if err != nil {
		return contract.Character{}, mapPG(err)
	}
	if tag.RowsAffected() == 0 {
		return contract.Character{}, ErrNotFound
	}
	return s.GetCharacter(ctx, id)
}

func (s *Store) DeleteCharacter(ctx context.Context, id string) error {
	if err := validUUID(id); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM characters WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetCharacter(ctx context.Context, id string) (contract.Character, error) {
	if err := validUUID(id); err != nil {
		return contract.Character{}, err
	}
	rows, err := s.pool.Query(ctx, characterSelect+` WHERE c.id=$1`, id)
	if err != nil {
		return contract.Character{}, err
	}
	defer rows.Close()
	list, err := scanCharacters(rows)
	if err != nil {
		return contract.Character{}, err
	}
	if len(list) == 0 {
		return contract.Character{}, ErrNotFound
	}
	return list[0], nil
}

func (s *Store) ListCharacters(ctx context.Context, battletag string) ([]contract.Character, error) {
	q := characterSelect
	var args []any
	if strings.TrimSpace(battletag) != "" {
		q += ` WHERE p.battletag=$1`
		args = append(args, strings.TrimSpace(battletag))
	}
	q += ` ORDER BY c.name, c.realm`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCharacters(rows)
}

const characterSelect = `
	SELECT c.id::text, c.player_id::text, c.name, c.realm, c.level, c.race, c.class, COALESCE(p.battletag, ''), c.backstory, c.participating
	FROM characters c JOIN players p ON p.id = c.player_id`

func scanCharacters(rows pgx.Rows) ([]contract.Character, error) {
	var out []contract.Character
	for rows.Next() {
		var c contract.Character
		if err := rows.Scan(&c.ID, &c.PlayerID, &c.Name, &c.Realm, &c.Level, &c.Race, &c.Class, &c.BattleTag, &c.Backstory, &c.Participating); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

type QuestInput struct {
	Title                string
	Description          string
	Objectives           []contract.Objective
	ObjectivesOrdered    *bool
	When                 *contract.TimeWindow
	Group                *contract.GroupRule
	MinLevel             *int
	PartyQuest           bool
	Active               *bool
	CreatedBy            string
	GroupID              string
	GroupSlug            string
	AssignedCharacterIDs []string
	Requires             []string
	Chain                *contract.Chain
}

type QuestPatch struct {
	Title                *string
	Description          *string
	Objectives           *[]contract.Objective
	ObjectivesOrdered    *bool
	When                 *contract.TimeWindow
	WhenSet              bool
	Group                *contract.GroupRule
	GroupSet             bool
	Changes              []string
	MinLevel             *int
	MinLevelSet          bool
	PartyQuest           *bool
	Active               *bool
	CreatedBy            *string
	AssignedCharacterIDs *[]string
	Requires             []string
	RequiresSet          bool
	Chain                *contract.Chain
	ChainSet             bool
}

func resolveCreator(ctx context.Context, q querier, createdBy string) (tag, playerID string, err error) {
	createdBy = strings.TrimSpace(createdBy)
	if err := validateBattleTag(createdBy); err != nil {
		return "", "", fmt.Errorf("%w: created_by must be a BattleTag", ErrInvalid)
	}
	err = q.QueryRow(ctx, `
		SELECT id::text, battletag FROM players WHERE battletag=$1`, createdBy).Scan(&playerID, &tag)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", fmt.Errorf("%w: created_by player not found", ErrInvalid)
	}
	if err != nil {
		return "", "", err
	}
	return tag, playerID, nil
}

func (s *Store) EnsurePlayer(ctx context.Context, battletag, displayName string) (contract.Player, error) {
	battletag = strings.TrimSpace(battletag)
	displayName = strings.TrimSpace(displayName)
	if err := validateBattleTag(battletag); err != nil {
		return contract.Player{}, err
	}
	if displayName == "" {
		displayName = battletag
	}
	var p contract.Player
	var created time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, COALESCE(battletag, ''), display_name, created_at
		FROM players WHERE battletag=$1`, battletag).Scan(&p.ID, &p.BattleTag, &p.DisplayName, &created)
	if err == nil {
		p.CreatedAt = contract.FormatTime(created)
		return p, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return contract.Player{}, err
	}
	out, err := s.CreatePlayer(ctx, battletag, displayName)
	if err != nil {
		return contract.Player{}, err
	}
	return out.Player, nil
}

func (s *Store) CreateQuest(ctx context.Context, in QuestInput) (contract.Quest, error) {
	objectives, err := contract.NormalizeObjectives(in.Objectives, false)
	if err != nil {
		return contract.Quest{}, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	in.Objectives = objectives
	when, err := contract.NormalizeTimeWindow(in.When)
	if err != nil {
		return contract.Quest{}, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	in.When = when
	group, err := contract.NormalizeGroup(in.Group)
	if err != nil {
		return contract.Quest{}, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	in.Group = group
	if err := validateQuestText(in.Title, in.Description, in.CreatedBy); err != nil {
		return contract.Quest{}, err
	}
	if in.MinLevel != nil && (*in.MinLevel < 1 || *in.MinLevel > 1000) {
		return contract.Quest{}, fmt.Errorf("%w: min_level", ErrInvalid)
	}
	display := strings.TrimSpace(in.CreatedBy)
	if i := strings.Index(display, "#"); i > 0 {
		display = display[:i]
	}
	if _, err := s.EnsurePlayer(ctx, in.CreatedBy, display); err != nil {
		return contract.Quest{}, err
	}
	active := true
	if in.Active != nil {
		active = *in.Active
	}
	ordered := false
	if in.ObjectivesOrdered != nil {
		ordered = *in.ObjectivesOrdered
	}
	requires, err := prepareRequires(in.Requires)
	if err != nil {
		return contract.Quest{}, err
	}
	in.Requires = requires
	chain, err := prepareChain(in.Chain)
	if err != nil {
		return contract.Quest{}, err
	}
	in.Chain = chain
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return contract.Quest{}, err
	}
	defer tx.Rollback(ctx)
	creatorTag, creatorID, err := resolveCreator(ctx, tx, in.CreatedBy)
	if err != nil {
		return contract.Quest{}, err
	}
	if err := ensureCharacters(ctx, tx, in.AssignedCharacterIDs); err != nil {
		return contract.Quest{}, err
	}
	if err := ensureQuests(ctx, tx, in.Requires); err != nil {
		return contract.Quest{}, err
	}
	objJSON, err := objectivesJSON(in.Objectives)
	if err != nil {
		return contract.Quest{}, err
	}
	whenJSON, err := nullableJSON(in.When)
	if err != nil {
		return contract.Quest{}, err
	}
	groupJSON, err := nullableJSON(in.Group)
	if err != nil {
		return contract.Quest{}, err
	}
	reqJSON, chainID, chainName, chainOrder, err := chainPersist(contract.Quest{Requires: in.Requires, Chain: in.Chain})
	if err != nil {
		return contract.Quest{}, err
	}
	if err := chainOrderAvailable(ctx, tx, "", in.Chain); err != nil {
		return contract.Quest{}, err
	}
	var groupID any
	if id := strings.TrimSpace(in.GroupID); id != "" {
		groupID = id
	}
	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO quests (title, description, objectives, min_level, party, active, version, created_by, created_by_player_id, objectives_ordered, time_window, group_rule, requires, chain_id, chain_name, chain_order, group_id)
		VALUES ($1,$2,$3::jsonb,$4,$5,$6,1,$7,$8,$9,$10::jsonb,$11::jsonb,$12::jsonb,$13,$14,$15,$16)
		RETURNING id::text`,
		strings.TrimSpace(in.Title), in.Description, objJSON, nullableInt(in.MinLevel), in.PartyQuest, active, creatorTag, creatorID, ordered, whenJSON, groupJSON,
		reqJSON, chainID, chainName, chainOrder, groupID,
	).Scan(&id)
	if err != nil {
		return contract.Quest{}, mapChainWrite(err)
	}
	if err := assertSagaLinks(ctx, tx, id, in.Chain, in.Chain != nil); err != nil {
		return contract.Quest{}, err
	}
	q := contract.Quest{
		ID: id, Title: strings.TrimSpace(in.Title), Description: in.Description, Objectives: in.Objectives,
		ObjectivesOrdered: ordered, When: in.When, Group: in.Group, MinLevel: in.MinLevel, PartyQuest: in.PartyQuest, Active: active, Version: 1,
		CreatedBy: creatorTag, AssignedTo: refs(in.AssignedCharacterIDs), Requires: in.Requires, Chain: in.Chain,
	}
	if q.Requires == nil {
		q.Requires = []string{}
	}
	if err := replaceAssignments(ctx, tx, id, in.AssignedCharacterIDs); err != nil {
		return contract.Quest{}, err
	}
	if err := insertSnapshot(ctx, tx, q, nil); err != nil {
		return contract.Quest{}, err
	}
	one := []contract.Quest{q}
	if err := applyChainPositions(ctx, tx, one); err != nil {
		return contract.Quest{}, err
	}
	q = one[0]
	if err := tx.Commit(ctx); err != nil {
		return contract.Quest{}, err
	}
	return q, nil
}

func (s *Store) UpdateQuest(ctx context.Context, id string, p QuestPatch) (contract.Quest, error) {
	if err := validUUID(id); err != nil {
		return contract.Quest{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return contract.Quest{}, err
	}
	defer tx.Rollback(ctx)
	q, err := loadQuest(ctx, tx, id)
	if err != nil {
		return contract.Quest{}, err
	}
	changes, err := normalizeChanges(p.Changes)
	if err != nil {
		return contract.Quest{}, err
	}
	if p.Title != nil {
		q.Title = strings.TrimSpace(*p.Title)
	}
	if p.Description != nil {
		q.Description = *p.Description
	}
	if p.Objectives != nil {
		objectives, err := contract.NormalizeObjectives(*p.Objectives, false)
		if err != nil {
			return contract.Quest{}, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
		}
		q.Objectives = objectives
	}
	if p.ObjectivesOrdered != nil {
		q.ObjectivesOrdered = *p.ObjectivesOrdered
	}
	if p.WhenSet {
		when, err := contract.NormalizeTimeWindow(p.When)
		if err != nil {
			return contract.Quest{}, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
		}
		q.When = when
	}
	if p.GroupSet {
		group, err := contract.NormalizeGroup(p.Group)
		if err != nil {
			return contract.Quest{}, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
		}
		q.Group = group
	}
	var creatorID any
	if p.CreatedBy != nil {
		tag, pid, err := resolveCreator(ctx, tx, *p.CreatedBy)
		if err != nil {
			return contract.Quest{}, err
		}
		q.CreatedBy = tag
		creatorID = pid
	} else {
		var existing sql.NullString
		if err := tx.QueryRow(ctx, `SELECT created_by_player_id::text FROM quests WHERE id=$1`, id).Scan(&existing); err != nil {
			return contract.Quest{}, err
		}
		if existing.Valid {
			creatorID = existing.String
		}
	}
	if p.PartyQuest != nil {
		q.PartyQuest = *p.PartyQuest
	}
	if p.Active != nil {
		q.Active = *p.Active
	}
	if p.MinLevelSet {
		q.MinLevel = p.MinLevel
	}
	if p.AssignedCharacterIDs != nil {
		if err := ensureCharacters(ctx, tx, *p.AssignedCharacterIDs); err != nil {
			return contract.Quest{}, err
		}
		q.AssignedTo = refs(*p.AssignedCharacterIDs)
	}
	if p.RequiresSet {
		reqs, err := prepareRequires(p.Requires)
		if err != nil {
			return contract.Quest{}, err
		}
		if err := rejectSelfRequire(id, reqs); err != nil {
			return contract.Quest{}, err
		}
		q.Requires = reqs
	}
	renameChain := false
	if p.ChainSet {
		chain, err := prepareChain(p.Chain)
		if err != nil {
			return contract.Quest{}, err
		}
		q.Chain = chain
		renameChain = chain != nil
	}
	if err := ensureQuests(ctx, tx, q.Requires); err != nil {
		return contract.Quest{}, err
	}
	if err := validateQuestText(q.Title, q.Description, q.CreatedBy); err != nil {
		return contract.Quest{}, err
	}
	if q.MinLevel != nil && (*q.MinLevel < 1 || *q.MinLevel > 1000) {
		return contract.Quest{}, fmt.Errorf("%w: min_level", ErrInvalid)
	}
	q.Version++
	objJSON, err := objectivesJSON(q.Objectives)
	if err != nil {
		return contract.Quest{}, err
	}
	whenJSON, err := nullableJSON(q.When)
	if err != nil {
		return contract.Quest{}, err
	}
	groupJSON, err := nullableJSON(q.Group)
	if err != nil {
		return contract.Quest{}, err
	}
	reqJSON, chainID, chainName, chainOrder, err := chainPersist(q)
	if err != nil {
		return contract.Quest{}, err
	}
	if err := chainOrderAvailable(ctx, tx, id, q.Chain); err != nil {
		return contract.Quest{}, err
	}
	_, err = tx.Exec(ctx, `
		UPDATE quests
		SET title=$2, description=$3, objectives=$4::jsonb, min_level=$5, party=$6, active=$7, version=$8, created_by=$9, created_by_player_id=$10, objectives_ordered=$11, time_window=$12::jsonb, group_rule=$13::jsonb, requires=$14::jsonb, chain_id=$15, chain_name=$16, chain_order=$17, updated_at=now()
		WHERE id=$1`,
		id, q.Title, q.Description, objJSON, nullableInt(q.MinLevel), q.PartyQuest, q.Active, q.Version, q.CreatedBy, creatorID, q.ObjectivesOrdered, whenJSON, groupJSON,
		reqJSON, chainID, chainName, chainOrder)
	if err != nil {
		return contract.Quest{}, mapChainWrite(err)
	}
	if err := assertSagaLinks(ctx, tx, id, q.Chain, renameChain); err != nil {
		return contract.Quest{}, err
	}
	if p.AssignedCharacterIDs != nil {
		if err := replaceAssignments(ctx, tx, id, *p.AssignedCharacterIDs); err != nil {
			return contract.Quest{}, err
		}
	}
	if err := insertSnapshot(ctx, tx, q, changes); err != nil {
		return contract.Quest{}, err
	}
	one := []contract.Quest{q}
	if err := applyChainPositions(ctx, tx, one); err != nil {
		return contract.Quest{}, err
	}
	q = one[0]
	if err := tx.Commit(ctx); err != nil {
		return contract.Quest{}, err
	}
	return q, nil
}

func objectivesJSON(list []contract.Objective) (string, error) {
	if list == nil {
		list = []contract.Objective{}
	}
	raw, err := json.Marshal(list)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func nullableJSON(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return string(raw), nil
}

func validateQuestText(title, description, createdBy string) error {
	if err := validateTitle(title); err != nil {
		return err
	}
	if err := clip(description, 8000); err != nil {
		return err
	}
	if err := clip(createdBy, 200); err != nil {
		return err
	}
	return nil
}

func normalizeChanges(in []string) ([]string, error) {
	var out []string
	for _, line := range in {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if err := clip(line, 200); err != nil {
			return nil, fmt.Errorf("%w: changes", ErrInvalid)
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: changes required", ErrInvalid)
	}
	if len(out) > 20 {
		return nil, fmt.Errorf("%w: too many changes", ErrInvalid)
	}
	return out, nil
}

func refs(ids []string) []contract.IDRef {
	out := make([]contract.IDRef, 0, len(ids))
	for _, id := range ids {
		out = append(out, contract.IDRef{ID: id})
	}
	return out
}

func ensureCharacters(ctx context.Context, q querier, ids []string) error {
	for _, id := range ids {
		if err := validUUID(id); err != nil {
			return err
		}
	}
	if len(ids) == 0 {
		return nil
	}
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM characters WHERE id::text = ANY($1::text[])`, ids).Scan(&n)
	if err != nil {
		return err
	}
	if n != len(unique(ids)) {
		return fmt.Errorf("%w: unknown character", ErrInvalid)
	}
	return nil
}

func unique(ids []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func replaceAssignments(ctx context.Context, q querier, questID string, ids []string) error {
	if _, err := q.Exec(ctx, `DELETE FROM quest_assignments WHERE quest_id=$1`, questID); err != nil {
		return err
	}
	for _, id := range unique(ids) {
		if _, err := q.Exec(ctx, `INSERT INTO quest_assignments (quest_id, character_id) VALUES ($1,$2)`, questID, id); err != nil {
			return err
		}
	}
	return nil
}

func insertSnapshot(ctx context.Context, q querier, quest contract.Quest, changes []string) error {
	ids := make([]string, 0, len(quest.AssignedTo))
	for _, a := range quest.AssignedTo {
		ids = append(ids, a.ID)
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	objJSON, err := objectivesJSON(quest.Objectives)
	if err != nil {
		return err
	}
	whenJSON, err := nullableJSON(quest.When)
	if err != nil {
		return err
	}
	groupJSON, err := nullableJSON(quest.Group)
	if err != nil {
		return err
	}
	reqJSON, chainID, chainName, chainOrder, err := chainPersist(quest)
	if err != nil {
		return err
	}
	changeJSON, err := json.Marshal(changes)
	if err != nil {
		return err
	}
	if changes == nil {
		changeJSON = []byte("[]")
	}
	_, err = q.Exec(ctx, `
		INSERT INTO quest_versions (quest_id, version, title, description, objectives, min_level, party, active, assigned_to, created_by, objectives_ordered, time_window, group_rule, requires, chain_id, chain_name, chain_order, changes)
		VALUES ($1,$2,$3,$4,$5::jsonb,$6,$7,$8,$9::jsonb,$10,$11,$12::jsonb,$13::jsonb,$14::jsonb,$15,$16,$17,$18::jsonb)`,
		quest.ID, quest.Version, quest.Title, quest.Description, objJSON,
		nullableInt(quest.MinLevel), quest.PartyQuest, quest.Active, string(raw), quest.CreatedBy, quest.ObjectivesOrdered, whenJSON, groupJSON,
		reqJSON, chainID, chainName, chainOrder, string(changeJSON))
	return err
}

func scanQuest(scan func(dest ...any) error) (contract.Quest, error) {
	var q contract.Quest
	var ml sql.NullInt32
	var obj, whenRaw, groupRaw, reqRaw, chainID, chainName string
	var chainOrder sql.NullInt32
	err := scan(&q.ID, &q.Title, &q.Description, &obj, &ml, &q.PartyQuest, &q.Active, &q.Version, &q.CreatedBy, &q.ObjectivesOrdered, &q.HasImage, &whenRaw, &groupRaw, &reqRaw, &chainID, &chainName, &chainOrder, &q.GroupID, &q.GroupSlug, &q.GroupName)
	if err != nil {
		return q, err
	}
	q.MinLevel = nullInt(ml)
	q.Objectives, err = contract.DecodeObjectives(obj)
	if err != nil {
		return q, fmt.Errorf("%w: objectives", ErrInvalid)
	}
	q.When, err = contract.DecodeTimeWindow(whenRaw)
	if err != nil {
		return q, fmt.Errorf("%w: when", ErrInvalid)
	}
	q.Group, err = contract.DecodeGroupRule(groupRaw)
	if err != nil {
		return q, fmt.Errorf("%w: group", ErrInvalid)
	}
	q.Requires, err = decodeRequires(reqRaw)
	if err != nil {
		return q, fmt.Errorf("%w: requires", ErrInvalid)
	}
	if chainID != "" && chainOrder.Valid {
		q.Chain = &contract.Chain{ID: chainID, Name: chainName, Order: int(chainOrder.Int32)}
	}
	q.AssignedTo = []contract.IDRef{}
	return q, nil
}

const questSelect = `
		SELECT q.id::text, q.title, q.description, q.objectives::text, q.min_level, q.party, q.active, q.version,
			COALESCE(NULLIF(p.battletag, ''), q.created_by),
			q.objectives_ordered,
			(q.image_bytes IS NOT NULL AND octet_length(q.image_bytes) > 0),
			COALESCE(q.time_window::text, ''),
			COALESCE(q.group_rule::text, ''),
			q.requires::text, q.chain_id, q.chain_name, q.chain_order,
			COALESCE(g.id::text, ''), COALESCE(g.slug, ''), COALESCE(g.name, '')
		FROM quests q
		LEFT JOIN players p ON p.id = q.created_by_player_id
		LEFT JOIN groups g ON g.id = q.group_id`

func loadQuest(ctx context.Context, q querier, id string) (contract.Quest, error) {
	quest, err := scanQuest(q.QueryRow(ctx, questSelect+` WHERE q.id=$1`, id).Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return contract.Quest{}, ErrNotFound
	}
	if err != nil {
		return contract.Quest{}, err
	}
	assigned, err := loadAssignments(ctx, q, []string{id})
	if err != nil {
		return contract.Quest{}, err
	}
	quest.AssignedTo = assigned[id]
	if quest.AssignedTo == nil {
		quest.AssignedTo = []contract.IDRef{}
	}
	one := []contract.Quest{quest}
	if err := applyChainPositions(ctx, q, one); err != nil {
		return contract.Quest{}, err
	}
	return one[0], nil
}

func loadAssignments(ctx context.Context, q querier, questIDs []string) (map[string][]contract.IDRef, error) {
	out := map[string][]contract.IDRef{}
	if len(questIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT quest_id::text, character_id::text FROM quest_assignments WHERE quest_id::text = ANY($1::text[]) ORDER BY character_id::text`, questIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var qid, cid string
		if err := rows.Scan(&qid, &cid); err != nil {
			return nil, err
		}
		out[qid] = append(out[qid], contract.IDRef{ID: cid})
	}
	return out, rows.Err()
}

func (s *Store) GetQuest(ctx context.Context, id string) (contract.QuestDetail, error) {
	if err := validUUID(id); err != nil {
		return contract.QuestDetail{}, err
	}
	q, err := loadQuest(ctx, s.pool, id)
	if err != nil {
		return contract.QuestDetail{}, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT version, title, description, objectives::text, min_level, party, active, assigned_to::text, created_by, created_at, COALESCE(changes::text, '[]'),
			COALESCE(time_window::text, ''), COALESCE(group_rule::text, ''),
			requires::text, chain_id, chain_name, chain_order
		FROM quest_versions WHERE quest_id=$1 ORDER BY version`, id)
	if err != nil {
		return contract.QuestDetail{}, err
	}
	defer rows.Close()
	var versions []contract.QuestVersion
	for rows.Next() {
		var v contract.QuestVersion
		var ml sql.NullInt32
		var assigned, obj, whenRaw, groupRaw, reqRaw, chainID, chainName, changesRaw string
		var chainOrder sql.NullInt32
		var created time.Time
		if err := rows.Scan(&v.Version, &v.Title, &v.Description, &obj, &ml, &v.PartyQuest, &v.Active, &assigned, &v.CreatedBy, &created, &changesRaw, &whenRaw, &groupRaw, &reqRaw, &chainID, &chainName, &chainOrder); err != nil {
			return contract.QuestDetail{}, err
		}
		v.Objectives, err = contract.DecodeObjectives(obj)
		if err != nil {
			return contract.QuestDetail{}, err
		}
		if changesRaw != "" && changesRaw != "null" {
			if err := json.Unmarshal([]byte(changesRaw), &v.Changes); err != nil {
				return contract.QuestDetail{}, err
			}
		}
		if v.Changes == nil {
			v.Changes = []string{}
		}
		v.When, err = contract.DecodeTimeWindow(whenRaw)
		if err != nil {
			return contract.QuestDetail{}, err
		}
		v.Group, err = contract.DecodeGroupRule(groupRaw)
		if err != nil {
			return contract.QuestDetail{}, err
		}
		v.Requires, err = decodeRequires(reqRaw)
		if err != nil {
			return contract.QuestDetail{}, err
		}
		if chainID != "" && chainOrder.Valid {
			v.Chain = &contract.Chain{ID: chainID, Name: chainName, Order: int(chainOrder.Int32)}
		}
		v.MinLevel = nullInt(ml)
		v.CreatedAt = contract.FormatTime(created)
		var ids []string
		_ = json.Unmarshal([]byte(assigned), &ids)
		v.AssignedTo = refs(ids)
		versions = append(versions, v)
	}
	if err := rows.Err(); err != nil {
		return contract.QuestDetail{}, err
	}
	completers, err := s.listQuestCompleters(ctx, id)
	if err != nil {
		return contract.QuestDetail{}, err
	}
	return contract.QuestDetail{
		Quest: q, Versions: versions, CompletedBy: completers,
		GoalsMarkdown: buildGoalsMarkdown(q),
		Card:          buildActivityCard(completers),
	}, nil
}

func (s *Store) listQuestCompleters(ctx context.Context, questID string) ([]contract.QuestCompleter, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.name, c.realm, COALESCE(c.class, ''), COALESCE(c.race, ''), c.level,
			COALESCE(p.battletag, ''), pr.completed_at, pr.started_at, pr.duration_sec,
			COALESCE(pr.level_at_completion, 0)
		FROM progress pr
		JOIN characters c ON c.id = pr.character_id
		JOIN players p ON p.id = c.player_id
		WHERE pr.quest_id=$1 AND pr.status='completed'
		ORDER BY pr.completed_at ASC NULLS LAST, c.name, c.realm`, questID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contract.QuestCompleter
	for rows.Next() {
		var c contract.QuestCompleter
		var completedAt time.Time
		var startedAt sql.NullTime
		var duration sql.NullInt32
		var levelAt int
		if err := rows.Scan(&c.Character, &c.Realm, &c.Class, &c.Race, &c.Level, &c.BattleTag, &completedAt, &startedAt, &duration, &levelAt); err != nil {
			return nil, err
		}
		if levelAt > 0 {
			c.Level = levelAt
		}
		c.CompletedAt = contract.FormatTime(completedAt.UTC())
		c.StartedAt = formatNullTime(startedAt)
		c.DurationSec = durationPtr(duration)
		c.When = syncdata.FormatOsloWhen(c.CompletedAt)
		out = append(out, c)
	}
	if out == nil {
		out = []contract.QuestCompleter{}
	}
	return out, rows.Err()
}

// buildGoalsMarkdown lists objectives like the addon detail pane (under the title, no section header).
func buildGoalsMarkdown(q contract.Quest) string {
	var b strings.Builder
	for i, o := range q.Objectives {
		text := strings.TrimSpace(o.Text)
		if text == "" {
			text = o.ID
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(fmt.Sprintf("%d. %s", i+1, text))
	}
	if b.Len() == 0 {
		return "(no goals)"
	}
	return b.String()
}

// buildActivityCard is a table of who finished a saga, matching the addon.
func buildActivityCard(completers []contract.QuestCompleter) *contract.QuestCard {
	card := &contract.QuestCard{
		Columns: []contract.QuestCardColumn{
			{ID: "character", Label: "Character", Fill: true, Bold: true},
			{ID: "detail", Label: "Details", Fill: true},
			{ID: "when", Label: "When"},
		},
	}
	if len(completers) == 0 {
		card.Rows = []contract.QuestCardRow{{
			ID:    "done-none",
			Group: "Activity",
			Cells: map[string]any{
				"character": "No one has completed this yet",
				"detail":    "",
				"when":      "",
			},
		}}
		return card
	}
	for i, c := range completers {
		who := c.Character
		if who != "" {
			who += " completed"
		}
		detail := ""
		if c.Class != "" && c.Level > 0 {
			detail = fmt.Sprintf("%s %d", c.Class, c.Level)
		} else if c.Class != "" {
			detail = c.Class
		}
		if c.Realm != "" {
			if detail != "" {
				detail += " · "
			}
			detail += c.Realm
		}
		if c.DurationSec != nil {
			if detail != "" {
				detail += " · "
			}
			detail += formatDuration(*c.DurationSec)
		}
		card.Rows = append(card.Rows, contract.QuestCardRow{
			ID:    fmt.Sprintf("done-%d", i+1),
			Group: "Activity",
			Cells: map[string]any{
				"character": who,
				"detail":    detail,
				"when":      c.When,
			},
		})
	}
	return card
}

func (s *Store) ListQuests(ctx context.Context, characterID string) ([]contract.QuestListItem, error) {
	var level int
	var charKnown bool
	if characterID != "" {
		if err := validUUID(characterID); err != nil {
			return nil, err
		}
		err := s.pool.QueryRow(ctx, `SELECT level FROM characters WHERE id=$1`, characterID).Scan(&level)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		charKnown = true
	}
	rows, err := s.pool.Query(ctx, questSelect+` ORDER BY q.active DESC, q.title`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var quests []contract.Quest
	var ids []string
	for rows.Next() {
		q, err := scanQuest(rows.Scan)
		if err != nil {
			return nil, err
		}
		quests = append(quests, q)
		ids = append(ids, q.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	assigned, err := loadAssignments(ctx, s.pool, ids)
	if err != nil {
		return nil, err
	}
	if err := applyChainPositions(ctx, s.pool, quests); err != nil {
		return nil, err
	}
	progress := map[string]contract.Progress{}
	if charKnown {
		prows, err := s.pool.Query(ctx, `
			SELECT quest_id::text, quest_version, status, completed_at
			FROM progress WHERE character_id=$1
			ORDER BY completed_at ASC NULLS LAST`, characterID)
		if err != nil {
			return nil, err
		}
		defer prows.Close()
		for prows.Next() {
			var p contract.Progress
			var completed sql.NullTime
			if err := prows.Scan(&p.QuestID, &p.QuestVersion, &p.Status, &completed); err != nil {
				return nil, err
			}
			if completed.Valid {
				p.CompletedAt = contract.FormatTime(completed.Time)
			}
			progress[p.QuestID] = p
		}
		if err := prows.Err(); err != nil {
			return nil, err
		}
	}
	out := make([]contract.QuestListItem, 0, len(quests))
	for _, q := range quests {
		q.AssignedTo = assigned[q.ID]
		if q.AssignedTo == nil {
			q.AssignedTo = []contract.IDRef{}
		}
		item := contract.QuestListItem{Quest: q}
		if charKnown {
			ok := contract.Eligible(level, characterID, q)
			item.Eligible = &ok
			done := map[string]bool{}
			for id, p := range progress {
				if p.Status == "completed" {
					done[id] = true
				}
			}
			locked, missing := contract.Gate(q.ID, q.Requires, done)
			unlocked := !locked
			item.Locked = &locked
			item.Unlocked = &unlocked
			item.Missing = missing
			if p, found := progress[q.ID]; found {
				status := p.Status
				item.Status = &status
				item.CompletedAt = p.CompletedAt
				item.CompletedVersion = p.QuestVersion
			}
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *Store) Sync(ctx context.Context, playerID string) (contract.SyncResponse, error) {
	var tag string
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(battletag, '') FROM players WHERE id=$1`, playerID).Scan(&tag); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return contract.SyncResponse{}, ErrNotFound
		}
		return contract.SyncResponse{}, err
	}
	if err := s.EnsurePersonalGroup(ctx, playerID); err != nil {
		return contract.SyncResponse{}, err
	}
	chars, err := scanCharactersMust(ctx, s.pool, characterSelect+` WHERE c.player_id=$1 ORDER BY c.name, c.realm`, playerID)
	if err != nil {
		return contract.SyncResponse{}, err
	}
	if chars == nil {
		chars = []contract.Character{}
	}
	invites, err := s.SyncInvites(ctx, playerID)
	if err != nil {
		return contract.SyncResponse{}, err
	}
	if invites == nil {
		invites = []contract.Invite{}
	}
	pendingByGroup := map[string]string{}
	for _, inv := range invites {
		if inv.Kind != "join" || inv.GroupID == "" {
			continue
		}
		if _, ok := pendingByGroup[inv.GroupID]; !ok {
			pendingByGroup[inv.GroupID] = inv.ID
		}
	}
	memberGroups := map[string]bool{}
	grows, err := s.pool.Query(ctx, `SELECT group_id::text FROM group_members WHERE player_id=$1`, playerID)
	if err != nil {
		return contract.SyncResponse{}, err
	}
	for grows.Next() {
		var id string
		if err := grows.Scan(&id); err != nil {
			grows.Close()
			return contract.SyncResponse{}, err
		}
		memberGroups[id] = true
	}
	if err := grows.Err(); err != nil {
		grows.Close()
		return contract.SyncResponse{}, err
	}
	grows.Close()
	rows, err := s.pool.Query(ctx, questSelect+`
		WHERE q.group_id IN (
			SELECT group_id FROM group_members WHERE player_id=$1
			UNION
			SELECT i.group_id FROM group_invites i
			JOIN players p ON p.id=$1
			WHERE i.status='pending'
			AND NOT EXISTS (
				SELECT 1 FROM group_members gm
				WHERE gm.group_id=i.group_id AND gm.player_id=p.id
			)
			AND (
				EXISTS (
					SELECT 1 FROM characters c
					WHERE c.player_id=p.id
					AND lower(c.name)=lower(i.character_name)
					AND lower(c.realm)=lower(i.realm)
				)
				OR (i.battletag IS NOT NULL AND i.battletag <> '' AND lower(i.battletag)=lower(COALESCE(p.battletag, '')))
			)
		)
		AND (q.active OR q.id IN (
			SELECT p.quest_id FROM progress p
			JOIN characters c ON c.id = p.character_id
			WHERE c.player_id=$1
		))
		ORDER BY q.min_level NULLS FIRST, q.title`, playerID)
	if err != nil {
		return contract.SyncResponse{}, err
	}
	defer rows.Close()
	var quests []contract.Quest
	var ids []string
	var memberIDs []string
	for rows.Next() {
		q, err := scanQuest(rows.Scan)
		if err != nil {
			return contract.SyncResponse{}, err
		}
		if !memberGroups[q.GroupID] {
			if inviteID := pendingByGroup[q.GroupID]; inviteID != "" {
				q.PendingInviteID = inviteID
			}
		}
		quests = append(quests, q)
		ids = append(ids, q.ID)
		if memberGroups[q.GroupID] {
			memberIDs = append(memberIDs, q.ID)
		}
	}
	if err := rows.Err(); err != nil {
		return contract.SyncResponse{}, err
	}
	if quests == nil {
		quests = []contract.Quest{}
	}
	assigned, err := loadAssignments(ctx, s.pool, ids)
	if err != nil {
		return contract.SyncResponse{}, err
	}
	for i := range quests {
		quests[i].AssignedTo = assigned[quests[i].ID]
		if quests[i].AssignedTo == nil {
			quests[i].AssignedTo = []contract.IDRef{}
		}
		if quests[i].HasImage {
			quests[i].ImageURL = "/v1/sagas/" + quests[i].ID + "/image"
		}
		// Personal starters are stamped Sagalore#0000; the log shows "Sagalore".
		if strings.EqualFold(quests[i].CreatedBy, personalCreator) {
			quests[i].CreatedBy = "Sagalore"
		}
	}
	if err := applyChainPositions(ctx, s.pool, quests); err != nil {
		return contract.SyncResponse{}, err
	}
	prows, err := s.pool.Query(ctx, `
		SELECT p.quest_id::text, p.character_id::text, p.quest_version, p.status,
			p.completed_at, p.started_at, p.duration_sec
		FROM progress p
		JOIN characters c ON c.id = p.character_id
		WHERE c.player_id=$1
		ORDER BY p.completed_at ASC NULLS LAST`, playerID)
	if err != nil {
		return contract.SyncResponse{}, err
	}
	defer prows.Close()
	var progress []contract.Progress
	for prows.Next() {
		var p contract.Progress
		var completed, started sql.NullTime
		var duration sql.NullInt32
		if err := prows.Scan(&p.QuestID, &p.CharacterID, &p.QuestVersion, &p.Status, &completed, &started, &duration); err != nil {
			return contract.SyncResponse{}, err
		}
		if completed.Valid {
			p.CompletedAt = contract.FormatTime(completed.Time)
		}
		p.StartedAt = formatNullTime(started)
		p.DurationSec = durationPtr(duration)
		progress = append(progress, p)
	}
	if progress == nil {
		progress = []contract.Progress{}
	}
	if err := prows.Err(); err != nil {
		return contract.SyncResponse{}, err
	}
	completions, err := s.sagaCompletions(ctx, memberIDs)
	if err != nil {
		return contract.SyncResponse{}, err
	}
	locks := contract.SagaLocks(chars, quests, progress)
	if locks == nil {
		locks = []contract.SagaLock{}
	}
	boards, err := s.LeaderboardsForPlayer(ctx, playerID)
	if err != nil {
		return contract.SyncResponse{}, err
	}
	groups, err := s.GroupsForPlayer(ctx, playerID)
	if err != nil {
		return contract.SyncResponse{}, err
	}
	if groups == nil {
		groups = []contract.GroupSummary{}
	}
	return contract.SyncResponse{
		BattleTag: tag, GeneratedAt: contract.FormatTime(time.Now()),
		Characters: chars, Quests: quests, Progress: progress, Completions: completions, Locks: locks,
		Groups: groups, Invites: invites, Leaderboards: boards,
	}, nil
}

func (s *Store) sagaCompletions(ctx context.Context, questIDs []string) ([]contract.QuestCompletion, error) {
	want := map[string]bool{}
	for _, id := range questIDs {
		want[id] = true
	}
	if len(want) == 0 {
		return []contract.QuestCompletion{}, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT p.quest_id::text, c.name, COALESCE(c.realm, ''), COALESCE(pl.battletag, ''),
			COALESCE(NULLIF(TRIM(pl.display_name), ''), ''),
			COALESCE(p.level_at_completion, c.level), COALESCE(c.race, ''), COALESCE(c.class, ''),
			p.completed_at, p.started_at, p.duration_sec
		FROM progress p
		JOIN characters c ON c.id = p.character_id
		JOIN players pl ON pl.id = c.player_id
		WHERE p.status = 'completed'
		AND EXISTS (
			SELECT 1 FROM group_members gm
			JOIN quests q ON q.group_id = gm.group_id
			WHERE q.id = p.quest_id AND gm.player_id = c.player_id
		)
		ORDER BY p.completed_at ASC NULLS LAST, c.name, c.realm`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []contract.QuestCompletion{}
	for rows.Next() {
		var c contract.QuestCompletion
		var completed, started sql.NullTime
		var duration sql.NullInt32
		if err := rows.Scan(&c.QuestID, &c.Character, &c.Realm, &c.BattleTag, &c.DisplayName,
			&c.Level, &c.Race, &c.Class, &completed, &started, &duration); err != nil {
			return nil, err
		}
		if !want[c.QuestID] || strings.TrimSpace(c.Character) == "" {
			continue
		}
		if completed.Valid {
			c.CompletedAt = contract.FormatTime(completed.Time)
		}
		c.StartedAt = formatNullTime(started)
		c.DurationSec = durationPtr(duration)
		out = append(out, c)
	}
	return out, rows.Err()
}

func scanCharactersMust(ctx context.Context, q querier, sql string, args ...any) ([]contract.Character, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCharacters(rows)
}

func (s *Store) Complete(ctx context.Context, playerID string, in contract.CompletionRequest, image []byte, contentType string) (contract.CompletionResponse, error) {
	if err := validUUID(in.CharacterID); err != nil {
		return contract.CompletionResponse{}, err
	}
	if err := validUUID(in.QuestID); err != nil {
		return contract.CompletionResponse{}, err
	}
	if in.QuestVersion < 1 {
		return contract.CompletionResponse{}, fmt.Errorf("%w: saga_version", ErrInvalid)
	}
	in.BattleTag = strings.TrimSpace(in.BattleTag)
	if in.BattleTag != "" {
		if err := validateBattleTag(in.BattleTag); err != nil {
			return contract.CompletionResponse{}, err
		}
	}
	if in.Level < 0 || in.Level > 1000 {
		return contract.CompletionResponse{}, fmt.Errorf("%w: level", ErrInvalid)
	}
	if len(in.GroupMembers) > 40 {
		return contract.CompletionResponse{}, fmt.Errorf("%w: group_members", ErrInvalid)
	}
	in.Instance = strings.TrimSpace(in.Instance)
	if len([]rune(in.Instance)) > 80 {
		return contract.CompletionResponse{}, fmt.Errorf("%w: instance", ErrInvalid)
	}
	for _, m := range in.GroupMembers {
		if len([]rune(strings.TrimSpace(m.Name))) > 40 || len([]rune(m.Realm)) > 40 || m.Level < 0 || m.Level > 1000 {
			return contract.CompletionResponse{}, fmt.Errorf("%w: group_members", ErrInvalid)
		}
	}
	completedAt := time.Now().UTC()
	if strings.TrimSpace(in.CompletedAt) != "" {
		t, err := time.Parse(time.RFC3339, in.CompletedAt)
		if err != nil {
			return contract.CompletionResponse{}, fmt.Errorf("%w: completed_at", ErrInvalid)
		}
		completedAt = t.UTC()
	}
	startedAt, duration, durationOut, err := completionTiming(completedAt, in.StartedAt)
	if err != nil {
		return contract.CompletionResponse{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return contract.CompletionResponse{}, err
	}
	defer tx.Rollback(ctx)

	var name, realm, raceName, className, tag string
	var storedLevel int
	err = tx.QueryRow(ctx, `
		SELECT c.name, c.realm, COALESCE(c.race, ''), COALESCE(c.class, ''), c.level, COALESCE(p.battletag, '')
		FROM characters c JOIN players p ON p.id = c.player_id
		WHERE c.id=$1 AND c.player_id=$2`, in.CharacterID, playerID).Scan(&name, &realm, &raceName, &className, &storedLevel, &tag)
	if errors.Is(err, pgx.ErrNoRows) {
		return contract.CompletionResponse{Allowed: false}, ErrForbidden
	}
	if err != nil {
		return contract.CompletionResponse{}, err
	}
	if in.BattleTag != "" && !strings.EqualFold(tag, in.BattleTag) {
		return contract.CompletionResponse{Allowed: false}, ErrForbidden
	}
	var questGroup sql.NullString
	if err := tx.QueryRow(ctx, `SELECT group_id::text FROM quests WHERE id=$1`, in.QuestID).Scan(&questGroup); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return contract.CompletionResponse{}, fmt.Errorf("%w: saga", ErrNotFound)
		}
		return contract.CompletionResponse{}, err
	}
	if !questGroup.Valid || strings.TrimSpace(questGroup.String) == "" {
		return contract.CompletionResponse{Allowed: false}, ErrForbidden
	}
	var member int
	err = tx.QueryRow(ctx, `SELECT 1 FROM group_members WHERE group_id=$1 AND player_id=$2`, questGroup.String, playerID).Scan(&member)
	if errors.Is(err, pgx.ErrNoRows) {
		return contract.CompletionResponse{Allowed: false}, ErrForbidden
	}
	if err != nil {
		return contract.CompletionResponse{}, err
	}
	// Stamp fresh race/class/level from the game onto the character row when present.
	freshRace := strings.TrimSpace(in.Race)
	freshClass := strings.TrimSpace(in.Class)
	if freshRace != "" || freshClass != "" || in.Level > 0 {
		race := raceName
		if freshRace != "" {
			race = freshRace
		}
		class := className
		if freshClass != "" {
			class = freshClass
		}
		level := storedLevel
		if in.Level > 0 {
			level = in.Level
		}
		if _, err := tx.Exec(ctx, `
			UPDATE characters SET race=$2, class=$3, level=$4, updated_at=now() WHERE id=$1`,
			in.CharacterID, race, class, level); err != nil {
			return contract.CompletionResponse{}, err
		}
		raceName, className, storedLevel = race, class, level
	}
	var snapTitle, groupRaw, objRaw string
	var snapOrdered bool
	err = tx.QueryRow(ctx, `
		SELECT title, COALESCE(group_rule::text, ''), objectives::text, objectives_ordered
		FROM quest_versions WHERE quest_id=$1 AND version=$2`, in.QuestID, in.QuestVersion).Scan(&snapTitle, &groupRaw, &objRaw, &snapOrdered)
	if errors.Is(err, pgx.ErrNoRows) {
		return contract.CompletionResponse{}, fmt.Errorf("%w: unknown quest version", ErrInvalid)
	}
	if err != nil {
		return contract.CompletionResponse{}, err
	}
	snapObjectives, err := contract.DecodeObjectives(objRaw)
	if err != nil {
		return contract.CompletionResponse{}, fmt.Errorf("%w: objectives", ErrInvalid)
	}
	rule, err := contract.DecodeGroupRule(groupRaw)
	if err != nil {
		return contract.CompletionResponse{}, fmt.Errorf("%w: group", ErrInvalid)
	}
	rosterLevel := in.Level
	if rosterLevel < 1 {
		rosterLevel = storedLevel
	}
	members := contract.EnsureMember(append([]contract.GroupMember(nil), in.GroupMembers...), name, realm, rosterLevel)
	groupOK := rule != nil && contract.GroupSatisfied(rule, members, in.Instance) == nil

	var existingID string
	var existingVersion int
	err = tx.QueryRow(ctx, `
		SELECT id::text, quest_version FROM progress
		WHERE character_id=$1 AND quest_id=$2
		ORDER BY completed_at ASC NULLS LAST
		LIMIT 1`, in.CharacterID, in.QuestID).Scan(&existingID, &existingVersion)
	if err == nil {
		if err := fillStarted(ctx, tx, existingID, startedAt, duration); err != nil {
			return contract.CompletionResponse{}, err
		}
		var storedTitle, storedGroup string
		_ = tx.QueryRow(ctx, `
			SELECT title, COALESCE(group_rule::text, '')
			FROM quest_versions WHERE quest_id=$1 AND version=$2`, in.QuestID, existingVersion).Scan(&storedTitle, &storedGroup)
		storedRule, _ := contract.DecodeGroupRule(storedGroup)
		var credits []contract.GroupCredit
		if storedRule != nil && contract.GroupSatisfied(storedRule, members, in.Instance) == nil {
			credits, err = creditGroup(ctx, tx, in.QuestID, existingVersion, storedTitle, in.CharacterID, name, completedAt, members)
			if err != nil {
				return contract.CompletionResponse{}, err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return contract.CompletionResponse{}, err
		}
		return contract.CompletionResponse{
			Allowed: true, AlreadyCompleted: true, Completed: true,
			ProgressID: existingID, CharacterName: name, QuestTitle: storedTitle, QuestVersion: existingVersion,
			GroupCredited: credits,
		}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return contract.CompletionResponse{}, err
	}
	if rule != nil && !groupOK {
		if err := contract.GroupSatisfied(rule, members, in.Instance); err != nil {
			return contract.CompletionResponse{}, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
		}
	}

	// Prerequisites use the live requires list. A completed row counts at any
	// quest_version, including one recorded before the saga was edited.
	requires, err := loadRequires(ctx, tx, in.QuestID)
	if err != nil {
		return contract.CompletionResponse{}, err
	}
	done, err := completedQuestIDs(ctx, tx, in.CharacterID)
	if err != nil {
		return contract.CompletionResponse{}, err
	}
	if locked, missing := contract.Gate(in.QuestID, requires, done); locked {
		msg, err := lockMessage(ctx, tx, missing)
		if err != nil {
			return contract.CompletionResponse{}, err
		}
		return contract.CompletionResponse{}, lockError{message: msg}
	}

	var screenshot any
	if len(image) > 0 {
		var sid string
		if err := tx.QueryRow(ctx, `INSERT INTO screenshots (content_type, bytes) VALUES ($1,$2) RETURNING id::text`, contentType, image).Scan(&sid); err != nil {
			return contract.CompletionResponse{}, err
		}
		screenshot = sid
	}
	var level any
	if in.Level > 0 {
		level = in.Level
	}
	var progressID string
	err = tx.QueryRow(ctx, `
		INSERT INTO progress (character_id, quest_id, quest_version, status, completed_at, started_at, duration_sec, level_at_completion, screenshot_id)
		SELECT $1,$2,$3,'completed',$4,$5,$6,$7,$8
		WHERE NOT EXISTS (
			SELECT 1 FROM progress WHERE character_id=$1 AND quest_id=$2
		)
		RETURNING id::text`,
		in.CharacterID, in.QuestID, in.QuestVersion, completedAt, startedAt, duration, level, screenshot).Scan(&progressID)
	if errors.Is(err, pgx.ErrNoRows) {
		if screenshot != nil {
			_, _ = tx.Exec(ctx, `DELETE FROM screenshots WHERE id=$1`, screenshot)
		}
		_ = tx.QueryRow(ctx, `
			SELECT id::text, quest_version FROM progress
			WHERE character_id=$1 AND quest_id=$2
			ORDER BY completed_at ASC NULLS LAST
			LIMIT 1`, in.CharacterID, in.QuestID).Scan(&existingID, &existingVersion)
		if err := fillStarted(ctx, tx, existingID, startedAt, duration); err != nil {
			return contract.CompletionResponse{}, err
		}
		var credits []contract.GroupCredit
		if groupOK {
			credits, err = creditGroup(ctx, tx, in.QuestID, existingVersion, snapTitle, in.CharacterID, name, completedAt, members)
			if err != nil {
				return contract.CompletionResponse{}, err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return contract.CompletionResponse{}, err
		}
		return contract.CompletionResponse{
			Allowed: true, AlreadyCompleted: true, Completed: true,
			ProgressID: existingID, CharacterName: name, QuestTitle: snapTitle, QuestVersion: existingVersion,
			GroupCredited: credits,
		}, nil
	}
	if err != nil {
		return contract.CompletionResponse{}, err
	}
	body := fmt.Sprintf("%s completed \"%s\" (version %d).", name, snapTitle, in.QuestVersion)
	if rule != nil {
		body = fmt.Sprintf("%s completed \"%s\" (version %d) with the party.", name, snapTitle, in.QuestVersion)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO adventure_log (player_id, character_id, quest_id, group_id, type, title, body)
		VALUES ($1,$2,$3,$4,'quest',$5,$6)`, playerID, in.CharacterID, in.QuestID, questGroup.String, "Saga completed", body); err != nil {
		return contract.CompletionResponse{}, err
	}
	var credits []contract.GroupCredit
	if groupOK {
		credits, err = creditGroup(ctx, tx, in.QuestID, in.QuestVersion, snapTitle, in.CharacterID, name, completedAt, members)
		if err != nil {
			return contract.CompletionResponse{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return contract.CompletionResponse{}, err
	}
	levelShown := in.Level
	if levelShown <= 0 {
		levelShown = storedLevel
	}
	resp := contract.CompletionResponse{
		Allowed: true, Completed: true, LogWritten: true, ProgressID: progressID,
		CharacterName: name, CharacterRace: raceName, CharacterClass: className, CharacterLevel: levelShown,
		QuestTitle: snapTitle, QuestVersion: in.QuestVersion,
		Objectives: snapObjectives, ObjectivesOrdered: snapOrdered,
		GroupCredited: credits,
	}
	if startedAt != nil {
		resp.StartedAt = contract.FormatTime(startedAt.(time.Time))
		resp.DurationSec = durationOut
	}
	return resp, nil
}

// creditGroup inserts one idempotent completion per other character in the roster.
// Unknown names are skipped. The caller's own row is the caller's job.
func creditGroup(ctx context.Context, tx querier, questID string, questVersion int, title, callerID, callerName string, completedAt time.Time, members []contract.GroupMember) ([]contract.GroupCredit, error) {
	var out []contract.GroupCredit
	seen := map[string]bool{}
	for _, m := range members {
		name := strings.TrimSpace(m.Name)
		realm := strings.TrimSpace(m.Realm)
		if name == "" || realm == "" {
			continue
		}
		key := strings.ToLower(name) + "\n" + strings.ToLower(realm)
		if seen[key] {
			continue
		}
		seen[key] = true
		var id, owner, foundName, foundRealm string
		err := tx.QueryRow(ctx, `
			SELECT id::text, player_id::text, name, realm
			FROM characters
			WHERE lower(name)=lower($1) AND lower(realm)=lower($2)
			LIMIT 1`, name, realm).Scan(&id, &owner, &foundName, &foundRealm)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if id == callerID {
			continue
		}
		var inGroup int
		err = tx.QueryRow(ctx, `
			SELECT 1 FROM group_members gm
			JOIN quests q ON q.group_id = gm.group_id
			WHERE q.id=$1 AND gm.player_id=$2`, questID, owner).Scan(&inGroup)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var progressID string
		var level any
		if m.Level > 0 {
			level = m.Level
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO progress (character_id, quest_id, quest_version, status, completed_at, level_at_completion)
			SELECT $1,$2,$3,'completed',$4,$5
			WHERE NOT EXISTS (
				SELECT 1 FROM progress WHERE character_id=$1 AND quest_id=$2
			)
			RETURNING id::text`,
			id, questID, questVersion, completedAt, level).Scan(&progressID)
		if errors.Is(err, pgx.ErrNoRows) {
			out = append(out, contract.GroupCredit{CharacterID: id, Name: foundName, Realm: foundRealm, Already: true})
			continue
		}
		if err != nil {
			return nil, err
		}
		body := fmt.Sprintf("%s completed \"%s\" (version %d) with %s.", foundName, title, questVersion, callerName)
		if _, err := tx.Exec(ctx, `
			INSERT INTO adventure_log (player_id, character_id, quest_id, group_id, type, title, body)
			VALUES ($1,$2,$3,(SELECT group_id FROM quests WHERE id=$3),'quest',$4,$5)`, owner, id, questID, "Saga completed", body); err != nil {
			return nil, err
		}
		out = append(out, contract.GroupCredit{CharacterID: id, Name: foundName, Realm: foundRealm})
	}
	return out, nil
}

func (s *Store) MarkWebhookPosted(ctx context.Context, progressID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE progress SET webhook_posted=true WHERE id=$1`, progressID)
	return err
}

// DeletePlayerProgress removes progress rows for a player's characters.
// questID empty deletes every row for that player. Returns how many were deleted.
func (s *Store) DeletePlayerProgress(ctx context.Context, playerID, questID string) (int64, error) {
	if err := validUUID(playerID); err != nil {
		return 0, err
	}
	if questID != "" {
		if err := validUUID(questID); err != nil {
			return 0, err
		}
		tag, err := s.pool.Exec(ctx, `
			DELETE FROM progress p
			USING characters c
			WHERE p.character_id = c.id AND c.player_id = $1 AND p.quest_id = $2`, playerID, questID)
		if err != nil {
			return 0, err
		}
		return tag.RowsAffected(), nil
	}
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM progress p
		USING characters c
		WHERE p.character_id = c.id AND c.player_id = $1`, playerID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// DeleteAdminProgress removes progress by character and/or quest.
// At least one of characterID or questID must be set.
func (s *Store) DeleteAdminProgress(ctx context.Context, characterID, questID string) (int64, error) {
	if characterID == "" && questID == "" {
		return 0, fmt.Errorf("character_id or saga_id is required")
	}
	if characterID != "" {
		if err := validUUID(characterID); err != nil {
			return 0, err
		}
	}
	if questID != "" {
		if err := validUUID(questID); err != nil {
			return 0, err
		}
	}
	var tag pgconn.CommandTag
	var err error
	switch {
	case characterID != "" && questID != "":
		tag, err = s.pool.Exec(ctx, `DELETE FROM progress WHERE character_id=$1 AND quest_id=$2`, characterID, questID)
	case characterID != "":
		tag, err = s.pool.Exec(ctx, `DELETE FROM progress WHERE character_id=$1`, characterID)
	default:
		tag, err = s.pool.Exec(ctx, `DELETE FROM progress WHERE quest_id=$1`, questID)
	}
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *Store) SetLevel(ctx context.Context, playerID string, in contract.LevelRequest) (contract.LevelResponse, error) {
	if err := validUUID(in.CharacterID); err != nil {
		return contract.LevelResponse{}, err
	}
	in.BattleTag = strings.TrimSpace(in.BattleTag)
	if in.BattleTag != "" {
		if err := validateBattleTag(in.BattleTag); err != nil {
			return contract.LevelResponse{}, err
		}
	}
	if in.Level < 1 || in.Level > 1000 {
		return contract.LevelResponse{}, fmt.Errorf("%w: level", ErrInvalid)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return contract.LevelResponse{}, err
	}
	defer tx.Rollback(ctx)
	var name, tag string
	var level int
	err = tx.QueryRow(ctx, `
		SELECT c.name, c.level, COALESCE(p.battletag, '')
		FROM characters c JOIN players p ON p.id = c.player_id
		WHERE c.id=$1 AND c.player_id=$2`, in.CharacterID, playerID).Scan(&name, &level, &tag)
	if errors.Is(err, pgx.ErrNoRows) {
		return contract.LevelResponse{Allowed: false}, ErrForbidden
	}
	if err != nil {
		return contract.LevelResponse{}, err
	}
	if tag != "" && !strings.EqualFold(tag, in.BattleTag) {
		return contract.LevelResponse{Allowed: false}, ErrForbidden
	}
	if level == in.Level {
		if err := tx.Commit(ctx); err != nil {
			return contract.LevelResponse{}, err
		}
		return contract.LevelResponse{Allowed: true, Level: level, Changed: false}, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE characters SET level=$2, updated_at=now() WHERE id=$1`, in.CharacterID, in.Level); err != nil {
		return contract.LevelResponse{}, err
	}
	body := fmt.Sprintf("%s reached level %d.", name, in.Level)
	if _, err := tx.Exec(ctx, `
		INSERT INTO adventure_log (player_id, character_id, type, title, body)
		VALUES ($1,$2,'level',$3,$4)`, playerID, in.CharacterID, fmt.Sprintf("Level %d", in.Level), body); err != nil {
		return contract.LevelResponse{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return contract.LevelResponse{}, err
	}
	return contract.LevelResponse{Allowed: true, Level: in.Level, Changed: true}, nil
}

func (s *Store) AddLog(ctx context.Context, playerID, characterID, questID, typ, title, body string) (contract.LogEntry, error) {
	if typ == "" || title == "" || body == "" {
		return contract.LogEntry{}, fmt.Errorf("%w: log fields", ErrInvalid)
	}
	if err := clip(typ, 40); err != nil || clip(title, 200) != nil || clip(body, 8000) != nil {
		return contract.LogEntry{}, fmt.Errorf("%w: log text", ErrInvalid)
	}
	if playerID != "" {
		if err := validUUID(playerID); err != nil {
			return contract.LogEntry{}, err
		}
	}
	if characterID != "" {
		if err := validUUID(characterID); err != nil {
			return contract.LogEntry{}, err
		}
	}
	if questID != "" {
		if err := validUUID(questID); err != nil {
			return contract.LogEntry{}, err
		}
	}
	var id string
	var created time.Time
	err := s.pool.QueryRow(ctx, `
		INSERT INTO adventure_log (player_id, character_id, quest_id, type, title, body)
		VALUES (NULLIF($1,'')::uuid, NULLIF($2,'')::uuid, NULLIF($3,'')::uuid, $4, $5, $6)
		RETURNING id::text, created_at`, playerID, characterID, questID, typ, title, body).Scan(&id, &created)
	if err != nil {
		return contract.LogEntry{}, err
	}
	return contract.LogEntry{
		ID: id, PlayerID: playerID, CharacterID: characterID, QuestID: questID,
		Type: typ, Title: title, Body: body, CreatedAt: contract.FormatTime(created),
	}, nil
}

func (s *Store) ListLog(ctx context.Context, battletag, characterID string, limit int) ([]contract.LogEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if characterID != "" {
		if err := validUUID(characterID); err != nil {
			return nil, err
		}
	}
	rows, err := s.pool.Query(ctx, `
		SELECT l.id::text, COALESCE(l.player_id::text,''), COALESCE(l.character_id::text,''), COALESCE(l.quest_id::text,''),
		       l.type, l.title, l.body, l.created_at
		FROM adventure_log l
		LEFT JOIN players p ON p.id = l.player_id
		WHERE ($1 = '' OR p.battletag = $1)
		  AND ($2 = '' OR l.character_id::text = $2)
		ORDER BY l.created_at DESC
		LIMIT $3`, battletag, characterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contract.LogEntry
	for rows.Next() {
		var e contract.LogEntry
		var created time.Time
		if err := rows.Scan(&e.ID, &e.PlayerID, &e.CharacterID, &e.QuestID, &e.Type, &e.Title, &e.Body, &created); err != nil {
			return nil, err
		}
		e.CreatedAt = contract.FormatTime(created)
		out = append(out, e)
	}
	if out == nil {
		out = []contract.LogEntry{}
	}
	return out, rows.Err()
}

const sessionTTL = 30 * 24 * time.Hour

// IssueSessionFor returns a 30-day gss_ session for a player who already exists.
func (s *Store) IssueSessionFor(ctx context.Context, playerID string) (contract.SessionResponse, error) {
	if err := validUUID(playerID); err != nil {
		return contract.SessionResponse{}, err
	}
	p, err := s.playerByID(ctx, playerID)
	if err != nil {
		return contract.SessionResponse{}, err
	}
	token, err := newToken("gss_")
	if err != nil {
		return contract.SessionResponse{}, err
	}
	expires := time.Now().UTC().Add(sessionTTL)
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO sessions (player_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
		p.ID, hashKey(token), expires); err != nil {
		return contract.SessionResponse{}, err
	}
	return contract.SessionResponse{
		TokenType: "Bearer", SessionToken: token, ExpiresAt: contract.FormatTime(expires), Player: p,
	}, nil
}

func (s *Store) playerByID(ctx context.Context, id string) (contract.Player, error) {
	var p contract.Player
	var created time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, COALESCE(battletag, ''), display_name, COALESCE(bnet_account_id, ''), created_at
		FROM players WHERE id=$1`, id).Scan(&p.ID, &p.BattleTag, &p.DisplayName, &p.BnetAccountID, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return contract.Player{}, ErrNotFound
	}
	if err != nil {
		return contract.Player{}, err
	}
	scanPlayer(&p, created)
	return p, nil
}

// UpsertCharacter stores a character reported by the addon for the logged-in player.
func (s *Store) UpsertCharacter(ctx context.Context, playerID string, in contract.CharacterUpsert) (contract.Character, error) {
	if err := validUUID(playerID); err != nil {
		return contract.Character{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Realm = strings.TrimSpace(in.Realm)
	in.Race = strings.TrimSpace(in.Race)
	in.Class = strings.TrimSpace(in.Class)
	in.BattleTag = strings.TrimSpace(in.BattleTag)
	if err := validateName(in.Name); err != nil {
		return contract.Character{}, err
	}
	if err := validateRealm(in.Realm); err != nil {
		return contract.Character{}, err
	}
	if err := clip(in.Race, 40); err != nil || clip(in.Class, 40) != nil {
		return contract.Character{}, fmt.Errorf("%w: character text", ErrInvalid)
	}
	if in.Level == 0 {
		in.Level = 1
	}
	if in.Level < 1 || in.Level > 1000 {
		return contract.Character{}, fmt.Errorf("%w: level", ErrInvalid)
	}
	if in.BattleTag != "" {
		if err := validateBattleTag(in.BattleTag); err != nil {
			return contract.Character{}, err
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return contract.Character{}, err
	}
	defer tx.Rollback(ctx)
	var tag string
	err = tx.QueryRow(ctx, `SELECT COALESCE(battletag, '') FROM players WHERE id=$1`, playerID).Scan(&tag)
	if errors.Is(err, pgx.ErrNoRows) {
		return contract.Character{}, ErrNotFound
	}
	if err != nil {
		return contract.Character{}, err
	}
	if in.BattleTag != "" && !strings.EqualFold(tag, in.BattleTag) {
		return contract.Character{}, fmt.Errorf("%w: battletag", ErrForbidden)
	}
	var owner string
	err = tx.QueryRow(ctx, `
		SELECT player_id::text FROM characters
		WHERE lower(name)=lower($1) AND lower(realm)=lower($2)`, in.Name, in.Realm).Scan(&owner)
	if err == nil && owner != playerID {
		return contract.Character{}, fmt.Errorf("%w: character", ErrConflict)
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return contract.Character{}, err
	}
	var id string
	var oldLevel int
	err = tx.QueryRow(ctx, `
		SELECT id::text, level FROM characters
		WHERE player_id=$1 AND lower(name)=lower($2) AND lower(realm)=lower($3)`,
		playerID, in.Name, in.Realm).Scan(&id, &oldLevel)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `
			INSERT INTO characters (player_id, name, realm, race, class, level)
			VALUES ($1,$2,$3,$4,$5,$6) RETURNING id::text`,
			playerID, in.Name, in.Realm, in.Race, in.Class, in.Level).Scan(&id)
		if err != nil {
			return contract.Character{}, mapPG(err)
		}
	} else if err != nil {
		return contract.Character{}, err
	} else {
		if _, err := tx.Exec(ctx, `
			UPDATE characters SET race=$2, class=$3, level=$4, updated_at=now() WHERE id=$1`,
			id, in.Race, in.Class, in.Level); err != nil {
			return contract.Character{}, err
		}
		if oldLevel != in.Level {
			body := fmt.Sprintf("%s reached level %d.", in.Name, in.Level)
			if _, err := tx.Exec(ctx, `
				INSERT INTO adventure_log (player_id, character_id, type, title, body)
				VALUES ($1,$2,'level',$3,$4)`, playerID, id, fmt.Sprintf("Level %d", in.Level), body); err != nil {
				return contract.Character{}, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return contract.Character{}, err
	}
	return s.GetCharacter(ctx, id)
}

func (s *Store) QuestIDByTitle(ctx context.Context, title string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `SELECT id::text FROM quests WHERE title=$1 ORDER BY created_at LIMIT 1`, title).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

func (s *Store) assertQuestOwner(ctx context.Context, questID, playerID string) error {
	if err := validUUID(questID); err != nil {
		return err
	}
	if err := validUUID(playerID); err != nil {
		return err
	}
	var owner sql.NullString
	err := s.pool.QueryRow(ctx, `SELECT created_by_player_id::text FROM quests WHERE id=$1`, questID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !owner.Valid || owner.String != playerID {
		return ErrForbidden
	}
	return nil
}

// AssertQuestOwnerByBattleTag allows admin patches that pass updated_by to
// edit only sagas authored by that BattleTag (case-insensitive).
func (s *Store) AssertQuestOwnerByBattleTag(ctx context.Context, questID, battleTag string) error {
	if err := validUUID(questID); err != nil {
		return err
	}
	tag := strings.TrimSpace(battleTag)
	if tag == "" {
		return fmt.Errorf("%w: updated_by required", ErrInvalid)
	}
	var createdBy string
	err := s.pool.QueryRow(ctx, `SELECT created_by FROM quests WHERE id=$1`, questID).Scan(&createdBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !strings.EqualFold(createdBy, tag) {
		return ErrNotSagaAuthor
	}
	return nil
}

// ListQuestsByPlayer returns sagas in groups this player belongs to.
func (s *Store) ListQuestsByPlayer(ctx context.Context, playerID string) ([]contract.Quest, error) {
	if err := validUUID(playerID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, questSelect+`
		WHERE q.group_id IN (SELECT group_id FROM group_members WHERE player_id=$1)
		ORDER BY q.updated_at DESC, q.title`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var quests []contract.Quest
	var ids []string
	for rows.Next() {
		q, err := scanQuest(rows.Scan)
		if err != nil {
			return nil, err
		}
		quests = append(quests, q)
		ids = append(ids, q.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	assigned, err := loadAssignments(ctx, s.pool, ids)
	if err != nil {
		return nil, err
	}
	for i := range quests {
		quests[i].AssignedTo = assigned[quests[i].ID]
		if quests[i].AssignedTo == nil {
			quests[i].AssignedTo = []contract.IDRef{}
		}
	}
	if err := applyChainPositions(ctx, s.pool, quests); err != nil {
		return nil, err
	}
	return quests, nil
}

// UpdateQuestForPlayer patches a saga only when the player owns it.
func (s *Store) UpdateQuestForPlayer(ctx context.Context, playerID, questID string, p QuestPatch) (contract.Quest, error) {
	if err := s.assertQuestOwner(ctx, questID, playerID); err != nil {
		return contract.Quest{}, err
	}
	// Players cannot reassign authorship.
	p.CreatedBy = nil
	return s.UpdateQuest(ctx, questID, p)
}

// CreateQuestForPlayer creates a saga owned by the signed-in player.
func (s *Store) CreateQuestForPlayer(ctx context.Context, playerID string, in QuestInput) (contract.Quest, error) {
	if err := validUUID(playerID); err != nil {
		return contract.Quest{}, err
	}
	var tag string
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(battletag, '') FROM players WHERE id=$1`, playerID).Scan(&tag); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return contract.Quest{}, ErrNotFound
		}
		return contract.Quest{}, err
	}
	if tag == "" {
		return contract.Quest{}, fmt.Errorf("%w: battletag required to create sagas", ErrInvalid)
	}
	groupID, err := s.resolveWriteGroup(ctx, playerID, in.GroupSlug)
	if err != nil {
		return contract.Quest{}, err
	}
	in.CreatedBy = tag
	in.GroupID = groupID
	return s.CreateQuest(ctx, in)
}

func (s *Store) SetQuestImage(ctx context.Context, playerID, questID, contentType string, image []byte) (contract.Quest, error) {
	if err := s.assertQuestOwner(ctx, questID, playerID); err != nil {
		return contract.Quest{}, err
	}
	return s.writeQuestImage(ctx, questID, contentType, image)
}

// AdminSetQuestImage sets a cover without ownership checks (ADMIN_TOKEN routes).
func (s *Store) AdminSetQuestImage(ctx context.Context, questID, contentType string, image []byte) (contract.Quest, error) {
	if err := validUUID(questID); err != nil {
		return contract.Quest{}, err
	}
	if _, err := s.GetQuest(ctx, questID); err != nil {
		return contract.Quest{}, err
	}
	return s.writeQuestImage(ctx, questID, contentType, image)
}

func (s *Store) writeQuestImage(ctx context.Context, questID, contentType string, image []byte) (contract.Quest, error) {
	if len(image) == 0 {
		return contract.Quest{}, fmt.Errorf("%w: empty image", ErrInvalid)
	}
	if len(image) > 8<<20 {
		return contract.Quest{}, fmt.Errorf("%w: image larger than 8 MiB", ErrInvalid)
	}
	ct := strings.TrimSpace(contentType)
	if ct == "" {
		ct = "application/octet-stream"
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE quests SET image_bytes=$2, image_content_type=$3, updated_at=now() WHERE id=$1`,
		questID, image, ct)
	if err != nil {
		return contract.Quest{}, err
	}
	detail, err := s.GetQuest(ctx, questID)
	if err != nil {
		return contract.Quest{}, err
	}
	return detail.Quest, nil
}

func (s *Store) ClearQuestImage(ctx context.Context, playerID, questID string) (contract.Quest, error) {
	if err := s.assertQuestOwner(ctx, questID, playerID); err != nil {
		return contract.Quest{}, err
	}
	return s.clearQuestImage(ctx, questID)
}

// AdminClearQuestImage clears a cover without ownership checks (ADMIN_TOKEN routes).
func (s *Store) AdminClearQuestImage(ctx context.Context, questID string) (contract.Quest, error) {
	if err := validUUID(questID); err != nil {
		return contract.Quest{}, err
	}
	if _, err := s.GetQuest(ctx, questID); err != nil {
		return contract.Quest{}, err
	}
	return s.clearQuestImage(ctx, questID)
}

func (s *Store) clearQuestImage(ctx context.Context, questID string) (contract.Quest, error) {
	_, err := s.pool.Exec(ctx, `
		UPDATE quests SET image_bytes=NULL, image_content_type='', updated_at=now() WHERE id=$1`, questID)
	if err != nil {
		return contract.Quest{}, err
	}
	detail, err := s.GetQuest(ctx, questID)
	if err != nil {
		return contract.Quest{}, err
	}
	return detail.Quest, nil
}

func (s *Store) QuestImage(ctx context.Context, questID string) (contentType string, image []byte, err error) {
	if err := validUUID(questID); err != nil {
		return "", nil, err
	}
	err = s.pool.QueryRow(ctx, `
		SELECT COALESCE(image_content_type, ''), image_bytes FROM quests WHERE id=$1`, questID).Scan(&contentType, &image)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, ErrNotFound
	}
	if err != nil {
		return "", nil, err
	}
	if len(image) == 0 {
		return "", nil, ErrNotFound
	}
	return contentType, image, nil
}
