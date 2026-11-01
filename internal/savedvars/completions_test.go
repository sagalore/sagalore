package savedvars

import (
	"strings"
	"testing"
)

func TestParseCompletions(t *testing.T) {
	body := `
SagaloreDB = {
["completions"] = {
["4cc34091-0e9b-4c18-800a-3162c90acc56"] = {
["saga_id"] = "4cc34091-0e9b-4c18-800a-3162c90acc56",
["character"] = "Magní",
["version"] = 3,
["level"] = 2,
["completed_at"] = 1792698415,
["started_at"] = 1792698000,
["realm"] = "Celebras",
["race"] = "Dwarf",
["class"] = "Hunter",
["group_members"] = "Magní/Celebras/18;Mira/Celebras/17",
["instance"] = "The Deadmines",
},
["81006099-ddf8-4bc3-9ba0-b78a2614ba30"] = {
["saga_id"] = "81006099-ddf8-4bc3-9ba0-b78a2614ba30",
["character"] = "Magní",
["version"] = 3,
["level"] = 2,
["completed_at"] = 1792698415,
["realm"] = "Celebras",
},
},
["objectives"] = {
},
}
`
	got := ParseCompletions("x.lua", body)
	if len(got) != 2 {
		t.Fatalf("got %d: %+v", len(got), got)
	}
	if got[0].Character != "Magní" || got[0].Realm != "Celebras" || got[0].QuestVersion != 3 || got[0].Level != 2 {
		t.Fatalf("%+v", got[0])
	}
	if got[0].Race != "Dwarf" || got[0].Class != "Hunter" {
		t.Fatalf("race/class %+v", got[0])
	}
	if !strings.Contains(got[0].QuestID, "4cc34091") {
		t.Fatalf("%s", got[0].QuestID)
	}
	if got[0].At.Unix() != 1792698415 || got[0].Started.Unix() != 1792698000 {
		t.Fatalf("at %v started %v", got[0].At, got[0].Started)
	}
	if got[0].Instance != "The Deadmines" || len(got[0].GroupMembers) != 2 || got[0].GroupMembers[1].Name != "Mira" || got[0].GroupMembers[1].Level != 17 {
		t.Fatalf("group %+v", got[0].GroupMembers)
	}
}
