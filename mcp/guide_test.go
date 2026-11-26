package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEveryToolHasAnExample(t *testing.T) {
	seen := map[string]bool{}
	for _, tool := range tools() {
		if seen[tool.Name] {
			t.Fatalf("duplicate tool %s", tool.Name)
		}
		seen[tool.Name] = true
		if !strings.Contains(tool.Description, "Example") {
			t.Fatalf("%s description has no example:\n%s", tool.Name, tool.Description)
		}
	}
	for _, name := range []string{"how_it_works", "get_download_instructions", "create_saga", "list_sagas", "list_groups", "list_characters"} {
		if !seen[name] {
			t.Fatalf("missing tool %s", name)
		}
	}
}

func TestHowItWorksCoversInstallCreateAndReload(t *testing.T) {
	tool, ok := findTool("how_it_works")
	if !ok {
		t.Fatal("missing how_it_works")
	}
	text, err := tool.Call(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"addon", "companion", "Battle.net", "/reload", "create_saga", "go", "talk", "item", "kill", "emote", "sequence"} {
		if !strings.Contains(text, want) {
			t.Fatalf("guide missing %q\n%s", want, text)
		}
	}
	if strings.Contains(strings.ToLower(text), "create_character") {
		t.Fatal("guide should not offer hand-written characters")
	}
}

func TestCreateSagaPostsWithoutCreatedBy(t *testing.T) {
	var gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/sagas" || r.Method != http.MethodPost {
			t.Fatalf("%s %s", r.Method, r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"saga-1","title":"An evening in Goldshire"}`))
	}))
	defer srv.Close()
	SetAPI(API{BaseURL: srv.URL, Token: "gst_test", HTTP: srv.Client()})
	t.Cleanup(func() { SetAPI(APIFromEnv()) })

	tool, _ := findTool("create_saga")
	out, err := tool.Call(context.Background(), []byte(`{
		"title":"An evening in Goldshire",
		"description":"Visit Lion's Pride Inn.",
		"party":false,
		"created_by":"should-not-send",
		"objectives":[{"id":"inn","type":"go","text":"Stand in Goldshire","zone":"Elwynn Forest"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer gst_test" {
		t.Fatalf("auth %q", gotAuth)
	}
	if strings.Contains(gotBody, "created_by") || strings.Contains(gotBody, "should-not-send") {
		t.Fatalf("body %s", gotBody)
	}
	if !strings.Contains(out, "saga-1") {
		t.Fatal(out)
	}
}

func TestSagaToolDescriptionsNameItemRules(t *testing.T) {
	create, ok := findTool("create_saga")
	if !ok {
		t.Fatal("missing create_saga")
	}
	for _, want := range []string{"item_id is required", `memory "hold"`, "show_count false", "image_url"} {
		if !strings.Contains(create.Description, want) {
			t.Fatalf("create_saga missing %q", want)
		}
	}
	update, ok := findTool("update_saga")
	if !ok {
		t.Fatal("missing update_saga")
	}
	for _, want := range []string{"item_id", `memory "hold"`, "changes", "image_url"} {
		if !strings.Contains(update.Description, want) {
			t.Fatalf("update_saga missing %q", want)
		}
	}
	props, _ := update.Schema["properties"].(map[string]any)
	if props["changes"] == nil || props["image_url"] == nil {
		t.Fatalf("update_saga schema %+v", props)
	}
	get, _ := findTool("get_saga")
	if !strings.Contains(get.Description, "JSON") {
		t.Fatal(get.Description)
	}
}

func TestCreateSagaForwardsImageURL(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"saga-1"}`))
	}))
	defer srv.Close()
	SetAPI(API{BaseURL: srv.URL, Token: "gst_test", HTTP: srv.Client()})
	t.Cleanup(func() { SetAPI(APIFromEnv()) })

	tool, _ := findTool("create_saga")
	if _, err := tool.Call(context.Background(), []byte(`{
		"title":"Bread",
		"description":"Hold a loaf.",
		"party":false,
		"image_url":"https://cdn.example/cover.png",
		"objectives":[{"id":"loaf","type":"item","text":"Have bread","item_id":4540,"show_count":false}]
	}`)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotBody, "https://cdn.example/cover.png") || !strings.Contains(gotBody, `"show_count":false`) {
		t.Fatal(gotBody)
	}
}

func TestListGroupsNeedsToken(t *testing.T) {
	SetAPI(API{BaseURL: "http://127.0.0.1:1", Token: ""})
	t.Cleanup(func() { SetAPI(APIFromEnv()) })
	tool, _ := findTool("list_groups")
	_, err := tool.Call(context.Background(), []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "SAGALORE_TOKEN") {
		t.Fatal(err)
	}
}

func TestDownloadInstructions(t *testing.T) {
	tool, _ := findTool("get_download_instructions")
	text, err := tool.Call(context.Background(), []byte(`{"os":"macos","arch":"arm64"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "sagalore-darwin-arm64.zip") || !strings.Contains(text, "/reload") {
		t.Fatal(text)
	}
	_, err = tool.Call(context.Background(), []byte(`{"os":"windows","arch":"arm64"}`))
	if err == nil {
		t.Fatal("windows arm64 should be rejected")
	}
}

func TestHowItWorksOverStdio(t *testing.T) {
	in := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"how_it_works","arguments":{}}}`,
	}, "\n") + "\n")
	var out bytes.Buffer
	if err := Serve(in, &out); err != nil {
		t.Fatal(err)
	}
	msgs := decodeFrames(t, &out)
	if len(msgs) != 3 {
		t.Fatalf("frames %d", len(msgs))
	}
	var listed struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(msgs[1], &listed); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range listed.Result.Tools {
		if tool.Name == "create_saga" && strings.Contains(tool.Description, "Example") && strings.Contains(tool.Description, "sequence") {
			found = true
		}
	}
	if !found {
		t.Fatal("create_saga missing from tools/list")
	}
	var called struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(msgs[2], &called); err != nil {
		t.Fatal(err)
	}
	if called.Result.IsError || len(called.Result.Content) == 0 || !strings.Contains(called.Result.Content[0].Text, "/reload") {
		t.Fatalf("%s", msgs[2])
	}
}

func decodeFrames(t *testing.T, r io.Reader) [][]byte {
	t.Helper()
	br := bufio.NewReader(r)
	var out [][]byte
	for {
		msg, err := readMessage(br)
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, msg)
	}
}
