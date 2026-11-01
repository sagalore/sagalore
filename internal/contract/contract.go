// Package contract is the JSON API shared by the Sagalore API and the companion.
package contract

import "time"

// SyncResponse is GET /v1/sync. The companion merges quests onto characters;
// the server does not pre-filter level or assignment (except that it omits
// inactive quests that this player has no progress on). Locks is the
// prerequisite gate for each of this player's characters.
type SyncResponse struct {
	BattleTag    string            `json:"battletag"`
	Email        string            `json:"email,omitempty"`
	DisplayName  string            `json:"display_name,omitempty"`
	GeneratedAt  string            `json:"generated_at"`
	Characters   []Character       `json:"characters"`
	Quests       []Quest           `json:"sagas"`
	Progress     []Progress        `json:"progress"`
	Completions  []QuestCompletion `json:"completions,omitempty"`
	Locks        []SagaLock        `json:"locks"`
	Groups       []GroupSummary    `json:"groups"`
	Invites      []Invite          `json:"invites"`
	Leaderboards []Leaderboard     `json:"leaderboards,omitempty"`
}

// QuestCompletion is one character who has finished a saga. Sync includes
// every character in the database, not only the signed-in player.
type QuestCompletion struct {
	QuestID     string `json:"saga_id"`
	Character   string `json:"character"`
	Realm       string `json:"realm"`
	BattleTag   string `json:"battletag,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Level       int    `json:"level,omitempty"`
	Race        string `json:"race,omitempty"`
	Class       string `json:"class,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
	StartedAt   string `json:"started_at,omitempty"`
	// DurationSec is completed_at - started_at in seconds. Nil when the addon
	// did not report a start, or the start is after the finish.
	DurationSec *int `json:"duration_sec,omitempty"`
}

// Leaderboard is one period of saga completions for characters in a group.
// Rank is 1-based. Completed is the number of distinct sagas finished in the
// period, not the number of finish rows.
type Leaderboard struct {
	GroupID   string             `json:"group_id"`
	GroupName string             `json:"group_name,omitempty"`
	Period    string             `json:"period"`
	Entries   []LeaderboardEntry `json:"entries"`
}

// LeaderboardEntry is one character on a group leaderboard.
type LeaderboardEntry struct {
	Rank            int    `json:"rank"`
	Name            string `json:"name"`
	Realm           string `json:"realm"`
	Class           string `json:"class,omitempty"`
	Race            string `json:"race,omitempty"`
	Level           int    `json:"level"`
	Completed       int    `json:"completed"`
	LastCompletedAt string `json:"last_completed_at,omitempty"`
}

type Character struct {
	ID            string `json:"id"`
	PlayerID      string `json:"player_id,omitempty"`
	Name          string `json:"name"`
	Realm         string `json:"realm"`
	Level         int    `json:"level"`
	Race          string `json:"race"`
	Class         string `json:"class"`
	BattleTag     string `json:"battletag"`
	Backstory     string `json:"backstory,omitempty"`
	Participating bool   `json:"participating"`
}

type IDRef struct {
	ID string `json:"id"`
}

type Quest struct {
	ID                string      `json:"id"`
	Title             string      `json:"title"`
	Description       string      `json:"description"`
	Objectives        []Objective `json:"objectives"`
	ObjectivesOrdered bool        `json:"objectives_ordered"`
	When              *TimeWindow `json:"when,omitempty"`
	Group             *GroupRule  `json:"group,omitempty"`
	// Tenant group (Forever / Gutar). Distinct from Group, the party-size rule.
	GroupID        string   `json:"group_id,omitempty"`
	GroupSlug      string   `json:"group_slug,omitempty"`
	GroupName      string   `json:"group_name,omitempty"`
	// PendingInviteID is set when this saga is preloaded for a pending join invite
	// (not yet a member). Empty once the account belongs to the group.
	PendingInviteID string   `json:"pending_invite_id,omitempty"`
	MinLevel        *int     `json:"min_level"`
	PartyQuest      bool     `json:"party"`
	AssignedTo      []IDRef  `json:"assigned_to"`
	CreatedBy       string   `json:"created_by"`
	Active          bool     `json:"active"`
	Version         int      `json:"version"`
	HasImage        bool     `json:"has_image"`
	ImageURL        string   `json:"image_url,omitempty"`
	ImageSignedURL  string   `json:"image_signed_url,omitempty"`
	Requires        []string `json:"requires"`
	Chain           *Chain   `json:"chain,omitempty"`
}

