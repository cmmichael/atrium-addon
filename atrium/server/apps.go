package main

import (
	"encoding/json"
	"log"
	"os"
	"time"
)

// The last request from an app, kept in /data so health can show a long gap across add-on restarts and updates:
// an app that cannot get through cannot say so, and a restart must not make two silent days look like none.

// appSaveEvery limits the writes: a phone makes many requests a minute.
const appSaveEvery = time.Minute

type appContactFile struct {
	At  time.Time `json:"at"`
	Who string    `json:"who"`
}

// appContact notes a request from an app now, and writes it down at most once a minute.
func (g *Gateway) appContact(who string) {
	now := time.Now()
	g.mu.Lock()
	g.appSeen, g.appWho = now, who
	// A new person is written at once, so health never names the wrong one for a minute.
	save := g.appsPath != "" && (now.Sub(g.appSaved) >= appSaveEvery || who != g.appSavedBy)
	if save {
		g.appSaved, g.appSavedBy = now, who
	}
	path := g.appsPath
	g.mu.Unlock()
	if save {
		writeAppContact(path, now, who)
	}
}

// saveAppContact writes the last contact down if it is newer than what the file has: on a clean stop, so the
// once-a-minute limit does not lose the last seconds before a restart.
func (g *Gateway) saveAppContact() {
	g.mu.Lock()
	at, who, path := g.appSeen, g.appWho, g.appsPath
	newer := path != "" && !at.IsZero() && at.After(g.appSaved)
	if newer {
		g.appSaved, g.appSavedBy = at, who
	}
	g.mu.Unlock()
	if newer {
		writeAppContact(path, at, who)
	}
}

func writeAppContact(path string, at time.Time, who string) {
	data, _ := json.Marshal(appContactFile{At: at, Who: who})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		log.Printf("apps: could not keep the last contact: %v", err)
	}
}

// loadAppContact reads the last contact kept by an earlier run.
func (g *Gateway) loadAppContact() {
	data, err := os.ReadFile(g.appsPath)
	if err != nil {
		return
	}
	var f appContactFile
	if json.Unmarshal(data, &f) != nil || f.At.IsZero() {
		return
	}
	g.mu.Lock()
	g.appSeen, g.appWho, g.appSaved, g.appSavedBy = f.At, f.Who, f.At, f.Who
	g.mu.Unlock()
}
