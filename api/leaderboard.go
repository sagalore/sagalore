package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sagalore/sagalore/internal/contract"
	"github.com/sagalore/sagalore/internal/syncdata"
)

// leaderboardNow is the clock for period windows. Tests replace it.
var leaderboardNow = time.Now

func (s *Server) groupLeaderboard(w http.ResponseWriter, r *http.Request) {
	period := strings.TrimSpace(r.URL.Query().Get("period"))
	if period == "" {
		period = "all"
	}
	out, err := s.store.GroupLeaderboard(r.Context(), playerFrom(r).ID, r.PathValue("id"), period)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// GroupLeaderboard ranks characters in one group by distinct sagas completed
// in the period. The caller must be a member. Sessions and personal tokens
// both reach this through withPlayer.
func (s *Store) GroupLeaderboard(ctx context.Context, playerID, groupID, period string) (contract.Leaderboard, error) {
	if err := validUUID(groupID); err != nil {
		return contract.Leaderboard{}, err
	}
	if _, err := s.memberRole(ctx, groupID, playerID); err != nil {
		return contract.Leaderboard{}, err
	}
	var name string
	err := s.pool.QueryRow(ctx, `SELECT name FROM groups WHERE id=$1`, groupID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return contract.Leaderboard{}, ErrNotFound
	}
	if err != nil {
		return contract.Leaderboard{}, err
	}
	return s.leaderboard(ctx, groupID, name, period)
}

// LeaderboardsForPlayer is every period for every group the player belongs to.
// The companion writes this into SagaloreData.lua.
func (s *Store) LeaderboardsForPlayer(ctx context.Context, playerID string) ([]contract.Leaderboard, error) {
	groups, err := s.GroupsForPlayer(ctx, playerID)
	if err != nil {
		return nil, err
	}
	out := []contract.Leaderboard{}
	for _, g := range groups {
		for _, period := range []string{"all", "month", "week", "today"} {
			board, err := s.leaderboard(ctx, g.ID, g.Name, period)
			if err != nil {
				return nil, err
			}
			out = append(out, board)
		}
	}
	return out, nil
}

func (s *Store) leaderboard(ctx context.Context, groupID, groupName, period string) (contract.Leaderboard, error) {
	period = strings.TrimSpace(period)
	if period == "" {
		period = "all"
	}
	start, end, unbounded, err := syncdata.PeriodBounds(period, leaderboardNow())
	if err != nil {
		return contract.Leaderboard{}, fmt.Errorf("%w: period", ErrInvalid)
	}
	var startArg, endArg any
	if !unbounded {
		startArg = start.UTC()
		endArg = end.UTC()
	}
	rows, err := s.pool.Query(ctx, `
		SELECT c.name, c.realm, COALESCE(c.class, ''), COALESCE(c.race, ''), c.level,
			COUNT(DISTINCT pr.quest_id), MAX(pr.completed_at)
		FROM progress pr
		JOIN characters c ON c.id = pr.character_id
		JOIN quests q ON q.id = pr.quest_id
		JOIN group_members gm ON gm.group_id = q.group_id AND gm.player_id = c.player_id
		WHERE pr.status = 'completed'
		  AND q.group_id = $1
		  AND ($2::timestamptz IS NULL OR pr.completed_at >= $2)
		  AND ($3::timestamptz IS NULL OR pr.completed_at < $3)
		GROUP BY c.id, c.name, c.realm, c.class, c.race, c.level
		ORDER BY COUNT(DISTINCT pr.quest_id) DESC, MAX(pr.completed_at) ASC, c.name, c.realm`,
		groupID, startArg, endArg)
	if err != nil {
		return contract.Leaderboard{}, err
	}
	defer rows.Close()
	entries := []contract.LeaderboardEntry{}
	for rows.Next() {
		var e contract.LeaderboardEntry
		var last sql.NullTime
		if err := rows.Scan(&e.Name, &e.Realm, &e.Class, &e.Race, &e.Level, &e.Completed, &last); err != nil {
			return contract.Leaderboard{}, err
		}
		if last.Valid {
			e.LastCompletedAt = contract.FormatTime(last.Time)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return contract.Leaderboard{}, err
	}
	for i := range entries {
		entries[i].Rank = i + 1
	}
	return contract.Leaderboard{
		GroupID: groupID, GroupName: groupName, Period: period, Entries: entries,
	}, nil
}

// assertQuestReader allows any member of the saga's group to read it.
// A saga with no group stays readable by its author.
func (s *Store) assertQuestReader(ctx context.Context, questID, playerID string) error {
	if err := validUUID(questID); err != nil {
		return err
	}
	if err := validUUID(playerID); err != nil {
		return err
	}
	var groupID, author sql.NullString
	err := s.pool.QueryRow(ctx, `
		SELECT group_id::text, created_by_player_id::text FROM quests WHERE id=$1`, questID).Scan(&groupID, &author)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if groupID.Valid && strings.TrimSpace(groupID.String) != "" {
		_, err = s.memberRole(ctx, groupID.String, playerID)
		return err
	}
	if author.Valid && author.String == playerID {
		return nil
	}
	return ErrForbidden
}

func validateActivitySort(mode string) error {
	switch mode {
	case "", "completed_at", "completion_time":
		return nil
	default:
		return fmt.Errorf("%w: sort", ErrInvalid)
	}
}

// sortCompleters orders a saga activity list.
// completed_at is newest first. completion_time is shortest duration first,
// with missing durations last.
func sortCompleters(list []contract.QuestCompleter, mode string) {
	switch mode {
	case "completed_at":
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].CompletedAt != list[j].CompletedAt {
				return list[i].CompletedAt > list[j].CompletedAt
			}
			if list[i].Character != list[j].Character {
				return list[i].Character < list[j].Character
			}
			return list[i].Realm < list[j].Realm
		})
	case "completion_time":
		sort.SliceStable(list, func(i, j int) bool {
			di, dj := list[i].DurationSec, list[j].DurationSec
			if (di == nil) != (dj == nil) {
				return di != nil
			}
			if di != nil && dj != nil && *di != *dj {
				return *di < *dj
			}
			if list[i].CompletedAt != list[j].CompletedAt {
				return list[i].CompletedAt > list[j].CompletedAt
			}
			if list[i].Character != list[j].Character {
				return list[i].Character < list[j].Character
			}
			return list[i].Realm < list[j].Realm
		})
	}
}

func durationPtr(n sql.NullInt32) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int32)
	return &v
}

func formatNullTime(t sql.NullTime) string {
	if !t.Valid {
		return ""
	}
	return contract.FormatTime(t.Time)
}

func formatDuration(sec int) string {
	if sec < 0 {
		return ""
	}
	h := sec / 3600
	m := (sec % 3600) / 60
	s := sec % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %02dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm %02ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

// completionTiming parses an addon-reported start and the duration in seconds.
// A start after the finish is stored, and the duration is left empty.
func completionTiming(completed time.Time, raw string) (started any, duration any, out *int, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil, nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%w: started_at", ErrInvalid)
	}
	t = t.UTC()
	sec := int64(completed.Sub(t).Seconds())
	if sec >= 0 && sec <= 2147483647 {
		n := int(sec)
		return t, n, &n, nil
	}
	return t, nil, nil, nil
}

func fillStarted(ctx context.Context, tx querier, progressID string, started, duration any) error {
	if started == nil || progressID == "" {
		return nil
	}
	_, err := tx.Exec(ctx, `
		UPDATE progress SET started_at=$2, duration_sec=$3
		WHERE id=$1 AND started_at IS NULL`, progressID, started, duration)
	return err
}
