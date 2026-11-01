package syncdata

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/sagalore/sagalore/internal/contract"
)

// ReloadNews compares two syncs the way the addon will see them.
// The first sync after the companion starts is quiet: startup already asks
// the player to /reload. Later changes produce a desktop line (reload now)
// and a shorter line the addon shows once the new file has been loaded.
//
// An unchanged sync with a new generated_at returns empty strings, so the
// ten-minute fallback poll does not notify.
func ReloadNews(prev, next contract.SyncResponse) (desktop, addon string) {
	if !hasSnapshot(prev) {
		return "", ""
	}
	before := snapshot(prev)
	after := snapshot(next)
	var created, updated, retired, completed []string
	for id, n := range after {
		p, ok := before[id]
		if !ok {
			created = append(created, n.title)
			continue
		}
		if p.active && !n.active {
			retired = append(retired, n.title)
			continue
		}
		if p.version != n.version || p.title != n.title || p.body != n.body || p.chars != n.chars {
			updated = append(updated, n.title)
			continue
		}
		if p.status != n.status || p.completers != n.completers {
			completed = append(completed, n.title)
		}
	}
	for id, p := range before {
		if _, ok := after[id]; !ok {
			retired = append(retired, p.title)
		}
	}
	switch {
	case len(created) > 0:
		return formatNews("created", created)
	case len(updated) > 0:
		return formatNews("updated", updated)
	case len(retired) > 0:
		return formatNews("retired", retired)
	case len(completed) > 0:
		return formatNews("completed", completed)
	default:
		return "", ""
	}
}

func hasSnapshot(s contract.SyncResponse) bool {
	return s.GeneratedAt != "" || len(s.Quests) > 0 || len(s.Characters) > 0 || len(s.Progress) > 0 || len(s.Completions) > 0
}

type questSnap struct {
	title      string
	body       string
	version    int
	status     string
	completers int
	chars      string
	active     bool
}

func snapshot(sync contract.SyncResponse) map[string]questSnap {
	catalog := map[string]contract.Quest{}
	for _, q := range sync.Quests {
		catalog[q.ID] = q
	}
	type acc struct {
		snap  questSnap
		chars map[string]struct{}
	}
	raw := map[string]*acc{}
	for _, e := range Merge(sync) {
		a := raw[e.QuestID]
		if a == nil {
			title := e.Title
			if title == "" {
				title = "Saga"
			}
			a = &acc{chars: map[string]struct{}{}}
			a.snap.title = title
			a.snap.body = e.Description + "\x1e" + objectivesSig(e.Objectives)
			raw[e.QuestID] = a
		}
		if e.QuestVersion > a.snap.version {
			a.snap.version = e.QuestVersion
		}
		if e.Status == "completed" {
			a.snap.status = "completed"
		} else if a.snap.status == "" {
			a.snap.status = e.Status
		}
		if n := len(e.CompletedBy); n > a.snap.completers {
			a.snap.completers = n
		}
		if e.CharacterID != "" {
			a.chars[e.CharacterID] = struct{}{}
		}
	}
	out := make(map[string]questSnap, len(raw))
	for id, a := range raw {
		if q, ok := catalog[id]; ok {
			if q.Version > a.snap.version {
				a.snap.version = q.Version
			}
			a.snap.active = q.Active
			if q.Title != "" {
				a.snap.title = q.Title
			}
		}
		ids := make([]string, 0, len(a.chars))
		for c := range a.chars {
			ids = append(ids, c)
		}
		sort.Strings(ids)
		a.snap.chars = strings.Join(ids, ",")
		out[id] = a.snap
	}
	return out
}

func objectivesSig(list []contract.Objective) string {
	var b strings.Builder
	for _, o := range list {
		b.WriteString(o.ID)
		b.WriteByte(0)
		b.WriteString(o.Type)
		b.WriteByte(0)
		b.WriteString(o.Text)
		b.WriteByte(0)
		b.WriteString(strconv.Itoa(o.Count))
		b.WriteByte(1)
	}
	return b.String()
}

func formatNews(kind string, titles []string) (string, string) {
	titles = uniqueTitles(titles)
	if len(titles) == 0 {
		return "", ""
	}
	title := titles[0]
	extra := ""
	if len(titles) > 1 {
		extra = fmt.Sprintf(" and %d more", len(titles)-1)
	}
	switch kind {
	case "created":
		return "New saga: " + title + extra + " — type /reload in WoW", "New saga: " + title + extra + " is loaded."
	case "updated":
		return "Saga updated: " + title + extra + " — type /reload in WoW", "Saga updated: " + title + extra + " is loaded."
	case "retired":
		return "Saga retired: " + title + extra + " — type /reload in WoW", "Saga retired: " + title + extra + " is loaded."
	default:
		return "Saga completed: " + title + extra + " — type /reload in WoW", "Saga completed: " + title + extra + " is loaded."
	}
}

func uniqueTitles(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, t := range in {
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
