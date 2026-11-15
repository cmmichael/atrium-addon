package main

import (
	"strings"
	"testing"
	"time"
)

func TestDreamingBringsTheMemoryToTheConsolidatedFacts(t *testing.T) {
	items := []memItem{{UID: "1", Summary: "Bins go out on Wednesday evening."}, {UID: "2", Summary: "The bins go out Wednesdays."}, {UID: "3", Summary: "We are away until Sunday 8 November."}}
	now := time.Date(2026, 11, 15, 3, 30, 0, 0, time.UTC)
	add, remove, _, ok, _ := planDream(items, []string{"Bins go out on Wednesday evening.", "  The dog is called   Bo. ", "the dog is called bo."}, now)
	if !ok || len(add) != 1 || add[0] != "The dog is called Bo." || len(remove) != 2 || remove[0].UID != "2" || remove[1].UID != "3" {
		t.Errorf("add %q remove %+v ok %v", add, remove, ok)
	}
	// Never wiped, never mostly dropped.
	if _, _, _, ok, why := planDream(items, nil, now); ok || !strings.Contains(why, "empty") {
		t.Errorf("an empty answer was taken: %v %s", ok, why)
	}
	many := make([]memItem, 10)
	for i := range many {
		many[i] = memItem{UID: string(rune('a' + i)), Summary: "Fact " + string(rune('A'+i))}
	}
	if _, _, _, ok, _ := planDream(many, []string{"Fact A", "Fact B", "Fact C"}, now); ok {
		t.Error("cutting ten memories to three was taken")
	}
	if _, _, _, ok, _ := planDream(nil, []string{"New fact."}, now); !ok {
		t.Error("a first fact on an empty list was refused")
	}
}

func TestDreamingAsksWithTheMemoryAndTheDay(t *testing.T) {
	now := time.Date(2026, 11, 15, 3, 30, 0, 0, time.UTC)
	s := dreamInstructions([]string{"Bins go out on Wednesday evening."}, []assistTurn{{Time: now.Add(-5 * time.Hour), Question: "remember we're away until Sunday", Answer: "Saved."}}, now)
	for _, want := range []string{"It is now Sunday 15 November 2026", "[Saturday 14 November 22:30] ", "left_out", "- Bins go out on Wednesday evening.", "Asked: remember we're away until Sunday", "Answered: Saved.", "never invent"} {
		if !strings.Contains(s, want) {
			t.Errorf("instructions lack %q:\n%s", want, s)
		}
	}
}

// What the first real night added: an event, a rejection written into the item, and one fact. Only the fact goes in.
func TestDreamingKeepsEventsStatesAndNotesOut(t *testing.T) {
	now := time.Date(2026, 11, 15, 3, 30, 0, 0, time.UTC)
	answer := []string{
		"The household started the Christmas Music scene on Saturday 14 November 2026 when asked to play Christmas music.",
		"Sam arrived home on Sunday 15 November 2026 at 6:04 PM is a live state and not kept as a fact.",
		"Sam is a household member.",
		"The lights went off at 23:10 today.",
		"The cleaner comes on Tuesdays at 10:00.",
		"We are away until Sunday 22 November.",
		"Bo is the dog. He sleeps in the den.",
	}
	add, _, refused, ok, _ := planDream(nil, answer, now)
	if !ok || strings.Join(add, "|") != "Sam is a household member.|The cleaner comes on Tuesdays at 10:00.|We are away until Sunday 22 November." {
		t.Errorf("added %q", add)
	}
	if len(refused) != 4 {
		t.Errorf("refused %q", refused)
	}
	// A first night adds three at most; later nights five.
	many := []string{"A is one.", "B is two.", "C is three.", "D is four.", "E is five.", "F is six."}
	if add, _, refused, _, _ := planDream(nil, many, now); len(add) != maxFirstFacts || len(refused) != len(many)-maxFirstFacts {
		t.Errorf("first night: added %d, refused %d", len(add), len(refused))
	}
	if add, _, _, _, _ := planDream([]memItem{{UID: "1", Summary: "Z is last."}}, append(many, "Z is last."), now); len(add) != maxNewFacts {
		t.Errorf("a later night added %d", len(add))
	}
	// A fact already on the list is never refused for its shape: it stays.
	if _, remove, _, ok, _ := planDream([]memItem{{UID: "1", Summary: "Party on 2 November at 19:00."}}, []string{"Party on 2 November at 19:00."}, now); !ok || len(remove) != 0 {
		t.Errorf("an existing item was dropped for its shape")
	}
	if got := factShape("New Year is on 1 January.", now); got != "" {
		t.Errorf("a date with no year was taken for a past one: %s", got)
	}
}