type Progress struct {
	QuestID       string `json:"saga_id"`
	CharacterID   string `json:"character_id"`
	QuestVersion  int    `json:"saga_version"`
	Status        string `json:"status"`
	CompletedAt   string `json:"completed_at,omitempty"`
	StartedAt     string `json:"started_at,omitempty"`
	DurationSec   *int   `json:"duration_sec,omitempty"`
	ScreenshotURL string `json:"screenshot_url,omitempty"`
}

// CompletionRequest is the JSON form of POST /v1/complete.
// screenshot_base64 is optional. Multipart uploads use the same fields plus a file part named "screenshot".
type CompletionRequest struct {
	CharacterID      string        `json:"character_id"`
	QuestID          string        `json:"saga_id"`
	QuestVersion     int           `json:"saga_version"`
	BattleTag        string        `json:"battletag"`
	Level            int           `json:"level,omitempty"`
	Race             string        `json:"race,omitempty"`
	Class            string        `json:"class,omitempty"`
	CompletedAt      string        `json:"completed_at,omitempty"`
	StartedAt        string        `json:"started_at,omitempty"`
	ScreenshotBase64 string        `json:"screenshot_base64,omitempty"`
	Instance         string        `json:"instance,omitempty"`
	GroupMembers     []GroupMember `json:"group_members,omitempty"`
}

type CompletionResponse struct {
	Allowed           bool          `json:"allowed"`
	AlreadyCompleted  bool          `json:"already_completed"`
	Completed         bool          `json:"completed"`
	ProgressID        string        `json:"progress_id,omitempty"`
	LogWritten        bool          `json:"log_written"`
	WebhookPosted     bool          `json:"webhook_posted"`
	CharacterName     string        `json:"character_name,omitempty"`
	CharacterRace     string        `json:"character_race,omitempty"`
	CharacterClass    string        `json:"character_class,omitempty"`
	CharacterLevel    int           `json:"character_level,omitempty"`
	QuestTitle        string        `json:"saga_title,omitempty"`
	QuestVersion      int           `json:"saga_version,omitempty"`
	StartedAt         string        `json:"started_at,omitempty"`
	DurationSec       *int          `json:"duration_sec,omitempty"`
	Objectives        []Objective   `json:"objectives,omitempty"`
	ObjectivesOrdered bool          `json:"objectives_ordered,omitempty"`
	GroupCredited     []GroupCredit `json:"group_credited,omitempty"`
}

type LevelRequest struct {
	CharacterID string `json:"character_id"`
	BattleTag   string `json:"battletag"`
	Level       int    `json:"level"`
}

type LevelResponse struct {
	Allowed bool `json:"allowed"`
	Level   int  `json:"level"`
	Changed bool `json:"changed"`
}

type Player struct {
	ID            string `json:"id"`
	BattleTag     string `json:"battletag"`
	DisplayName   string `json:"display_name"`
	BnetAccountID string `json:"bnet_account_id,omitempty"`
	CreatedAt     string `json:"created_at"`
	// OAuthClientID is set by PlayerByKey when the bearer is an OAuth access token.
	// It is not part of the JSON API.
	OAuthClientID string `json:"-"`
}

// GroupSummary is a tenant the signed-in account belongs to.
// It is not the saga party-size rule, which stays on the objective and saga field named group.
type GroupSummary struct {
	ID       string `json:"id"`
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Locale   string `json:"locale"`
	Role     string `json:"role,omitempty"`
	OwnerID  string `json:"owner_player_id,omitempty"`
	Personal bool   `json:"personal,omitempty"`
}

