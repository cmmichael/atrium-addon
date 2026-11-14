package main

// AI Assist's log, kept on the Home Assistant side: every question asked in the apps (iPhone, iPad, web), what the
// assistant did on the way (its thinking, the tools it called and how they went), the answer and how long it took.
// The apps send each turn as it ends (POST /atrium/assist); the add-on appends it to /data/assist.jsonl, which
// survives restarts and updates, and writes one line to its own log, so the conversations can be read back to make
// the assistant better. GET /atrium/assist lists the newest, for the owner and administrators.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type assistStep struct {
	Kind    string `json:"kind"` // thinking or tool
	Text    string `json:"text"`
	Tool    string `json:"tool,omitempty"`
	Outcome string `json:"outcome,omitempty"` // done, failed
}

type assistTurn struct {
	Time         time.Time    `json:"time"`
	Login        string       `json:"login"`  // from the connection, not the app
	Device       string       `json:"device"` // the same
	App          string       `json:"app,omitempty"`
	Question     string       `json:"question"`
	Answer       string       `json:"answer"`
	Failed       bool         `json:"failed,omitempty"`
	ResponseType string       `json:"responseType,omitempty"`
	Steps        []assistStep `json:"steps,omitempty"`
	Took         float64      `json:"took,omitempty"`     // seconds, question to answer
	Pipeline     bool         `json:"pipeline,omitempty"` // answered through an Assist pipeline (steps known)
	Spoken       bool         `json:"spoken,omitempty"`
	Conversation string       `json:"conversation,omitempty"`
}

type assistLog struct {
	mu   sync.Mutex
	path string
	max  int64 // bytes before the oldest half is dropped
}

const assistTurnMax = 64 << 10

func (g *Gateway) serveAssist(w http.ResponseWriter, r *http.Request, id Identity) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case "POST":
		data, err := io.ReadAll(io.LimitReader(r.Body, assistTurnMax+1))
		if err != nil || len(data) > assistTurnMax {
			http.Error(w, "a turn is at most 64 KiB", http.StatusRequestEntityTooLarge)
			return
		}
		var t assistTurn
		if err := json.Unmarshal(data, &t); err != nil || strings.TrimSpace(t.Question) == "" {
			http.Error(w, "not a turn", http.StatusBadRequest)
			return
		}
		t.Login, t.Device = id.Login, id.Device
		if t.Time.IsZero() || t.Time.After(time.Now().Add(time.Minute)) {
			t.Time = time.Now()
		}
		if err := g.assist.add(t); err != nil {
			log.Printf("assist: could not keep a turn: %v", err)
			http.Error(w, "could not keep it", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	case "GET":
		if !g.mayConfigure(id) {
			http.Error(w, "only the owner and administrators may read AI Assist's log", http.StatusForbidden)
			return
		}
		n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		turns, err := g.assist.newest(n)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(turns)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "GET or POST", http.StatusMethodNotAllowed)
	}
}

func (l *assistLog) add(t assistTurn) error {
	line, err := json.Marshal(t)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	size := int64(0)
	if st, e := f.Stat(); e == nil {
		size = st.Size()
	}
	f.Close()
	if err != nil {
		return err
	}
	if l.max > 0 && size > l.max {
		l.trim()
	}
	log.Print(t.summary())
	return nil
}

// trim keeps the newer half of the file, whole lines only. Called with the lock held.
func (l *assistLog) trim() {
	data, err := os.ReadFile(l.path)
	if err != nil {
		return
	}
	cut := len(data) / 2
	if i := bytes.IndexByte(data[cut:], '\n'); i >= 0 {
		cut += i + 1
	}
	tmp := l.path + ".tmp"
	if os.WriteFile(tmp, data[cut:], 0o600) == nil {
		os.Rename(tmp, l.path)
	}
}

// newest returns up to n turns (all of them for n <= 0), newest first.
func (l *assistLog) newest(n int) ([]assistTurn, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.Open(l.path)
	if os.IsNotExist(err) {
		return []assistTurn{}, nil
	} else if err != nil {
		return nil, err
	}
	defer f.Close()
	var all []assistTurn
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), assistTurnMax*2)
	for sc.Scan() {
		var t assistTurn
		if json.Unmarshal(sc.Bytes(), &t) == nil {
			all = append(all, t)
		}
	}
	out := make([]assistTurn, 0, len(all))
	for i := len(all) - 1; i >= 0 && (n <= 0 || len(out) < n); i-- {
		out = append(out, all[i])
	}
	return out, nil
}

// summary is the turn's line in the add-on's log: who, how long, what it did, the question and the start of the answer.
func (t assistTurn) summary() string {
	var tools []string
	thought := false
	for _, s := range t.Steps {
		switch s.Kind {
		case "tool":
			mark := "✓"
			if s.Outcome == "failed" {
				mark = "✗"
			}
			tools = append(tools, s.Text+" "+mark)
		case "thinking":
			thought = true
		}
	}
	how := fmt.Sprintf("%.1f s", t.Took)
	if thought {
		how += " · thought"
	}
	if len(tools) > 0 {
		how += " · " + strings.Join(tools, ", ")
	}
	if !t.Pipeline {
		how += " · no steps (conversation/process)"
	}
	outcome := "→"
	if t.Failed {
		outcome = "→ failed:"
	}
	return fmt.Sprintf("assist %s (%s) %s · %q %s %q", t.Login, t.Device, how, clip(t.Question, 160), outcome, clip(t.Answer, 200))
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
