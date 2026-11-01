package savedvars

import "testing"

func TestParseResets(t *testing.T) {
	body := `SagaloreDB = {
  ["completions"] = {
  },
  ["pending_resets"] = {
    {
      ["all"] = true,
      ["at"] = 1761312000,
    },
    {
      ["saga_id"] = "68a6b8dc-8cbf-4b36-9db0-dff4e6d605a0",
      ["at"] = 1761312100,
    },
  },
  ["objectives"] = {
  },
}
`
	got := ParseResets("/tmp/Sagalore.lua", body)
	if len(got) != 2 {
		t.Fatalf("got %d resets, want 2: %+v", len(got), got)
	}
	if !got[0].All || got[0].At != 1761312000 {
		t.Fatalf("first: %+v", got[0])
	}
	if got[1].QuestID != "68a6b8dc-8cbf-4b36-9db0-dff4e6d605a0" || got[1].At != 1761312100 {
		t.Fatalf("second: %+v", got[1])
	}
}

func TestParseResetsEmpty(t *testing.T) {
	if got := ParseResets("x", `SagaloreDB = { ["completions"] = {} }`); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}
