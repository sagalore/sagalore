package queue

import (
	"path/filepath"
	"testing"
	"time"
)

func TestDedupeAndPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.json")
	q, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	key := CompletionKey("quest-1", "Magni", "Argent Dawn")
	if !q.Enqueue(Item{Kind: "completion", Key: key, QuestID: "quest-1", Character: "Magni"}) {
		t.Fatal("first enqueue")
	}
	if q.Enqueue(Item{Kind: "completion", Key: key, QuestID: "quest-1", Character: "magni"}) {
		t.Fatal("duplicate quest+character")
	}
	otherRealm := CompletionKey("quest-1", "Magni", "Silvermoon")
	if !q.Enqueue(Item{Kind: "completion", Key: otherRealm, QuestID: "quest-1", Character: "Magni", Realm: "Silvermoon"}) {
		t.Fatal("same name on another realm is a different character")
	}
	if err := q.Save(); err != nil {
		t.Fatal(err)
	}
	q.Succeed(key)
	if err := q.Save(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.Enqueue(Item{Key: key, Kind: "completion"}) {
		t.Fatal("finished key must stay deduped")
	}
	if again.Len() != 1 {
		t.Fatalf("len %d", again.Len())
	}
	now := time.Now()
	again.Fail(otherRealm, "network", false, now)
	due := again.Due(now)
	if len(due) != 0 {
		t.Fatal("should wait for backoff")
	}
	due = again.Due(now.Add(6 * time.Second))
	if len(due) != 1 {
		t.Fatalf("due after backoff %#v", due)
	}
	again.Fail(otherRealm, "nope", true, now)
	if len(again.Due(now.Add(24*time.Hour))) != 0 {
		t.Fatal("permanent failure must not retry")
	}
}

func TestLevelKey(t *testing.T) {
	if LevelKey("Magni", "Argent Dawn", 12) != LevelKey("magni", "argent dawn", 12) {
		t.Fatal("case")
	}
	if LevelKey("Magni", "Argent Dawn", 12) == LevelKey("Magni", "Argent Dawn", 13) {
		t.Fatal("level should differ")
	}
}
