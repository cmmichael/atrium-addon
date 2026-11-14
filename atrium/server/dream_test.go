package main

import (
	"strings"
	"testing"
	"time"
)

func TestDreamingBringsTheMemoryToTheConsolidatedFacts(t *testing.T) {
	items := []memItem{{UID: "1", Summary: "Bins go out on Wednesday evening."}, {UID: "2", Summary: "The bins go out Wednesdays."}, {UID: "3", Summary: "We are away until Sunday 8 November."}}
	add, remove, ok, _ := planDream(items, []string{"Bins go out on Wednesday evening.", "  The dog is called   Bo. ", "the dog is called bo."})
	if !ok || len(add) != 1 || add[0] != "The dog is called Bo." || len(remove) != 2 || remove[0].UID != "2" || remove[1].UID != "3" {
		t.Errorf("add %q remove %+v ok %v", add, remove, ok)
	}
	// Never wiped, never mostly dropped.
	if _, _, ok, why := planDream(items, nil); ok || !strings.Contains(why, "empty") {
		t.Errorf("an empty answer was taken: %v %s", ok, why)
	}
	many := make([]memItem, 10)
	for i := range many {
		many[i] = memItem{UID: string(rune('a' + i)), Summary: "Fact " + string(rune('A'+i))}
	}
	if _, _, ok, _ := planDream(many, []string{"Fact A", "Fact B", "Fact C"}); ok {
		t.Error("cutting ten memories to three was taken")
	}
	if _, _, ok, _ := planDream(nil, []string{"New fact."}); !ok {
		t.Error("a first fact on an empty list was refused")
	}
}

func TestDreamingAsksWithTheMemoryAndTheDay(t *testing.T) {
	now := time.Date(2026, 11, 15, 3, 30, 0, 0, time.UTC)
	s := dreamInstructions([]string{"Bins go out on Wednesday evening."}, []assistTurn{{Time: now.Add(-5 * time.Hour), Question: "remember we're away until Sunday", Answer: "Saved."}}, now)
	for _, want := range []string{"Today is Sunday 15 November 2026", "- Bins go out on Wednesday evening.", "Asked: remember we're away until Sunday", "Answered: Saved.", "never invent"} {
		if !strings.Contains(s, want) {
			t.Errorf("instructions lack %q:\n%s", want, s)
		}
	}
}
