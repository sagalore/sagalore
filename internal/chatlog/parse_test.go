package chatlog

import "testing"

func TestParseCompleteRealmWithSpaces(t *testing.T) {
	line := `10/22 14:44:00.123  [Sagalore] COMPLETE 11111111-1111-1111-1111-111111111111 3 Magni Argent Dawn 12 1761144240`
	ev, ok := Parse(line)
	if !ok {
		t.Fatal("not parsed")
	}
	if ev.Kind != KindComplete || ev.QuestID != "11111111-1111-1111-1111-111111111111" || ev.QuestVersion != 3 {
		t.Fatalf("%+v", ev)
	}
	if ev.Character != "Magni" || ev.Realm != "Argent Dawn" || ev.Level != 12 || ev.At.Unix() != 1761144240 {
		t.Fatalf("%+v", ev)
	}
}

func TestParseCompleteFromChannelEcho(t *testing.T) {
	line := `10/22 22:30:00.000  [Sagalore] Magní: [Sagalore] COMPLETE 11111111-1111-1111-1111-111111111111 3 Magní Celebras 2 1761144240`
	ev, ok := Parse(line)
	if !ok || ev.Kind != KindComplete || ev.Character != "Magní" || ev.Realm != "Celebras" {
		t.Fatalf("%+v ok=%v", ev, ok)
	}
}

func TestParseCharacterPipes(t *testing.T) {
	line := `[Sagalore] CHARACTER Magni|Argent Dawn|12|Dwarf|Warrior|Magnus#2231|1761144240`
	ev, ok := Parse(line)
	if !ok || ev.Kind != KindCharacter || ev.Character != "Magni" || ev.Realm != "Argent Dawn" {
		t.Fatalf("%+v ok=%v", ev, ok)
	}
	if ev.Level != 12 || ev.Race != "Dwarf" || ev.Class != "Warrior" || ev.BattleTag != "Magnus#2231" {
		t.Fatalf("%+v", ev)
	}
	if _, ok := Parse(`[Sagalore] CHARACTER Magni|Realm|12|Dwarf|Warrior|notag|1761144240`); ok {
		t.Fatal("battletag")
	}
}

func TestParseLevelAndJunk(t *testing.T) {
	ev, ok := Parse(`[Sagalore] LEVELUP Magni Kel'Thuzad 13 1761144300`)
	if !ok || ev.Kind != KindLevel || ev.Realm != "Kel'Thuzad" || ev.Level != 13 {
		t.Fatalf("%+v ok=%v", ev, ok)
	}
	if _, ok := Parse(`[Sagalore] COMPLETE bare-id`); ok {
		t.Fatal("short line should fail")
	}
	if _, ok := Parse("Chat: Fullført – noe"); ok {
		t.Fatal("friendly line must not parse")
	}
	if _, ok := Parse(`[Sagalore] COMPLETE id 1 Magni - 0 1761144240`); ok {
		t.Fatal("level 0")
	}
	ev, ok = Parse(`[Sagalore] COMPLETE abc 0 Magni - 4 1761144240`)
	if !ok || ev.Realm != "" || ev.QuestVersion != 0 || ev.Level != 4 {
		t.Fatalf("%+v", ev)
	}
}

func TestParseRejectsUnknownPrefix(t *testing.T) {
	ev, ok := Parse(`[Other] COMPLETE 11111111-1111-1111-1111-111111111111 3 Magni Argent Dawn 12 1761144240`)
	if ok {
		t.Fatalf("unexpected %+v", ev)
	}
}
