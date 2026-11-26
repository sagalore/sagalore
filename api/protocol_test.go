package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sagalore/sagalore/internal/contract"
)

func TestProtocolGateCurrentAndPrevious(t *testing.T) {
	srv := httptest.NewServer((&Server{store: NewStore(testPool), adminToken: "admin"}).Handler())
	t.Cleanup(srv.Close)

	// N and N-1 pass the gate and fail auth, which proves the request was
	// not rejected as an upgrade. Below min and above max are 426.
	cases := []struct {
		header string
		want   int
		code   string
	}{
		{header: "", want: http.StatusUnauthorized},
		{header: "1", want: http.StatusUnauthorized},
		{header: "2", want: http.StatusUnauthorized},
		{header: "0", want: http.StatusUpgradeRequired, code: "upgrade_required"},
		{header: "3", want: http.StatusUpgradeRequired, code: "protocol_mismatch"},
		{header: "nope", want: http.StatusBadRequest, code: "invalid_protocol"},
	}
	for _, tc := range cases {
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/sync", nil)
		if err != nil {
			t.Fatal(err)
		}
		if tc.header != "" {
			req.Header.Set(contract.ProtocolHeader, tc.header)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Fatalf("header %q: status %d body %s", tc.header, resp.StatusCode, raw)
		}
		if tc.code != "" && !strings.Contains(string(raw), tc.code) {
			t.Fatalf("header %q: body %s", tc.header, raw)
		}
		if tc.want == http.StatusUpgradeRequired && !strings.Contains(string(raw), "protocol") {
			t.Fatalf("header %q: upgrade body %s", tc.header, raw)
		}
	}

	// Login stays available without a protocol header.
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/auth/dev", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusUpgradeRequired {
		t.Fatal("auth must not require a protocol header")
	}
}

func TestSyncAdvertisesProtocol(t *testing.T) {
	resetDB(t)
	srv := httptest.NewServer((&Server{store: NewStore(testPool), devToken: "dev-secret"}).Handler())
	t.Cleanup(srv.Close)
	_, token := devSession(t, "bnet-proto", "Proto#1", "Proto")
	for _, header := range []string{"1", "2"} {
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/sync", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set(contract.ProtocolHeader, header)
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var sync contract.SyncResponse
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("header %s: %d %s", header, resp.StatusCode, raw)
		}
		if err := json.Unmarshal(raw, &sync); err != nil {
			t.Fatal(err)
		}
		info := contract.CurrentProtocol()
		if sync.Protocol != info {
			t.Fatalf("header %s: protocol %+v", header, sync.Protocol)
		}
	}
}
