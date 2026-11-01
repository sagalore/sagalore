package savedvars

import "testing"

func TestParsePendingInvites(t *testing.T) {
	body := `SagaloreDB = {
  ["pending_invites"] = {
    {
      ["action"] = "accept",
      ["id"] = "fca516d2-50d1-46bf-83b2-e82a53e50768",
      ["at"] = 1761432000,
    },
    {
      ["action"] = "decline",
      ["id"] = "91a0d156-c6e3-4ff7-ad70-eeb275bd6554",
      ["at"] = 1761432100,
    },
  },
}
`
	got := ParsePendingInvites("/tmp/Sagalore.lua", body)
	if len(got) != 2 {
		t.Fatalf("got %d invites, want 2: %+v", len(got), got)
	}
	if got[0].Action != "accept" || got[0].ID != "fca516d2-50d1-46bf-83b2-e82a53e50768" || got[0].At != 1761432000 {
		t.Fatalf("first: %+v", got[0])
	}
	if got[1].Action != "decline" || got[1].ID != "91a0d156-c6e3-4ff7-ad70-eeb275bd6554" {
		t.Fatalf("second: %+v", got[1])
	}
}

func TestParsePendingInvitesEmpty(t *testing.T) {
	if got := ParsePendingInvites("x", `SagaloreDB = { ["completions"] = {} }`); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}
