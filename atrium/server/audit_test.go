package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// What an app sends stays on one log line.
func TestAppTextCannotForgeLogLines(t *testing.T) {
	if got := oneLine("connected\n2026/11/14 21:00:00 owner@example.com asked\r to update\x00"); strings.ContainsAny(got, "\n\r\x00") {
		t.Errorf("oneLine kept a control character: %q", got)
	}
}

// A line longer than any buffer does not hide the turns after it.
func TestTheAssistLogReadsPastALongLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "assist.jsonl")
	long := `{"question":"` + strings.Repeat("x", 300<<10) + `"}`
	os.WriteFile(path, []byte(`{"question":"first"}`+"\n"+long+"\n"+`{"question":"after"}`+"\n"), 0o600)
	turns, err := (&assistLog{path: path}).newest(0)
	if err != nil || len(turns) != 3 || turns[0].Question != "after" {
		t.Errorf("read %d turns (%v), newest %q", len(turns), err, firstQuestion(turns))
	}
}

func firstQuestion(t []assistTurn) string {
	if len(t) == 0 {
		return ""
	}
	return t[0].Question
}

// Registering is bounded: a hundred phones at most (the one heard from longest ago makes room), twenty live
// activities a phone, and no tokens longer than Apple's.
func TestPhoneRegistrationIsBounded(t *testing.T) {
	a := &alerts{path: filepath.Join(t.TempDir(), "devices.json")}
	type reg = struct {
		Token      string            `json:"token"`
		LiveStart  string            `json:"liveStart"`
		Model      string            `json:"model"`
		Env        string            `json:"env"`
		Activities map[string]string `json:"activities"`
	}
	for i := 0; i < maxDevices+20; i++ {
		a.register(Identity{Login: fmt.Sprint("p", i), Device: "d"}, reg{Token: fmt.Sprint("t", i)})
	}
	if len(a.devices) != maxDevices || a.devices[len(a.devices)-1].Login != fmt.Sprint("p", maxDevices+19) {
		t.Errorf("%d devices kept, last %s", len(a.devices), a.devices[len(a.devices)-1].Login)
	}
	acts := map[string]string{}
	for i := 0; i < 50; i++ {
		acts[fmt.Sprint("alert", i)] = "tok"
	}
	d := a.register(Identity{Login: "p119", Device: "d"}, reg{Token: "t119", Activities: acts})
	if len(d.Activities) > maxActivities {
		t.Errorf("%d activities kept", len(d.Activities))
	}
	d = a.register(Identity{Login: "big", Device: "d"}, reg{Token: strings.Repeat("a", 10000)})
	if d.Token != "" {
		t.Error("a 10 kB token was kept")
	}
	if data, err := os.ReadFile(a.path); err != nil || !strings.Contains(string(data), "p119") {
		t.Errorf("the phones were not saved: %v", err)
	}
}

// The configuration is the owner's and administrators': a guest may not read it, and the set-up page's Tailscale
// sign-in link is not shown to a listed add-on.
func TestTheConfigurationAndSignInLinkAreForAdministrators(t *testing.T) {
	ha := fakeHA(t)
	defer ha.Close()
	if code, _ := get(t, gateway(ha, Identity{Login: "guest@example.com"}), "/atrium/config"); code != 403 {
		t.Errorf("a guest read the configuration: %d", code)
	}
	if code, _ := get(t, gateway(ha, Identity{Login: "owner@example.com", Owner: true}), "/atrium/config"); code != 200 {
		t.Errorf("the owner could not read the configuration: %d", code)
	}
	g := gateway(ha, Identity{Login: "d7e97e69_claudecode", Addon: true})
	g.setup = Setup{State: "needs-login", AuthURL: "https://login.tailscale.com/a/secret"}
	if _, body := get(t, g, "/atrium/setup"); body["authURL"] != nil {
		t.Errorf("a listed add-on saw the sign-in link: %v", body["authURL"])
	}
	g = gateway(ha, Identity{Login: "owner@example.com", Owner: true})
	g.setup = Setup{State: "needs-login", AuthURL: "https://login.tailscale.com/a/secret"}
	if _, body := get(t, g, "/atrium/setup"); body["authURL"] == nil {
		t.Error("the owner did not see the sign-in link")
	}
}