// GroupMemberRow is one account in a tenant group (not a party roster line).
type GroupMemberRow struct {
	PlayerID    string `json:"player_id"`
	BattleTag   string `json:"battletag,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Role        string `json:"role"`
	JoinedAt    string `json:"joined_at,omitempty"`
}

// Invite is addressed to a character. kind is join (new member) or include (alt already on a member account).
type Invite struct {
	ID        string `json:"id"`
	GroupID   string `json:"group_id"`
	GroupName string `json:"group_name"`
	Name      string `json:"name"`
	Realm     string `json:"realm"`
	BattleTag string `json:"battletag,omitempty"`
	Status    string `json:"status"`
	Kind      string `json:"kind"`
	InvitedAt string `json:"invited_at,omitempty"`
}

// PersonalToken is a long-lived gst_ credential. Token is set only when it is minted.
type PersonalToken struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Token     string `json:"token,omitempty"`
	CreatedAt string `json:"created_at"`
}

// SessionResponse is what the API returns after Battle.net login.
// The session token is a Sagalore credential (gss_). It is not a Battle.net token.
type SessionResponse struct {
	TokenType    string `json:"token_type"`
	SessionToken string `json:"session_token"`
	ExpiresAt    string `json:"expires_at"`
	Player       Player `json:"player"`
}

// CharacterUpsert is PUT /v1/characters. The companion sends what the addon read.
type CharacterUpsert struct {
	Name      string `json:"name"`
	Realm     string `json:"realm"`
	Level     int    `json:"level"`
	Race      string `json:"race"`
	Class     string `json:"class"`
	BattleTag string `json:"battletag"`
}

type CreatePlayerResponse struct {
	Player Player `json:"player"`
	APIKey string `json:"api_key"`
}

type QuestVersion struct {
	Version     int         `json:"version"`
	Title       string      `json:"title"`
	Description string      `json:"description"`
	Objectives  []Objective `json:"objectives"`
	When        *TimeWindow `json:"when,omitempty"`
	Group       *GroupRule  `json:"group,omitempty"`
	Changes     []string    `json:"changes"`
	MinLevel    *int        `json:"min_level"`
	PartyQuest  bool        `json:"party"`
	Active      bool        `json:"active"`
	AssignedTo  []IDRef     `json:"assigned_to"`
	CreatedBy   string      `json:"created_by"`
	CreatedAt   string      `json:"created_at"`
	Requires    []string    `json:"requires"`
	Chain       *Chain      `json:"chain,omitempty"`
}

type QuestDetail struct {
	Quest
	Versions      []QuestVersion   `json:"versions"`
	CompletedBy   []QuestCompleter `json:"completed_by"`
	GoalsMarkdown string           `json:"goals_markdown,omitempty"`
	Card          *QuestCard       `json:"card,omitempty"`
}

// QuestCompleter is someone who finished this saga (admin/detail views).
type QuestCompleter struct {
	Character   string `json:"character"`
	Realm       string `json:"realm"`
	Class       string `json:"class,omitempty"`
	Level       int    `json:"level,omitempty"`
	Race        string `json:"race,omitempty"`
	BattleTag   string `json:"battletag,omitempty"`
	CompletedAt string `json:"completed_at"`
	StartedAt   string `json:"started_at,omitempty"`
	DurationSec *int   `json:"duration_sec,omitempty"`
	When        string `json:"when,omitempty"`
}

// QuestCard is a table of who finished a saga.
type QuestCard struct {
	Columns []QuestCardColumn `json:"columns"`
	Rows    []QuestCardRow    `json:"rows"`
}

type QuestCardColumn struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Fill        bool   `json:"fill,omitempty"`
	Bold        bool   `json:"bold,omitempty"`
	IsMultiline bool   `json:"isMultiline,omitempty"`
}

type QuestCardRow struct {
	ID    string         `json:"id"`
	Group string         `json:"group,omitempty"`
	Cells map[string]any `json:"cells"`
}

// QuestListItem is an admin quest row. Status is null when character_id was
// requested and that character has not started the quest. Eligible, Locked,
// and Unlocked are set only when character_id was requested. Locked is the
// prerequisite gate and is independent of level and assignment.
type QuestListItem struct {
	Quest
	Status           *string  `json:"status"`
	CompletedAt      string   `json:"completed_at,omitempty"`
	CompletedVersion int      `json:"completed_version,omitempty"`
	Eligible         *bool    `json:"eligible,omitempty"`
	Locked           *bool    `json:"locked,omitempty"`
	Unlocked         *bool    `json:"unlocked,omitempty"`
	Missing          []string `json:"missing,omitempty"`
}

type LogEntry struct {
	ID          string `json:"id"`
	PlayerID    string `json:"player_id,omitempty"`
	CharacterID string `json:"character_id,omitempty"`
	QuestID     string `json:"saga_id,omitempty"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	CreatedAt   string `json:"created_at"`
}

type ErrorBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// Eligible reports whether a character should see a quest.
// A character sees a quest when it is active and either it is a party quest
// or assigned_to contains the character id. min_level is not a hard gate —
// the addon colors the title by difficulty (like the WoW quest log) instead.
// level is kept for call-site compatibility and is unused.
func Eligible(level int, characterID string, quest Quest) bool {
	_ = level
	if !quest.Active {
		return false
	}
	if quest.PartyQuest {
		return true
	}
	for _, a := range quest.AssignedTo {
		if a.ID == characterID {
			return true
		}
	}
	return false
}

// FormatTime renders a UTC timestamp the way the API writes it.
func FormatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
