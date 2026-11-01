package savedvars

import "testing"

func TestParsePendingCharacter(t *testing.T) {
	body := `SagaloreDB = {
["pending_character"] = {
["name"] = "Magní",
["realm"] = "Celebras",
["level"] = 12,
["race"] = "Dwarf",
["class"] = "Warrior",
["battletag"] = "Magnus#2231",
["at"] = 1761144240,
},
}
`
	ch, ok := ParsePendingCharacter("x.lua", body)
	if !ok {
		t.Fatal("expected character")
	}
	if ch.Name != "Magní" || ch.Realm != "Celebras" || ch.Level != 12 || ch.Class != "Warrior" || ch.BattleTag != "Magnus#2231" {
		t.Fatalf("%+v", ch)
	}
}
