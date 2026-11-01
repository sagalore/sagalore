package syncdata

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sagalore/sagalore/internal/contract"
)

// WriteLua renders SagaloreData.lua. The addon reads this file at load /reload.
func WriteLua(battletag, generatedAt string, entries []Entry) string {
	return WriteLuaFile(battletag, generatedAt, entries, "", "")
}

// WriteLuaFile is WriteLua plus an optional one-shot notice. The addon shows
// notice once, after /reload, when notice_id changes.
func WriteLuaFile(battletag, generatedAt string, entries []Entry, notice, noticeID string) string {
	var b strings.Builder
	b.WriteString("-- Written by the Sagalore companion. Do not edit by hand.\n")
	b.WriteString("-- Type /reload to load new sagas.\n")
	b.WriteString("SagaloreData = {\n")
	b.WriteString("  version = 1,\n")
	fmt.Fprintf(&b, "  battletag = %s,\n", luaString(battletag))
	fmt.Fprintf(&b, "  generated_at = %s,\n", luaString(generatedAt))
	if notice != "" && noticeID != "" {
		fmt.Fprintf(&b, "  notice = %s,\n", luaString(notice))
		fmt.Fprintf(&b, "  notice_id = %s,\n", luaString(noticeID))
	}
	b.WriteString("  entries = {\n")
	for _, e := range entries {
		b.WriteString("    {\n")
		fmt.Fprintf(&b, "      character = %s,\n", luaString(e.Character))
		fmt.Fprintf(&b, "      character_id = %s,\n", luaString(e.CharacterID))
		fmt.Fprintf(&b, "      realm = %s,\n", luaString(e.Realm))
		fmt.Fprintf(&b, "      saga_id = %s,\n", luaString(e.QuestID))
		fmt.Fprintf(&b, "      saga_version = %d,\n", e.QuestVersion)
		fmt.Fprintf(&b, "      title = %s,\n", luaString(e.Title))
		fmt.Fprintf(&b, "      description = %s,\n", luaString(e.Description))
		fmt.Fprintf(&b, "      objectives = %s,\n", luaObjectives(e.Objectives))
		if e.ObjectivesOrdered {
			b.WriteString("      objectives_ordered = true,\n")
		} else {
			b.WriteString("      objectives_ordered = false,\n")
		}
		writeLuaWhen(&b, e.When, "      ")
		writeLuaGroup(&b, e.Group, "      ")
		if e.GroupID != "" {
			fmt.Fprintf(&b, "      group_id = %s,\n", luaString(e.GroupID))
		}
		if e.GroupSlug != "" {
			fmt.Fprintf(&b, "      group_slug = %s,\n", luaString(e.GroupSlug))
		}
		if e.GroupName != "" {
			fmt.Fprintf(&b, "      group_name = %s,\n", luaString(e.GroupName))
		}
		if e.PendingInviteID != "" {
			fmt.Fprintf(&b, "      pending_invite_id = %s,\n", luaString(e.PendingInviteID))
		}
		if e.MinLevel == nil {
			b.WriteString("      min_level = nil,\n")
		} else {
			fmt.Fprintf(&b, "      min_level = %d,\n", *e.MinLevel)
		}
		fmt.Fprintf(&b, "      status = %s,\n", luaString(e.Status))
		fmt.Fprintf(&b, "      completed_at = %s,\n", luaString(e.CompletedAt))
		if e.PartyQuest {
			b.WriteString("      party = true,\n")
		} else {
			b.WriteString("      party = false,\n")
		}
		fmt.Fprintf(&b, "      created_by = %s,\n", luaString(e.CreatedBy))
		fmt.Fprintf(&b, "      requires = %s,\n", luaRequires(e.Requires))
		if e.ChainID != "" {
			fmt.Fprintf(&b, "      chain_id = %s,\n", luaString(e.ChainID))
			fmt.Fprintf(&b, "      chain_name = %s,\n", luaString(e.ChainName))
			fmt.Fprintf(&b, "      chain_order = %d,\n", e.ChainOrder)
			if e.ChainIndex > 0 && e.ChainTotal > 0 {
				fmt.Fprintf(&b, "      chain_index = %d,\n", e.ChainIndex)
				fmt.Fprintf(&b, "      chain_total = %d,\n", e.ChainTotal)
			}
		}
		if e.Locked {
			b.WriteString("      locked = true,\n")
		} else {
			b.WriteString("      locked = false,\n")
		}
		fmt.Fprintf(&b, "      completed_by = %s,\n", luaCompleters(e.CompletedBy))
		if e.Image != "" {
			fmt.Fprintf(&b, "      image = %s,\n", luaString(e.Image))
			if e.ImageAspect > 0.05 {
				fmt.Fprintf(&b, "      image_aspect = %s,\n", strconv.FormatFloat(e.ImageAspect, 'f', 4, 64))
			}
		}
		b.WriteString("    },\n")
	}
	b.WriteString("  },\n}\n")
	return b.String()
}

