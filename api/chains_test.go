package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sagalore/sagalore/internal/contract"
)

func chainObjective(id, text string) map[string]any {
	return map[string]any{"id": id, "type": "interact", "text": text, "npc": "Innkeeper Farley"}
}

func TestSagaChains(t *testing.T) {
	resetDB(t)
	srv := httptest.NewServer((&Server{store: NewStore(testPool), adminToken: "admin-test"}).Handler())
	t.Cleanup(srv.Close)
	admin := apiClient{base: srv.URL, token: "admin-test", http: srv.Client()}
	playerRec, token := devSession(t, "bnet-magnus", "Magnus#2231", "Magnus")
	devGroup(t, playerRec.ID, "tutorial", "Tutorial")
	author := apiClient{base: srv.URL, token: token, http: srv.Client()}
	created := contract.CreatePlayerResponse{Player: playerRec, APIKey: token}

	var magni, mira contract.Character
	resp, err := admin.do(http.MethodPost, "/v1/admin/characters", map[string]any{
		"player_id": created.Player.ID, "name": "Magni", "realm": "Argent Dawn",
		"race": "Dwarf", "class": "Paladin", "level": 12,
	}, &magni)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("magni %d %v", statusOf(resp), err)
	}
	resp, err = admin.do(http.MethodPost, "/v1/admin/characters", map[string]any{
		"player_id": created.Player.ID, "name": "Mira", "realm": "Argent Dawn",
		"race": "Human", "class": "Mage", "level": 12,
	}, &mira)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("mira %d %v", statusOf(resp), err)
	}

	var first contract.Quest
	resp, err = author.do(http.MethodPost, "/v1/sagas", map[string]any{
		"title": "Kapittel 1", "description": "Start",
		"objectives": []map[string]any{chainObjective("talk", "Snakk med Farley")},
		"party":      true, "chain": map[string]any{"id": "goldshire", "name": "Goldshire", "order": 1},
	}, &first)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("chapter 1 %d %v %+v", statusOf(resp), err, first)
	}
	if first.Chain == nil || first.Chain.Index != 1 || first.Chain.Total != 1 || len(first.Requires) != 0 {
		t.Fatalf("chapter 1 chain %+v requires %v", first.Chain, first.Requires)
	}

	var unknown contract.ErrorBody
	resp, err = author.do(http.MethodPost, "/v1/sagas", map[string]any{
		"title": "Ukjent", "description": "x",
		"objectives": []map[string]any{chainObjective("talk", "Snakk")},
		"party":      true,
		"requires":   []string{"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
	}, &unknown)
	if err != nil || resp.StatusCode != http.StatusBadRequest || !strings.Contains(unknown.Message, "unknown prerequisite") {
		t.Fatalf("unknown prereq %d %+v %v", statusOf(resp), unknown, err)
	}

	var second contract.Quest
	resp, err = author.do(http.MethodPost, "/v1/sagas", map[string]any{
		"title": "Kapittel 2", "description": "Videre",
		"objectives": []map[string]any{chainObjective("road", "Stå på veien")},
		"party":      true, "requires": []string{first.ID},
		"chain": map[string]any{"id": "goldshire", "name": "Goldshire", "order": 2},
	}, &second)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("chapter 2 %d %v", statusOf(resp), err)
	}
	if second.Chain == nil || second.Chain.Index != 2 || second.Chain.Total != 2 || len(second.Requires) != 1 {
		t.Fatalf("chapter 2 %+v requires %v", second.Chain, second.Requires)
	}

	resp, err = author.do(http.MethodPost, "/v1/sagas", map[string]any{
		"title": "Kapittel 2 igjen", "description": "x",
		"objectives": []map[string]any{chainObjective("talk", "Snakk")},
		"party":      true, "chain": map[string]any{"id": "goldshire", "name": "Goldshire", "order": 2},
	}, &unknown)
	if err != nil || resp.StatusCode != http.StatusBadRequest || !strings.Contains(unknown.Message, "chain order") {
		t.Fatalf("duplicate order %d %+v %v", statusOf(resp), unknown, err)
	}

	resp, err = author.do(http.MethodPatch, "/v1/sagas/"+first.ID, map[string]any{
		"requires": []string{second.ID}, "changes": []string{"Krever neste"},
	}, &unknown)
	if err != nil || resp.StatusCode != http.StatusBadRequest || !strings.Contains(unknown.Message, "cycle") {
		t.Fatalf("cycle %d %+v %v", statusOf(resp), unknown, err)
	}
	var detail contract.QuestDetail
	resp, err = admin.do(http.MethodGet, "/v1/admin/sagas/"+first.ID, nil, &detail)
	if err != nil || detail.Version != 1 || detail.Chain == nil || detail.Chain.Total != 2 {
		t.Fatalf("cycle must roll back %+v %v", detail.Quest, err)
	}

	resp, err = author.do(http.MethodPatch, "/v1/sagas/"+second.ID, map[string]any{
		"requires": []map[string]any{{"id": second.ID}}, "changes": []string{"Seg selv"},
	}, &unknown)
	if err != nil || resp.StatusCode != http.StatusBadRequest || !strings.Contains(unknown.Message, "itself") {
		t.Fatalf("self %d %+v %v", statusOf(resp), unknown, err)
	}

	var listed struct {
		Quests []contract.QuestListItem `json:"sagas"`
	}
	resp, err = admin.do(http.MethodGet, "/v1/admin/sagas?character_id="+magni.ID, nil, &listed)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatal(err, statusOf(resp))
	}
	gates := map[string]contract.QuestListItem{}
	for _, q := range listed.Quests {
		gates[q.ID] = q
	}
	if gates[first.ID].Locked == nil || *gates[first.ID].Locked || gates[first.ID].Unlocked == nil || !*gates[first.ID].Unlocked {
		t.Fatalf("chapter 1 should be unlocked %+v", gates[first.ID])
	}
	if gates[second.ID].Locked == nil || !*gates[second.ID].Locked || len(gates[second.ID].Missing) != 1 {
		t.Fatalf("chapter 2 should be locked %+v", gates[second.ID])
	}

	player := apiClient{base: srv.URL, token: created.APIKey, http: srv.Client()}
	var denied contract.ErrorBody
	resp, err = player.do(http.MethodPost, "/v1/complete", contract.CompletionRequest{
		CharacterID: magni.ID, QuestID: second.ID, QuestVersion: 1, BattleTag: "Magnus#2231", Level: 12,
	}, &denied)
	if err != nil || resp.StatusCode != http.StatusConflict || denied.Error != "locked" || denied.Message != "Locked: finish Kapittel 1 first" {
		t.Fatalf("locked complete %d %+v %v", statusOf(resp), denied, err)
	}

	var done contract.CompletionResponse
	resp, err = player.do(http.MethodPost, "/v1/complete", contract.CompletionRequest{
		CharacterID: magni.ID, QuestID: first.ID, QuestVersion: 1, BattleTag: "Magnus#2231", Level: 12,
	}, &done)
	if err != nil || resp.StatusCode != 200 || !done.Completed || done.QuestVersion != 1 {
		t.Fatalf("complete chapter 1 %+v %d %v", done, statusOf(resp), err)
	}

	var edited contract.Quest
	resp, err = author.do(http.MethodPatch, "/v1/sagas/"+first.ID, map[string]any{
		"title": "Kapittel 1, redigert", "changes": []string{"Nytt navn"},
	}, &edited)
	if err != nil || edited.Version != 2 {
		t.Fatalf("edit %+v %v", edited, err)
	}

	resp, err = player.do(http.MethodPost, "/v1/complete", contract.CompletionRequest{
		CharacterID: magni.ID, QuestID: second.ID, QuestVersion: 1, BattleTag: "Magnus#2231", Level: 12,
	}, &done)
	if err != nil || resp.StatusCode != 200 || !done.Completed || done.AlreadyCompleted {
		t.Fatalf("chapter 2 should accept a prerequisite completed on version 1: %+v %d %v", done, statusOf(resp), err)
	}

	resp, err = player.do(http.MethodPost, "/v1/complete", contract.CompletionRequest{
		CharacterID: magni.ID, QuestID: first.ID, QuestVersion: 2, BattleTag: "Magnus#2231",
	}, &done)
	if err != nil || !done.AlreadyCompleted || done.QuestVersion != 1 {
		t.Fatalf("stored version stays 1: %+v", done)
	}

	var syncOut contract.SyncResponse
	resp, err = player.do(http.MethodGet, "/v1/sync", nil, &syncOut)
	if err != nil || resp.StatusCode != 200 {
		t.Fatal(err, statusOf(resp))
	}
	lockBy := map[string]contract.SagaLock{}
	for _, l := range syncOut.Locks {
		lockBy[l.CharacterID+"/"+l.QuestID] = l
	}
	if lockBy[magni.ID+"/"+second.ID].Locked || !lockBy[magni.ID+"/"+second.ID].Unlocked {
		t.Fatalf("magni unlocked %+v", lockBy[magni.ID+"/"+second.ID])
	}
	if !lockBy[mira.ID+"/"+second.ID].Locked || lockBy[mira.ID+"/"+second.ID].Missing[0] != first.ID {
		t.Fatalf("mira still locked %+v", lockBy[mira.ID+"/"+second.ID])
	}

	var cleared contract.Quest
	resp, err = author.do(http.MethodPatch, "/v1/sagas/"+second.ID, map[string]any{
		"requires": []string{},
		"chain":    nil,
		"changes":  []string{"Fjernet kjede"},
	}, &cleared)
	if err != nil || resp.StatusCode != 200 || cleared.Chain != nil || len(cleared.Requires) != 0 {
		t.Fatalf("clear %+v %d %v", cleared, statusOf(resp), err)
	}
	resp, err = admin.do(http.MethodGet, "/v1/admin/sagas/"+second.ID, nil, &detail)
	if err != nil || len(detail.Versions) < 2 {
		t.Fatalf("versions %+v", detail.Versions)
	}
	prev := detail.Versions[len(detail.Versions)-2]
	if prev.Chain == nil || prev.Chain.Order != 2 || len(prev.Requires) != 1 {
		t.Fatalf("snapshot kept the chain %+v", prev)
	}
}
