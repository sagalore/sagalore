package contract

import (
	"encoding/json"
	"testing"
)

func TestNormalizeRequiresAndChain(t *testing.T) {
	ids, err := NormalizeRequires([]string{
		"AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE",
		"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
	})
	if err != nil || len(ids) != 1 || ids[0] != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Fatalf("%v %v", ids, err)
	}
	if _, err := NormalizeRequires([]string{"not-a-uuid"}); err == nil {
		t.Fatal("expected bad id")
	}
	chain, err := NormalizeChain(&Chain{ID: "goldshire", Name: " Goldshire ", Order: 2, Index: 9, Total: 9})
	if err != nil || chain.ID != "goldshire" || chain.Name != "Goldshire" || chain.Order != 2 || chain.Index != 0 || chain.Total != 0 {
		t.Fatalf("%+v %v", chain, err)
	}
	if _, err := NormalizeChain(&Chain{ID: "has space", Name: "A", Order: 1}); err == nil {
		t.Fatal("expected bad chain id")
	}
	if _, err := NormalizeChain(&Chain{ID: "ok", Name: "A", Order: 0}); err == nil {
		t.Fatal("expected bad order")
	}
}

func TestParseRequiresObjects(t *testing.T) {
	ids, set, err := ParseRequires(json.RawMessage(`[{"id":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","title":"En"}]`))
	if err != nil || !set || len(ids) != 1 {
		t.Fatalf("%v %v %v", ids, set, err)
	}
	ids, set, err = ParseRequires(nil)
	if err != nil || set || ids != nil {
		t.Fatalf("omit %v %v %v", ids, set, err)
	}
	ids, set, err = ParseRequires(json.RawMessage(`null`))
	if err != nil || !set || len(ids) != 0 {
		t.Fatalf("null %v %v %v", ids, set, err)
	}
	chain, set, err := ParseChain(json.RawMessage(`null`))
	if err != nil || !set || chain != nil {
		t.Fatalf("chain null %+v %v %v", chain, set, err)
	}
}

func TestRequireCycleAndLockMessage(t *testing.T) {
	if !HasRequireCycle(map[string][]string{"a": {"b"}, "b": {"a"}}) {
		t.Fatal("cycle")
	}
	if !HasRequireCycle(map[string][]string{"a": {"a"}}) {
		t.Fatal("self")
	}
	if HasRequireCycle(map[string][]string{"a": {"b"}, "b": {}}) {
		t.Fatal("chain is not a cycle")
	}
	if LockMessage([]string{"En kveld"}) != "Locked: finish En kveld first" {
		t.Fatal(LockMessage([]string{"En kveld"}))
	}
	if LockMessage([]string{"A", "B"}) != "Locked: finish A and B first" {
		t.Fatal(LockMessage([]string{"A", "B"}))
	}
	if LockMessage([]string{"A", "B", "C"}) != "Locked: finish A, B and C first" {
		t.Fatal(LockMessage([]string{"A", "B", "C"}))
	}
}

func TestGateIgnoresCompletionVersion(t *testing.T) {
	// done is keyed only by saga id. An older saga_version still counts.
	done := map[string]bool{"chapter-1": true}
	locked, missing := Gate("chapter-2", []string{"chapter-1"}, done)
	if locked || missing != nil {
		t.Fatalf("prereq done on any version should unlock: %v %v", locked, missing)
	}
	locked, missing = Gate("chapter-2", []string{"chapter-1"}, nil)
	if !locked || len(missing) != 1 {
		t.Fatalf("missing prereq: %v %v", locked, missing)
	}
	locked, _ = Gate("chapter-1", []string{"other"}, map[string]bool{"chapter-1": true})
	if locked {
		t.Fatal("already completed saga stays unlocked")
	}
	locks := SagaLocks(
		[]Character{{ID: "c1"}, {ID: "c2"}},
		[]Quest{{ID: "q2", Requires: []string{"q1"}}, {ID: "q1"}},
		[]Progress{{QuestID: "q1", CharacterID: "c1", Status: "completed", QuestVersion: 1}},
	)
	got := map[string]SagaLock{}
	for _, l := range locks {
		got[l.CharacterID+"/"+l.QuestID] = l
	}
	if got["c1/q2"].Locked || !got["c1/q2"].Unlocked {
		t.Fatalf("c1 should be unlocked %+v", got["c1/q2"])
	}
	if !got["c2/q2"].Locked || got["c2/q2"].Unlocked || len(got["c2/q2"].Missing) != 1 {
		t.Fatalf("c2 should be locked %+v", got["c2/q2"])
	}
}
