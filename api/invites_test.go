package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sagalore/sagalore/internal/contract"
)

func TestInviteListAndRevokeAuthz(t *testing.T) {
	resetDB(t)
	srv := httptest.NewServer((&Server{store: NewStore(testPool)}).Handler())
	t.Cleanup(srv.Close)
	owner, ownerToken := devSession(t, "bnet-owner", "Owner#1", "Owner")
	group := devGroup(t, owner.ID, "tutorial", "Tutorial")
	member, memberToken := devSession(t, "bnet-member", "Member#2", "Member")
	joinGroup(t, group.ID, member.ID)
	_, outsiderToken := devSession(t, "bnet-outsider", "Outsider#3", "Outsider")
	invitee, inviteeToken := devSession(t, "bnet-invitee", "Invitee#4", "Invitee")
	_ = invitee

	ownerAPI := apiClient{base: srv.URL, token: ownerToken, http: srv.Client()}
	memberAPI := apiClient{base: srv.URL, token: memberToken, http: srv.Client()}
	outsiderAPI := apiClient{base: srv.URL, token: outsiderToken, http: srv.Client()}
	inviteeAPI := apiClient{base: srv.URL, token: inviteeToken, http: srv.Client()}

	var created contract.Invite
	resp, err := ownerAPI.do(http.MethodPost, "/v1/groups/"+group.ID+"/invites", map[string]string{
		"name": "Magni", "realm": "Everlook", "battletag": "Invitee#4",
	}, &created)
	if err != nil || resp.StatusCode != http.StatusCreated || created.ID == "" || created.InvitedAt == "" {
		t.Fatalf("create %d %v %+v", statusOf(resp), err, created)
	}
	resp, err = memberAPI.do(http.MethodPost, "/v1/groups/"+group.ID+"/invites", map[string]string{
		"name": "Other", "realm": "Everlook",
	}, nil)
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("member create %d %v", statusOf(resp), err)
	}

	var listed struct {
		Invites []contract.Invite `json:"invites"`
	}
	resp, err = ownerAPI.do(http.MethodGet, "/v1/groups/"+group.ID+"/invites?status=pending", nil, &listed)
	if err != nil || resp.StatusCode != http.StatusOK || len(listed.Invites) != 1 {
		t.Fatalf("owner list %d %v %+v", statusOf(resp), err, listed.Invites)
	}
	got := listed.Invites[0]
	if got.ID != created.ID || got.Name != "Magni" || got.Realm != "Everlook" || got.Status != "pending" {
		t.Fatalf("owner row %+v", got)
	}
	if _, err := time.Parse(time.RFC3339, got.InvitedAt); err != nil {
		t.Fatal(err)
	}
	resp, err = memberAPI.do(http.MethodGet, "/v1/groups/"+group.ID+"/invites", nil, &listed)
	if err != nil || resp.StatusCode != http.StatusOK || len(listed.Invites) != 1 || listed.Invites[0].Name != "Magni" {
		t.Fatalf("member list %d %v %+v", statusOf(resp), err, listed.Invites)
	}
	resp, err = outsiderAPI.do(http.MethodGet, "/v1/groups/"+group.ID+"/invites?status=pending", nil, nil)
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("outsider list %d %v", statusOf(resp), err)
	}
	resp, err = ownerAPI.do(http.MethodGet, "/v1/groups/"+group.ID+"/invites?status=accepted", nil, nil)
	if err != nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d %v", statusOf(resp), err)
	}

	var mine struct {
		Invites []contract.Invite `json:"invites"`
	}
	resp, err = inviteeAPI.do(http.MethodGet, "/v1/me/invites", nil, &mine)
	if err != nil || resp.StatusCode != http.StatusOK || len(mine.Invites) != 1 || mine.Invites[0].ID != created.ID {
		t.Fatalf("invitee %d %v %+v", statusOf(resp), err, mine.Invites)
	}
	resp, err = ownerAPI.do(http.MethodGet, "/v1/me/invites", nil, &mine)
	if err != nil || resp.StatusCode != http.StatusOK || len(mine.Invites) != 0 {
		t.Fatalf("owner mine %d %v %+v", statusOf(resp), err, mine.Invites)
	}

	resp, err = memberAPI.do(http.MethodDelete, "/v1/groups/"+group.ID+"/invites/"+created.ID, nil, nil)
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("member revoke %d %v", statusOf(resp), err)
	}
	resp, err = outsiderAPI.do(http.MethodDelete, "/v1/groups/"+group.ID+"/invites/"+created.ID, nil, nil)
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("outsider revoke %d %v", statusOf(resp), err)
	}
	resp, err = ownerAPI.do(http.MethodDelete, "/v1/groups/"+group.ID+"/invites/00000000-0000-4000-8000-000000000099", nil, nil)
	if err != nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing %d %v", statusOf(resp), err)
	}
	resp, err = ownerAPI.do(http.MethodDelete, "/v1/groups/"+group.ID+"/invites/"+created.ID, nil, nil)
	if err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("owner revoke %d %v", statusOf(resp), err)
	}
	resp, err = ownerAPI.do(http.MethodGet, "/v1/groups/"+group.ID+"/invites", nil, &listed)
	if err != nil || resp.StatusCode != http.StatusOK || len(listed.Invites) != 0 {
		t.Fatalf("after revoke %+v %d %v", listed.Invites, statusOf(resp), err)
	}
	resp, err = inviteeAPI.do(http.MethodGet, "/v1/me/invites", nil, &mine)
	if err != nil || resp.StatusCode != http.StatusOK || len(mine.Invites) != 0 {
		t.Fatalf("invitee after revoke %+v", mine.Invites)
	}

	var accepted contract.Invite
	resp, err = ownerAPI.do(http.MethodPost, "/v1/groups/"+group.ID+"/invites", map[string]string{
		"name": "Mira", "realm": "Everlook", "battletag": "Invitee#4",
	}, &accepted)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatal(statusOf(resp), err)
	}
	resp, err = inviteeAPI.do(http.MethodPost, "/v1/invites/"+accepted.ID+"/accept", map[string]string{}, nil)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("accept %d %v", statusOf(resp), err)
	}
	resp, err = ownerAPI.do(http.MethodDelete, "/v1/groups/"+group.ID+"/invites/"+accepted.ID, nil, nil)
	if err != nil || resp.StatusCode != http.StatusConflict {
		t.Fatalf("revoke accepted %d %v", statusOf(resp), err)
	}
}
