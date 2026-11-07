package main

import "testing"

func TestClipTitles(t *testing.T) {
	for title, want := range map[string]Clip{
		"11/07/26 09:35:58 9s Object Detection - Person": {Title: "Person", When: "11/07/26 09:35:58", Seconds: 9},
		"11/07/26 08:24:01 12s Ring Event":               {Title: "Ring", When: "11/07/26 08:24:01", Seconds: 12},
		"something else":                                 {Title: "something else"},
	} {
		c := parseClip("id", title, "")
		if c.Title != want.Title || c.When != want.When || c.Seconds != want.Seconds {
			t.Errorf("%q → %+v", title, c)
		}
	}
}
