package app

import (
	"testing"

	"github.com/sagalore/sagalore/internal/config"
	"github.com/sagalore/sagalore/internal/contract"
)

func TestAccountStatusLoggedOut(t *testing.T) {
	user := accountStatus(false, contract.Player{}, config.Config{}, contract.SyncResponse{}, "en")
	if user != "Not logged in" {
		t.Fatalf("got %q", user)
	}
}

func TestAccountStatusPrefersBattleTag(t *testing.T) {
	user := accountStatus(true, contract.Player{
		DisplayName: "Magnus", BattleTag: "old#1",
	}, config.Config{BattleTag: "cfg#2"}, contract.SyncResponse{
		BattleTag: "Magnus#2231",
		Characters: []contract.Character{
			{Name: "Magní", Realm: "Celebras", Class: "Hunter", Level: 2},
		},
	}, "en")
	if user != "Account: Magnus#2231" {
		t.Fatalf("user=%q", user)
	}
}