// WriteSyncLua is WriteLuaFile plus groups, invites, and character participation.
func WriteSyncLua(battletag, generatedAt string, entries []Entry, notice, noticeID string, groups []contract.GroupSummary, invites []contract.Invite, characters []contract.Character, leaderboards []contract.Leaderboard) string {
	body := WriteLuaFile(battletag, generatedAt, entries, notice, noticeID)
	// WriteLuaFile closes the table. Replace the final closing brace with the extra sections.
	const close = "}\n"
	if !strings.HasSuffix(body, close) {
		return body
	}
	var extra strings.Builder
	writeLuaGroups(&extra, groups, invites, characters)
	writeLuaLeaderboards(&extra, leaderboards)
	return strings.TrimSuffix(body, close) + extra.String() + close
}

func writeLuaGroups(b *strings.Builder, groups []contract.GroupSummary, invites []contract.Invite, characters []contract.Character) {
	b.WriteString("  groups = {\n")
	for _, g := range groups {
		personal := "false"
		if g.Personal {
			personal = "true"
		}
		fmt.Fprintf(b, "    { id = %s, slug = %s, name = %s, personal = %s },\n",
			luaString(g.ID), luaString(g.Slug), luaString(g.Name), personal)
	}
	b.WriteString("  },\n  invites = {\n")
	for _, inv := range invites {
		fmt.Fprintf(b, "    { id = %s, group_id = %s, group_name = %s, name = %s, realm = %s, battletag = %s, kind = %s },\n",
			luaString(inv.ID), luaString(inv.GroupID), luaString(inv.GroupName), luaString(inv.Name), luaString(inv.Realm), luaString(inv.BattleTag), luaString(inv.Kind))
	}
	b.WriteString("  },\n  characters = {\n")
	for _, c := range characters {
		flag := "false"
		if c.Participating {
			flag = "true"
		}
		fmt.Fprintf(b, "    { name = %s, realm = %s, participating = %s },\n", luaString(c.Name), luaString(c.Realm), flag)
	}
	b.WriteString("  },\n")
}

func writeLuaLeaderboards(b *strings.Builder, boards []contract.Leaderboard) {
	b.WriteString("  leaderboards = {\n")
	for _, board := range boards {
		b.WriteString("    {\n")
		fmt.Fprintf(b, "      group_id = %s,\n", luaString(board.GroupID))
		fmt.Fprintf(b, "      group_name = %s,\n", luaString(board.GroupName))
		fmt.Fprintf(b, "      period = %s,\n", luaString(board.Period))
		b.WriteString("      entries = {\n")
		for _, e := range board.Entries {
			fmt.Fprintf(b, "        { rank = %d, name = %s, realm = %s, class = %s, race = %s, level = %d, completed = %d, last_completed_at = %s },\n",
				e.Rank, luaString(e.Name), luaString(e.Realm), luaString(e.Class), luaString(e.Race),
				e.Level, e.Completed, luaString(e.LastCompletedAt))
		}
		b.WriteString("      },\n")
		b.WriteString("    },\n")
	}
	b.WriteString("  },\n")
}

func luaObjectives(list []contract.Objective) string {
	return luaObjectiveList(list, "      ")
}

func luaObjectiveList(list []contract.Objective, indent string) string {
	if len(list) == 0 {
		return "{}"
	}
	var b strings.Builder
	b.WriteString("{\n")
	for _, o := range list {
		writeLuaObjective(&b, o, indent+"  ")
	}
	b.WriteString(indent)
	b.WriteString("}")
	return b.String()
}

func writeLuaObjective(b *strings.Builder, o contract.Objective, indent string) {
	b.WriteString(indent)
	b.WriteString("{\n")
	field := indent + "  "
	fmt.Fprintf(b, "%sid = %s,\n", field, luaString(o.ID))
	fmt.Fprintf(b, "%stype = %s,\n", field, luaString(o.Type))
	fmt.Fprintf(b, "%stext = %s,\n", field, luaString(o.Text))
	writeLuaString(b, field, "memory", o.Memory)
	writeLuaString(b, field, "zone", o.Zone)
	writeLuaString(b, field, "subzone", o.Subzone)
	writeLuaString(b, field, "npc", o.NPC)
	writeLuaString(b, field, "object", o.Object)
	writeLuaString(b, field, "item_name", o.ItemName)
	writeLuaString(b, field, "emote", o.Emote)
	if o.MapID != nil {
		fmt.Fprintf(b, "%smap_id = %d,\n", field, *o.MapID)
	}
	writeLuaFloat(b, field, "x", o.X)
	writeLuaFloat(b, field, "y", o.Y)
	writeLuaFloat(b, field, "radius", o.Radius)
	if o.ItemID != nil {
		fmt.Fprintf(b, "%sitem_id = %d,\n", field, *o.ItemID)
	}
	if o.Count > 0 {
		fmt.Fprintf(b, "%scount = %d,\n", field, o.Count)
	}
	if o.Level > 0 {
		fmt.Fprintf(b, "%slevel = %d,\n", field, o.Level)
	}
	if len(o.Kinds) > 0 {
		b.WriteString(field)
		b.WriteString("kinds = {")
		for i, k := range o.Kinds {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(luaString(k))
		}
		b.WriteString("},\n")
	}
	if o.WithinSec > 0 {
		fmt.Fprintf(b, "%swithin_sec = %d,\n", field, o.WithinSec)
	}
	writeLuaWhen(b, o.When, field)
	writeLuaStepGroup(b, o.Group, field)
	if len(o.Beats) > 0 {
		fmt.Fprintf(b, "%sbeats = %s,\n", field, luaObjectiveList(o.Beats, field))
	}
	if len(o.Children) > 0 {
		fmt.Fprintf(b, "%sobjectives = %s,\n", field, luaObjectiveList(o.Children, field))
	}
	b.WriteString(indent)
	b.WriteString("},\n")
}

