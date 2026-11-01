package notify

import "testing"

func TestCleanCollapsesLinesAndCaps(t *testing.T) {
	if got := clean("Ny saga: X\n— skriv /reload"); got != "Ny saga: X — skriv /reload" {
		t.Fatal(got)
	}
	long := make([]rune, 300)
	for i := range long {
		long[i] = 'æ'
	}
	if n := len([]rune(clean(string(long)))); n != 240 {
		t.Fatal(n)
	}
}
