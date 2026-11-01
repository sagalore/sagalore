package main

import "testing"

func TestDisplayActor(t *testing.T) {
	cases := []struct {
		name, email, tag, want string
	}{
		{"Magnus", "magnus@sparkel.io", "Magnus#2231", "Magnus"},
		{"magnus@sparkel.io", "magnus@sparkel.io", "Magnus#2231", "Magnus"},
		{"", "magnus@sparkel.io", "", "magnus"},
		{"", "", "Magnus#2231", "Magnus"},
		{"", "", "", ""},
	}
	for _, c := range cases {
		if got := displayActor(c.name, c.email, c.tag); got != c.want {
			t.Fatalf("%q/%q/%q: got %q want %q", c.name, c.email, c.tag, got, c.want)
		}
	}
}