func writeLuaWhen(b *strings.Builder, w *contract.TimeWindow, indent string) {
	if w == nil {
		return
	}
	fmt.Fprintf(b, "%swhen = {\n", indent)
	fmt.Fprintf(b, "%s  clock = %s,\n", indent, luaString(w.Clock))
	if w.Start != "" {
		fmt.Fprintf(b, "%s  start = %s,\n", indent, luaString(w.Start))
		// "end" is a Lua keyword, so it cannot be a bare field name.
		fmt.Fprintf(b, "%s  [\"end\"] = %s,\n", indent, luaString(w.End))
	}
	if w.StartsAt != "" {
		fmt.Fprintf(b, "%s  starts_at = %s,\n", indent, luaString(w.StartsAt))
	}
	if w.EndsAt != "" {
		fmt.Fprintf(b, "%s  ends_at = %s,\n", indent, luaString(w.EndsAt))
	}
	fmt.Fprintf(b, "%s},\n", indent)
}

func writeLuaStepGroup(b *strings.Builder, g *contract.StepGroup, indent string) {
	if g == nil {
		return
	}
	fmt.Fprintf(b, "%sgroup = {\n", indent)
	fmt.Fprintf(b, "%s  level = %d,\n", indent, g.Level)
	if g.Size > 0 {
		fmt.Fprintf(b, "%s  size = %d,\n", indent, g.Size)
	}
	if g.Instance != "" {
		fmt.Fprintf(b, "%s  instance = %s,\n", indent, luaString(g.Instance))
	}
	fmt.Fprintf(b, "%s},\n", indent)
}

func writeLuaGroup(b *strings.Builder, g *contract.GroupRule, indent string) {
	if g == nil {
		return
	}
	fmt.Fprintf(b, "%sgroup = {\n", indent)
	fmt.Fprintf(b, "%s  size = %d,\n", indent, g.Size)
	if g.MinLevel != nil {
		fmt.Fprintf(b, "%s  min_level = %d,\n", indent, *g.MinLevel)
	}
	if g.Instance != "" {
		fmt.Fprintf(b, "%s  instance = %s,\n", indent, luaString(g.Instance))
	}
	fmt.Fprintf(b, "%s},\n", indent)
}

func luaRequires(list []contract.Require) string {
	if len(list) == 0 {
		return "{}"
	}
	var b strings.Builder
	b.WriteString("{\n")
	for _, r := range list {
		b.WriteString("        { ")
		fmt.Fprintf(&b, "id = %s, title = %s", luaString(r.ID), luaString(r.Title))
		b.WriteString(" },\n")
	}
	b.WriteString("      }")
	return b.String()
}

func luaCompleters(list []Completer) string {
	if len(list) == 0 {
		return "{}"
	}
	var b strings.Builder
	b.WriteString("{\n")
	for _, c := range list {
		when := c.When
		if when == "" {
			when = FormatOsloWhen(c.CompletedAt)
		}
		b.WriteString("        { ")
		fmt.Fprintf(&b, "character = %s, realm = %s, battletag = %s, level = %d, race = %s, class = %s, completed_at = %s, when = %s",
			luaString(c.Character), luaString(c.Realm), luaString(c.BattleTag),
			c.Level, luaString(c.Race), luaString(c.Class),
			luaString(c.CompletedAt), luaString(when))
		if c.StartedAt != "" {
			fmt.Fprintf(&b, ", started_at = %s", luaString(c.StartedAt))
		}
		if c.DurationSec != nil {
			fmt.Fprintf(&b, ", duration_sec = %d", *c.DurationSec)
		}
		b.WriteString(" },\n")
	}
	b.WriteString("      }")
	return b.String()
}

func writeLuaString(b *strings.Builder, indent, key, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(b, "%s%s = %s,\n", indent, key, luaString(value))
}

func writeLuaFloat(b *strings.Builder, indent, key string, value *float64) {
	if value == nil {
		return
	}
	fmt.Fprintf(b, "%s%s = %s,\n", indent, key, strconv.FormatFloat(*value, 'f', -1, 64))
}

func luaString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 32 {
				fmt.Fprintf(&b, "\\%d", r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
