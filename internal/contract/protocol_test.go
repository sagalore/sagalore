package contract

import "testing"

func TestCheckProtocolAcceptsCurrentAndPrevious(t *testing.T) {
	info := CurrentProtocol()
	if info.Version != ProtocolVersion || info.Max != ProtocolVersion || info.Min != MinProtocolVersion {
		t.Fatalf("%+v", info)
	}
	if info.Min != info.Version-1 && !(info.Version == 1 && info.Min == 1) {
		t.Fatalf("server should accept N and N-1, got %+v", info)
	}
	for _, h := range []string{"", "1", "2"} {
		got := CheckProtocol(h)
		if !got.OK || got.TooOld || got.TooNew || got.Invalid {
			t.Fatalf("header %q: %+v", h, got)
		}
	}
	old := CheckProtocol("0")
	if old.OK || !old.TooOld || old.Client != 0 {
		t.Fatalf("below min: %+v", old)
	}
	newer := CheckProtocol("3")
	if newer.OK || !newer.TooNew || newer.Client != 3 {
		t.Fatalf("above max: %+v", newer)
	}
	bad := CheckProtocol("nope")
	if !bad.Invalid || bad.OK {
		t.Fatalf("invalid: %+v", bad)
	}
}
