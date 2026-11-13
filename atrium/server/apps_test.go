package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The last app contact outlives a restart, and health says nothing about its age before there is one.
func TestTheLastAppContactIsKeptAcrossRestarts(t *testing.T) {
	ha := fakeHA(t)
	defer ha.Close()
	path := filepath.Join(t.TempDir(), "apps.json")

	fresh := gateway(ha, Identity{Login: "kid@example.com", Device: "phone"})
	fresh.appsPath = path
	fresh.mu.Lock()
	seen := fresh.appSeen
	fresh.mu.Unlock()
	if !seen.IsZero() {
		t.Fatal("a fresh add-on has a contact already")
	}
	fresh.appContact("kid@example.com (phone)")

	restarted := gateway(ha, Identity{})
	restarted.appsPath = path
	restarted.loadAppContact()
	restarted.mu.Lock()
	at, who := restarted.appSeen, restarted.appWho
	restarted.mu.Unlock()
	if who != "kid@example.com (phone)" || time.Since(at) > time.Minute {
		t.Errorf("after a restart the last contact is %q at %v", who, at)
	}

	// No contact: health leaves the age out rather than saying 0.
	var h healthReport
	if b, _ := json.Marshal(h); strings.Contains(string(b), "ageSeconds\":0,\"who") || strings.Contains(string(b), `"apps":{"ageSeconds"`) {
		t.Errorf("health with no contact: %s", b)
	}
}
