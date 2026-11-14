package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAIAssistTurnsAreKeptOnTheHomeAssistantSide(t *testing.T) {
	ha := fakeHA(t)
	defer ha.Close()
	owner, guest := Identity{Login: "owner@example.com", Owner: true, Device: "owners-iphone"}, Identity{Login: "guest@example.com", Device: "guests-iphone"}
	g := gateway(ha, guest)
	path := filepath.Join(t.TempDir(), "assist.jsonl")
	g.assist = &assistLog{path: path, max: 8 << 20}
	post := func(body string) int {
		r := httptest.NewRequest("POST", "/atrium/assist", strings.NewReader(body))
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		return w.Code
	}
	turn := `{"question":"is the alarm armed?","answer":"I couldn't find an alarm panel.","responseType":"action_done","took":4.6,"pipeline":true,
		"steps":[{"kind":"thinking","text":"Look for the alarm."},{"kind":"tool","text":"Look at the home · alarm","tool":"GetLiveContext","outcome":"failed"}],"login":"someone-else@example.com"}`
	if code := post(turn); code != 202 {
		t.Fatalf("a guest's turn answered %d", code)
	}
	if code := post(`{"answer":"no question"}`); code != 400 {
		t.Errorf("a turn without a question answered %d", code)
	}
	// Kept on disk under the caller's own login, whatever the app said.
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"login":"guest@example.com"`) || strings.Contains(string(data), "someone-else") {
		t.Errorf("the file holds %s", data)
	}
	// One line in the add-on's log says what happened.
	var kept assistTurn
	json.Unmarshal([]byte(strings.TrimSpace(string(data))), &kept)
	if s := kept.summary(); !strings.Contains(s, "4.6 s · thought · Look at the home · alarm ✗") || !strings.Contains(s, `"is the alarm armed?"`) {
		t.Errorf("summary %s", s)
	}
	// Reading is for the owner and administrators, newest first.
	if code, _ := get(t, g, "/atrium/assist"); code != 403 {
		t.Errorf("a guest reading the log answered %d", code)
	}
	post(`{"question":"what lights are on","answer":"Nine.","took":2.3,"pipeline":true}`)
	g.Who = func(context.Context, string) (Identity, error) { return owner, nil }
	r := httptest.NewRequest("GET", "/atrium/assist?limit=1", nil)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	var turns []assistTurn
	json.Unmarshal(w.Body.Bytes(), &turns)
	if w.Code != 200 || len(turns) != 1 || turns[0].Question != "what lights are on" {
		t.Errorf("the owner's GET answered %d %s", w.Code, w.Body.String())
	}
}

func TestTheAIAssistLogDropsItsOldestHalfWhenFull(t *testing.T) {
	l := &assistLog{path: filepath.Join(t.TempDir(), "assist.jsonl"), max: 2000}
	for i := 0; i < 40; i++ {
		if err := l.add(assistTurn{Question: strings.Repeat("q", 40) + string(rune('A'+i%26)), Answer: "a"}); err != nil {
			t.Fatal(err)
		}
	}
	st, _ := os.Stat(l.path)
	turns, _ := l.newest(0)
	if st.Size() > 2000 || len(turns) == 0 || len(turns) >= 40 {
		t.Errorf("size %d, %d turns kept", st.Size(), len(turns))
	}
}
