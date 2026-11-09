package house

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func j(s string) any {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		panic(err)
	}
	return v
}

func list(s string) []map[string]any {
	var out []map[string]any
	for _, v := range j(s).([]any) {
		out = append(out, v.(map[string]any))
	}
	return out
}

// A small Home Assistant: a few areas, a fridge that is not a thermostat, a car that is not a door.
func smallHome() Dump {
	d := Dump{Name: "Test Home", States: map[string]*State{}}
	d.Floors = list(`[{"floor_id":"up","name":"Upstairs","level":1},{"floor_id":"down","name":"Downstairs","level":0}]`)
	d.Areas = list(`[{"area_id":"kitchen","name":"Kitchen","floor_id":"down"},{"area_id":"bed","name":"Bedroom","floor_id":"up"},{"area_id":"shed","name":"Shed","floor_id":null},{"area_id":"empty","name":"Empty","floor_id":"down"}]`)
	d.Devices = list(`[{"id":"dev1","area_id":"kitchen"},{"id":"car","area_id":"shed"},{"id":"sonos-kitchen","name":"Kitchen","area_id":"kitchen"},{"id":"ma-kitchen","name":"Kitchen"},
		{"id":"frame","name":"Bedroom Frame","area_id":"bed"},{"id":"frame2","name":"Bedroom Frame","area_id":"bed"},{"id":"gtv","name":"Kitchen TV Remote","area_id":"kitchen"}]`)
	reg := func(id, area, device, platform string, hidden bool) map[string]any {
		e := map[string]any{"entity_id": id, "platform": platform}
		if area != "" {
			e["area_id"] = area
		}
		if device != "" {
			e["device_id"] = device
		}
		if hidden {
			e["hidden_by"] = "user"
		}
		return e
	}
	d.Entities = []map[string]any{
		reg("light.kitchen_island", "", "dev1", "demo", false), reg("light.kitchen_group", "kitchen", "", "demo", false), reg("light.kitchen_hidden", "kitchen", "", "demo", true),
		reg("climate.fridge", "kitchen", "", "demo", false), reg("climate.downstairs", "kitchen", "", "demo", false), reg("cover.bed_shade", "bed", "", "demo", false),
		reg("lock.car", "", "car", "tesla_fleet", false), reg("light.shed", "shed", "", "demo", false), reg("scene.movie", "", "", "demo", false),
		reg("binary_sensor.smoke", "kitchen", "", "demo", false), reg("media_player.kitchen", "kitchen", "", "sonos", false),
		reg("switch.kitchen_kettle", "kitchen", "", "demo", false), reg("climate.hall", "", "", "demo", false),
		reg("sensor.shed_pipe_temperature", "shed", "", "demo", false),
		reg("light.kiosk_screen", "", "", "browser_mod", false), reg("camera.front", "", "bell", "unifiprotect", false),
		reg("media_player.front_speaker", "", "bell", "unifiprotect", false), reg("binary_sensor.front_doorbell", "", "bell", "unifiprotect", false),
		reg("binary_sensor.front_door", "", "bell", "demo", false), reg("scene.bed_night", "bed", "", "hue", false),
		reg("light.oven_light", "kitchen", "", "miele", false),
		map[string]any{"entity_id": "media_player.kitchen_ma", "platform": "music_assistant", "device_id": "ma-kitchen", "config_entry_id": "ma-entry"},
		// A Frame TV in the bedroom (its remote and its player on one device, known to two integrations), a Google TV
		// in the kitchen, and a receiver with no remote of its own.
		reg("media_player.bed_frame", "", "frame", "samsungtv", false), reg("remote.bed_frame", "", "frame", "samsungtv", false),
		reg("media_player.bed_frame_smart", "", "frame2", "samsungtv_smart", false), reg("remote.bed_frame_smart", "", "frame2", "samsungtv_smart", false),
		reg("media_player.kitchen_gtv", "", "gtv", "androidtv_remote", false), reg("remote.kitchen_gtv", "", "gtv", "androidtv_remote", false),
		reg("media_player.avr", "kitchen", "", "songpal", false), reg("script.matrix_ps5", "", "", "script", false), reg("select.matrix_out_1", "", "", "demo", false),
	}
	st := func(id, state, attrs string) {
		a, _ := j(attrs).(map[string]any)
		d.States[id] = &State{State: state, Attributes: a}
	}
	st("light.kitchen_island", "on", `{"friendly_name":"Kitchen Island lights"}`)
	st("light.kitchen_group", "off", `{"friendly_name":"Kitchen Main lights"}`)
	st("light.kitchen_hidden", "off", `{}`)
	st("climate.fridge", "cool", `{"friendly_name":"Fridge","min_temp":34,"max_temp":46}`)
	st("climate.downstairs", "cool", `{"friendly_name":"Kitchen Thermostat","min_temp":50,"max_temp":90}`)
	st("climate.hall", "heat", `{"friendly_name":"Hall Thermostat","min_temp":50,"max_temp":90}`)
	st("cover.bed_shade", "open", `{"friendly_name":"Bedroom Shade","device_class":"shade"}`)
	st("lock.car", "locked", `{}`)
	st("light.shed", "off", `{"friendly_name":"Shed light"}`)
	st("scene.movie", "2026-01-01T00:00:00+00:00", `{"friendly_name":"Movie"}`)
	st("binary_sensor.smoke", "off", `{"friendly_name":"Kitchen smoke","device_class":"smoke"}`)
	st("media_player.kitchen", "idle", `{"friendly_name":"Kitchen","device_class":"speaker"}`)
	st("switch.kitchen_kettle", "off", `{"friendly_name":"Kitchen Kettle"}`)
	st("sensor.shed_pipe_temperature", "18.5", `{"friendly_name":"Shed pipe","device_class":"temperature","unit_of_measurement":"°C"}`)
	st("light.kiosk_screen", "on", `{"friendly_name":"Kiosk screen"}`)
	st("light.oven_light", "off", `{"friendly_name":"Oven light"}`)
	st("media_player.kitchen_ma", "idle", `{"friendly_name":"Kitchen","device_class":"speaker"}`)
	st("camera.front", "idle", `{"friendly_name":"Front"}`)
	st("media_player.front_speaker", "idle", `{"friendly_name":"Front speaker","device_class":"speaker"}`)
	st("binary_sensor.front_doorbell", "off", `{"device_class":"occupancy"}`)
	st("binary_sensor.front_door", "off", `{"friendly_name":"Front door","device_class":"door"}`)
	st("scene.bed_night", "2026-01-02T00:00:00+00:00", `{"friendly_name":"Night"}`)
	st("weather.forecast_home", "sunny", `{}`)
	st("person.a", "home", `{}`)
	st("alarm_control_panel.house", "disarmed", `{}`)
	st("media_player.bed_frame", "off", `{"friendly_name":"Bedroom Frame","device_class":"tv","source_list":["TV","HDMI 1","Netflix"],"volume_level":0.2}`)
	st("remote.bed_frame", "on", `{}`)
	st("media_player.bed_frame_smart", "off", `{"friendly_name":"Bedroom Frame","device_class":"tv","source_list":["TV"]}`)
	st("remote.bed_frame_smart", "on", `{}`)
	st("media_player.kitchen_gtv", "on", `{"friendly_name":"Kitchen TV","device_class":"tv","source_list":["YouTube","Plex"]}`)
	st("remote.kitchen_gtv", "on", `{}`)
	st("media_player.avr", "on", `{"friendly_name":"Receiver","device_class":"receiver","source_list":["HDMI1","HDMI2"],"volume_level":0.4}`)
	st("script.matrix_ps5", "off", `{}`)
	st("select.matrix_out_1", "Apple TV", `{"options":["Apple TV","PS5"]}`)
	return d
}

// What the atrium-mapping skill would write for smallHome: a few overrides, and the screens only it can fill.
const smallMapping = `
version: 2
name: Cedar House
floors:
  - { id: up, rank: 1 }
  - { id: other, name: Outbuildings }
rooms:
  - id: kitchen
    rank: 1
    hide: [switch.kitchen_kettle]
    entities: [climate.hall]
  - id: bed
    name: Nora's Room
  - id: empty
    hide: true
  - id: unassigned
    hide: true
  - id: garden
    floor: other
    entities: [light.shed, light.missing]
  - id: shed
    temperature: none
    thermostat: none
hide: ["light.*_group", "integration:tesla_fleet"]
scenes:
  - { entity: scene.movie, name: "🎬 Film" }
home:
  weather: weather.forecast_home
  alarm: alarm_control_panel.house
  people: [person.a, person.gone]
security:
  hazards: [binary_sensor.smoke]
music:
  speakers: [media_player.kitchen]
names:
  light.kitchen_island: Island
`

func mapped(t *testing.T, text string) Mapping {
	t.Helper()
	m, err := ParseMapping([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func names(rooms []Room) []string {
	var out []string
	for _, r := range rooms {
		out = append(out, r.Name)
	}
	return out
}

func floorNames(fs []Floor) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Name)
	}
	return out
}

func eq(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %v, want %v", what, got, want)
	}
}

func TestTheRoomsAreDrawnFromHomeAssistantWithNoMappingAtAll(t *testing.T) {
	h, problems := Render(Mapping{}, smallHome(), Config{})
	eq(t, "problems", problems, []string(nil))
	eq(t, "name", h.Name, "Test Home")
	eq(t, "floors", floorNames(h.Floors), []string{"Downstairs", "Upstairs", "Elsewhere"}) // by level; the shed has no floor
	eq(t, "rooms", names(h.Rooms), []string{"Kitchen", "Bedroom", "Shed", "Unassigned"})   // every area with something to control, by floor
	eq(t, "hidden rooms", names(h.HiddenRooms), []string{"Empty"})                         // an area with nothing in it
	kitchen := h.Rooms[0]
	// Lights (one through its device's area), the kettle, the thermostat but not the fridge, the speaker; not the
	// hidden light, not the oven's own light.
	eq(t, "kitchen", kitchen.Entities, []string{"light.kitchen_island", "light.kitchen_group", "switch.kitchen_kettle", "climate.downstairs", "media_player.kitchen"})
	eq(t, "shed", h.Rooms[2].Entities, []string{"light.shed"})                    // the car's lock is not a door
	eq(t, "shed readout", h.Rooms[2].Temperature, "sensor.shed_pipe_temperature") // a plausible room temperature there
	eq(t, "unassigned", h.Rooms[3].Entities, []string{"climate.hall"})            // in no area, but shown
	eq(t, "island", h.Names["light.kitchen_island"], "Island lights")             // Home Assistant's name, shortened for the room
	// The custom screens are the add-on's own guesses until the file says otherwise.
	eq(t, "weather", h.Summary.Weather, "weather.forecast_home")
	eq(t, "people", h.Summary.People, []string{"person.a"})
	eq(t, "alarm", h.Summary.Alarm, "alarm_control_panel.house")
	eq(t, "hazards", h.Security.Hazards, []string{"binary_sensor.smoke"})
	eq(t, "entries", h.Security.Entries, []string{"binary_sensor.front_door"})
	eq(t, "camera for the door", h.Security.CameraFor, map[string]string{"binary_sensor.front_door": "camera.front"})
	eq(t, "doorbells", h.Security.Doorbells, []string{"camera.front"})
	eq(t, "speakers", h.Speakers, []string{"media_player.kitchen"}) // not the doorbell's own speaker, not the kiosk, not MA's copy
	if h.MusicAssistant == nil || h.MusicAssistant.Entry != "ma-entry" || h.MusicAssistant.Players["media_player.kitchen"] != "media_player.kitchen_ma" {
		t.Errorf("music assistant: %+v", h.MusicAssistant)
	}
	eq(t, "unassigned", h.Rooms[3].Entities, []string{"climate.hall"}) // the kiosk "light" and the camera's speaker are not controls
	// Most recently used first: the bedroom's Night ran on the 2nd, Movie on the 1st.
	eq(t, "scenes", h.Scenes, []SceneRef{{Entity: "scene.bed_night", Name: "Night", Room: "bed"}, {Entity: "scene.movie", Name: "Movie"}})
	if !(Mapping{}).Empty() || (Mapping{}).Custom() {
		t.Error("an empty mapping must say so")
	}
}

func TestTheMappingOverridesTheRoomsAndFillsTheScreens(t *testing.T) {
	h, problems := Render(mapped(t, smallMapping), smallHome(), Config{})
	eq(t, "name", h.Name, "Cedar House")
	eq(t, "floors", floorNames(h.Floors), []string{"Upstairs", "Downstairs", "Outbuildings"}) // ranked first; "other" renamed
	eq(t, "rooms", names(h.Rooms), []string{"Nora's Room", "Kitchen", "Garden", "Shed"})      // renamed; a room of the file's own; Empty and Unassigned hidden
	kitchen := h.Rooms[1]
	// The kettle hidden here, the group light hidden house-wide (glob), the hall thermostat borrowed.
	eq(t, "kitchen", kitchen.Entities, []string{"light.kitchen_island", "climate.hall", "climate.downstairs", "media_player.kitchen"})
	eq(t, "kitchen hidden", kitchen.Hidden, []string{"light.kitchen_group", "switch.kitchen_kettle"})
	eq(t, "garden", h.Rooms[2].Entities, []string{"light.shed"}) // borrowed; the missing light dropped and reported
	eq(t, "shed", h.Rooms[3].Entities, []string{"light.shed"})   // still in its own area too
	eq(t, "shed readout", h.Rooms[3].Temperature, "")            // none: not even the automatic pick
	eq(t, "island", h.Names["light.kitchen_island"], "Island")   // the mapping's name
	eq(t, "problems", problems, []string{"room garden: Home Assistant has no light.missing", "home: Home Assistant has no person.gone"})
	eq(t, "scenes", h.Scenes, []SceneRef{{Entity: "scene.movie", Name: "🎬 Film"}, {Entity: "scene.bed_night", Name: "Night", Room: "bed"}})
	eq(t, "entries", h.Security.Entries, []string{}) // the file's security section replaces the guess wholesale
	eq(t, "hazards", h.Security.Hazards, []string{"binary_sensor.smoke"})
	eq(t, "energy", h.Energy.Consumers, []string{}) // no energy section: an empty screen, which the apps hide
	eq(t, "speakers", h.Speakers, []string{"media_player.kitchen"})
	eq(t, "weather", h.Summary.Weather, "weather.forecast_home")
	eq(t, "people", h.Summary.People, []string{"person.a"})
	eq(t, "alarm", h.Summary.Alarm, "alarm_control_panel.house")
	for _, r := range h.Rooms {
		for _, id := range r.Entities {
			if smallHome().States[id] == nil {
				t.Errorf("%s shows %s, which has no state", r.Name, id)
			}
		}
	}
	// A new device in Home Assistant is in the house on the next render, with nothing written anywhere.
	d := smallHome()
	d.Entities = append(d.Entities, map[string]any{"entity_id": "fan.bed", "area_id": "bed", "platform": "demo"})
	d.States["fan.bed"] = &State{State: "off", Attributes: map[string]any{"friendly_name": "Bedroom Fan"}}
	h, _ = Render(mapped(t, smallMapping), d, Config{})
	eq(t, "new fan", h.Rooms[0].Entities, []string{"cover.bed_shade", "fan.bed"})
}

func TestTheMappingIsCheckedBeforeItIsUsed(t *testing.T) {
	for _, bad := range []string{
		"rooms:\n  - name: No id\n",
		"rooms:\n  - id: a\n  - id: a\n",
		"rooms:\n  - id: a\n    entities: [notanentity]\n",
		"rooms:\n  - id: a\n    hide: [notanentity]\n",
		"rooms:\n  - id: a\n    hide: maybe\n",
		"rooms:\n  - id: a\n    thermostat: sensor.x\n",
		"rooms:\n  - id: a\n    entities: [none]\n",
		"hide: [notanentity]\n",
		"hide: [\"integration:\"]\n",
		"version: 7\nrooms: []\n",
		"rooms: [\n",                // not YAML
		"rooms: []\nbedrooms: []\n", // an unknown key is a typo, not a feature
	} {
		if _, err := ParseMapping([]byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if m, err := ParseMapping(nil); err != nil || !m.Empty() {
		t.Errorf("an empty file: %v %+v", err, m)
	}
	// A version 1 file (rooms listed in full) still reads: its rooms are overrides that borrow what they list.
	if m, err := ParseMapping([]byte("version: 1\nrooms:\n  - id: den\n    entities: [light.a]\n")); err != nil || len(m.Rooms) != 1 {
		t.Errorf("a version 1 file: %v %+v", err, m)
	}
}

func TestTheConfigurationHidesRenamesMovesAndReorders(t *testing.T) {
	one, two := 1, 2
	c := Config{
		Name:   "Our House",
		Floors: map[string]FloorConfig{"outside": {Name: "Outside", Order: &two}, "down": {Order: &one}},
		Rooms: map[string]RoomConfig{
			"kitchen": {Entities: []string{"media_player.kitchen", "light.kitchen_group"}, Thermostat: "climate.hall"},
			"bed":     {Name: "Nora's Room", Hidden: false},
			"shed":    {Floor: "outside"},
			"empty":   {Order: &one},
		},
		Entities: map[string]EntityConfig{
			"light.kitchen_island":  {Hidden: true},
			"switch.kitchen_kettle": {Name: "Kettle plug"},
			"cover.bed_shade":       {Room: "kitchen"},
			"light.gone":            {Name: "A light that no longer exists"}, // dormant until it comes back
		},
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	h, _ := Render(Mapping{}, smallHome(), c)
	eq(t, "name", h.Name, "Our House")
	eq(t, "floors", floorNames(h.Floors), []string{"Downstairs", "Outside", "Upstairs", "Elsewhere"}) // ordered floors first, then by level
	eq(t, "rooms", names(h.Rooms), []string{"Kitchen", "Shed", "Unassigned"})                         // the bedroom's only control moved to the kitchen, so it is not a room any more
	eq(t, "kitchen", h.Rooms[0].Entities, []string{"media_player.kitchen", "light.kitchen_group", "switch.kitchen_kettle", "cover.bed_shade", "climate.hall", "climate.downstairs"})
	eq(t, "kettle", h.Names["switch.kitchen_kettle"], "Kettle plug")
	eq(t, "hall thermostat named for the kitchen", h.Names["climate.hall"], "Hall Thermostat")
	if _, dormant := h.Names["light.gone"]; dormant {
		t.Error("a dormant override leaked into the layout")
	}
	// The light comes back: its override applies again, nothing to do.
	d := smallHome()
	d.Entities = append(d.Entities, map[string]any{"entity_id": "light.gone", "area_id": "shed", "platform": "demo"})
	d.States["light.gone"] = &State{State: "on", Attributes: map[string]any{}}
	returned, _ := Render(Mapping{}, d, c)
	eq(t, "returned", returned.Names["light.gone"], "A light that no longer exists")
	// The owner's taps sit on top of the file: a rank from the file, an order from the owner, the owner wins.
	h, _ = Render(mapped(t, smallMapping), smallHome(), Config{Rooms: map[string]RoomConfig{"shed": {Order: &one}}})
	eq(t, "owner's order", names(h.Rooms), []string{"Nora's Room", "Kitchen", "Shed", "Garden"})
}

func TestHiddenRoomsAndBadConfigs(t *testing.T) {
	h, _ := Render(Mapping{}, smallHome(), Config{Rooms: map[string]RoomConfig{"kitchen": {Hidden: true}}})
	eq(t, "rooms", names(h.Rooms), []string{"Bedroom", "Shed", "Unassigned"})
	eq(t, "hidden rooms", names(h.HiddenRooms), []string{"Empty", "Kitchen"})
	for _, bad := range []Config{
		{Entities: map[string]EntityConfig{"nodomain": {Hidden: true}}},
		{Entities: map[string]EntityConfig{"light.x": {Hidden: true, Room: "kitchen"}}},
		{Rooms: map[string]RoomConfig{"kitchen": {Thermostat: "sensor.temp"}}},
	} {
		if bad.Validate() == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestTheJSONContractWithTheApps(t *testing.T) {
	h, _ := Render(mapped(t, smallMapping), smallHome(), Config{})
	data, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(data, &m)
	for _, key := range []string{"name", "floors", "rooms", "scenes", "summary", "security", "energy", "speakers", "names"} {
		if _, ok := m[key]; !ok {
			t.Errorf("house.json lacks %q", key)
		}
	}
	room := m["rooms"].([]any)[0].(map[string]any)
	for _, key := range []string{"id", "name", "floor", "icon", "entities"} {
		if _, ok := room[key]; !ok {
			t.Errorf("a room lacks %q", key)
		}
	}
	sec := m["security"].(map[string]any)
	if _, ok := sec["cameraFor"].(map[string]any); !ok {
		t.Error("security.cameraFor must be an object even when empty")
	}
}

func TestCameraNamesAndChannels(t *testing.T) {
	eq(t, "unifi", cameraName("Front Door - Entry High resolution channel"), "Front Door - Entry")
	eq(t, "nest", cameraName("Driveway"), "Driveway")
	eq(t, "camera word", cameraName("Back Yard Camera"), "Back Yard")
	eq(t, "lower case", cameraName("Back Yard camera"), "Back Yard")
	eq(t, "only the word", cameraName("Camera"), "Camera")
}

// One remote per television, paired on the device, keys from the integration's own names; a television that two
// integrations know is one remote (the core integration's).
func TestTelevisionsBecomeRemotesByThemselves(t *testing.T) {
	h, problems := Render(Mapping{}, smallHome(), Config{})
	eq(t, "problems", problems, []string(nil))
	eq(t, "remotes", names2(h.Remotes), []string{"Bedroom Frame", "Kitchen TV"})
	frame, gtv := h.Remotes[0], h.Remotes[1]
	eq(t, "frame id", frame.ID, "remote.bed_frame")
	eq(t, "frame room", frame.Room, "bed")
	eq(t, "frame player", frame.Player, "media_player.bed_frame")
	eq(t, "frame volume", frame.Volume, "media_player.bed_frame")
	eq(t, "frame up", frame.Keys["up"], Action{Domain: "remote", Service: "send_command", Entity: "remote.bed_frame", Data: map[string]any{"command": "KEY_UP"}})
	eq(t, "frame on", frame.Keys["power_on"], Action{Domain: "media_player", Service: "turn_on", Entity: "media_player.bed_frame"})
	eq(t, "frame play/pause", frame.Keys["play_pause"], Action{Domain: "media_player", Service: "media_play_pause", Entity: "media_player.bed_frame"})
	eq(t, "frame volume up", frame.Keys["volume_up"].Data["command"], any("KEY_VOLUP"))
	if _, has := frame.Keys["power"]; has {
		t.Error("the remote's own power key (art mode on a Frame) should not be a default")
	}
	eq(t, "frame inputs", frame.Inputs, &Inputs{Entity: "media_player.bed_frame"})
	eq(t, "gtv select", gtv.Keys["select"].Data["command"], any("DPAD_CENTER"))
	eq(t, "gtv volume", gtv.Volume, "") // no level to show
	eq(t, "tv name", h.Names["media_player.kitchen_gtv"], "Kitchen TV")
	// The receiver alone is not a remote (nothing to press that the add-on knows), nor a room tile.
	for _, r := range h.Rooms {
		if contains(r.Entities, "media_player.avr") || contains(r.Entities, "media_player.bed_frame") {
			t.Errorf("a television or receiver in room %s's tiles: %v", r.Name, r.Entities)
		}
	}
	// The guesses, in the file's shape.
	auto := AutoMapping(smallHome())
	eq(t, "auto remotes", len(auto.Remotes), 2)
	eq(t, "auto frame", auto.Remotes[0], RemoteMapping{Remote: "remote.bed_frame", Player: "media_player.bed_frame", Name: "Bedroom Frame"})
}

// Two sets with the same default device name in different rooms are two remotes; the same name in the same room (or
// with no room known) is one television that two integrations know.
func TestTwoTelevisionsOfOneNameInTwoRoomsAreTwoRemotes(t *testing.T) {
	d := smallHome()
	d.Devices = append(d.Devices, map[string]any{"id": "tv-k", "name": "Samsung TV", "area_id": "kitchen"}, map[string]any{"id": "tv-s", "name": "Samsung TV", "area_id": "shed"},
		map[string]any{"id": "tv-s2", "name": "Samsung TV"}) // the shed set again, through the custom integration, its device unplaced
	for _, tv := range []struct{ id, dev, platform string }{{"kitchen_tv", "tv-k", "samsungtv"}, {"shed_tv", "tv-s", "samsungtv"}, {"shed_tv_smart", "tv-s2", "samsungtv_smart"}} {
		d.Entities = append(d.Entities, map[string]any{"entity_id": "media_player." + tv.id, "device_id": tv.dev, "platform": tv.platform}, map[string]any{"entity_id": "remote." + tv.id, "device_id": tv.dev, "platform": tv.platform})
		d.States["media_player."+tv.id] = &State{State: "off", Attributes: j(`{"friendly_name":"Samsung TV","device_class":"tv"}`).(map[string]any)}
		d.States["remote."+tv.id] = &State{State: "on", Attributes: map[string]any{}}
	}
	h, _ := Render(Mapping{}, d, Config{})
	eq(t, "remotes", names2(h.Remotes), []string{"Bedroom Frame", "Kitchen TV", "Samsung TV", "Samsung TV"})
	ids := []string{}
	for _, r := range h.Remotes {
		if r.Name == "Samsung TV" {
			ids = append(ids, r.ID)
		}
	}
	eq(t, "one per room, the core integration's for the shed", ids, []string{"remote.kitchen_tv", "remote.shed_tv"})
}

// A button or input whose action was left out is reported as that, not as an empty entity.
func TestAnActionLeftOutIsReportedAsSuch(t *testing.T) {
	m, err := ParseMapping([]byte("version: 2\nremotes:\n  - id: living\n    player: media_player.bed_frame\n    buttons:\n      - { name: Movie night }\n"))
	if err != nil {
		t.Fatal(err)
	}
	_, problems := Render(m, smallHome(), Config{})
	eq(t, "problems", problems, []string{"remote living, button Movie night: has no action: give an entity id, {remote, command}, {select, option} or {service, entity, data}"})
}

// The file's remotes: a receiver's volume, inputs through a matrix script, a key moved to another device, a key
// dropped, a button; and what cannot be used is reported, not fatal.
func TestTheFileMapsARemoteToScriptsAndServices(t *testing.T) {
	m, err := ParseMapping([]byte(`
version: 2
remotes:
  - id: living
    name: Living room
    room: kitchen
    remote: remote.bed_frame
    player: media_player.bed_frame
    volume: media_player.avr
    inputs:
      - name: Apple TV
        action: { select: select.matrix_out_1, option: Apple TV }
        when: { select.matrix_out_1: Apple TV }
      - name: PS5
        action: script.matrix_ps5
        when: { select.matrix_out_1: PS5 }
      - name: Nowhere
        action: script.missing
    keys:
      home: { remote: remote.kitchen_gtv, command: HOME }
      art: { remote: remote.bed_frame, command: KEY_POWER }
      guide: none
      bad: { service: media_player.select_source, entity: media_player.gone }
    buttons:
      - { name: Movie night, icon: film, action: scene.movie }
  - name: Just the receiver
    inputs: media_player.avr
    keys:
      power_on: { service: media_player.turn_on, entity: media_player.avr }
`))
	if err != nil {
		t.Fatal(err)
	}
	h, problems := Render(m, smallHome(), Config{})
	eq(t, "problems", problems, []string{"remote living, key bad: Home Assistant has no media_player.gone", "remote living, input Nowhere: Home Assistant has no script.missing"})
	eq(t, "remotes", names2(h.Remotes), []string{"Living room", "Just the receiver"})
	r := h.Remotes[0]
	eq(t, "id", r.ID, "living")
	eq(t, "room", r.Room, "kitchen")
	eq(t, "volume", r.Volume, "media_player.avr")
	eq(t, "volume up", r.Keys["volume_up"], Action{Domain: "media_player", Service: "volume_up", Entity: "media_player.avr"})
	eq(t, "mute", r.Keys["mute"], Action{Domain: "media_player", Service: "volume_mute", Entity: "media_player.avr"})
	eq(t, "home", r.Keys["home"], Action{Domain: "remote", Service: "send_command", Entity: "remote.kitchen_gtv", Data: map[string]any{"command": "HOME"}})
	eq(t, "art", r.Keys["art"].Data["command"], any("KEY_POWER"))
	if _, has := r.Keys["guide"]; has {
		t.Error("guide: none should drop the key")
	}
	eq(t, "inputs", r.Inputs.Options, []InputOption{
		{Name: "Apple TV", Action: Action{Domain: "select", Service: "select_option", Entity: "select.matrix_out_1", Data: map[string]any{"option": "Apple TV"}}, Entity: "select.matrix_out_1", Value: "Apple TV"},
		{Name: "PS5", Action: Action{Domain: "script", Service: "turn_on", Entity: "script.matrix_ps5"}, Entity: "select.matrix_out_1", Value: "PS5"},
	})
	eq(t, "button", r.Buttons, []RemoteButton{{Name: "Movie night", Icon: "film", Action: Action{Domain: "scene", Service: "turn_on", Entity: "scene.movie"}}})
	avr := h.Remotes[1]
	eq(t, "avr id", avr.ID, "")
	eq(t, "avr inputs", avr.Inputs, &Inputs{Entity: "media_player.avr"})
	eq(t, "avr keys", len(avr.Keys), 1)
	// The file's shape round-trips (atrium_auto prints it).
	if out, err := yaml.Marshal(m); err != nil || !strings.Contains(string(out), "option: PS5") && !strings.Contains(string(out), "action: script.matrix_ps5") {
		t.Errorf("marshal: %v\n%s", err, out)
	}
	// Bad files are refused whole.
	for _, bad := range []string{
		"remotes:\n  - keys: {home: {remote: light.x, command: HOME}}\n",
		"remotes:\n  - keys: {home: 42}\n",
		"remotes:\n  - inputs: [{action: script.x}]\n",
		"remotes:\n  - buttons: [{name: X, action: {service: nodot}}]\n",
	} {
		if _, err := ParseMapping([]byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	// An entity alone that does not say what to call is a problem at render, not a refusal.
	m2, err := ParseMapping([]byte("remotes:\n  - name: X\n    player: media_player.avr\n    keys: {home: media_player.avr}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, problems := Render(m2, smallHome(), Config{}); len(problems) != 1 || !strings.Contains(problems[0], "does not say what to call") {
		t.Errorf("problems: %v", problems)
	}
}

func names2(rs []Remote) []string {
	out := []string{}
	for _, r := range rs {
		out = append(out, r.Name)
	}
	return out
}

// The energy section takes a battery: grid and battery powers are signed (positive importing, positive
// discharging), a sensor with the opposite sign says `invert: true`, and house.json carries the ids plus the list
// of inverted ones; the apps do the arithmetic. The add-on's own guess never fills these keys.
func TestTheEnergySectionTakesABattery(t *testing.T) {
	d := smallHome()
	for id, unit := range map[string]string{"sensor.pv_power": "W", "sensor.house_power": "W", "sensor.grid_power": "W", "sensor.battery_power": "kW", "sensor.battery_soc": "%"} {
		d.Entities = append(d.Entities, map[string]any{"entity_id": id, "platform": "demo"})
		d.States[id] = &State{State: "1", Attributes: map[string]any{"unit_of_measurement": unit, "device_class": "power"}}
	}
	m := mapped(t, `
energy:
  solar_power: sensor.pv_power
  load_power: [sensor.house_power, {entity: sensor.house_power}]
  grid_power: {entity: sensor.grid_power, invert: true}
  battery_power: {entity: sensor.battery_power}
  battery_level: sensor.battery_soc
`)
	h, problems := Render(m, d, Config{})
	eq(t, "problems", problems, []string(nil))
	eq(t, "solar", h.Energy.SolarPower, "sensor.pv_power")
	eq(t, "load", h.Energy.LoadPower, []string{"sensor.house_power"})
	eq(t, "grid", h.Energy.GridPower, "sensor.grid_power")
	eq(t, "battery", h.Energy.BatteryPower, "sensor.battery_power")
	eq(t, "level", h.Energy.BatteryLevel, "sensor.battery_soc")
	eq(t, "inverted", h.Energy.Inverted, []string{"sensor.grid_power"})
	data, _ := json.Marshal(h.Energy)
	for _, key := range []string{`"gridPower":"sensor.grid_power"`, `"batteryPower":"sensor.battery_power"`, `"batteryLevel":"sensor.battery_soc"`, `"inverted":["sensor.grid_power"]`} {
		if !strings.Contains(string(data), key) {
			t.Errorf("house.json energy lacks %s: %s", key, data)
		}
	}
	// A battery sensor Home Assistant does not have is dropped and reported like any other; nothing is inverted then.
	h, problems = Render(mapped(t, "energy:\n  battery_power: {entity: sensor.nope, invert: true}\n"), d, Config{})
	eq(t, "missing battery", h.Energy.BatteryPower, "")
	eq(t, "nothing inverted", h.Energy.Inverted, []string(nil))
	eq(t, "reported", problems, []string{"energy: Home Assistant has no sensor.nope"})
	// The shapes the file may not take.
	for _, bad := range []string{
		"energy:\n  grid_power: {entity: sensor.x, foo: 1}\n",
		"energy:\n  grid_power: {invert: true}\n",
		"energy:\n  grid_power: [sensor.x]\n",
		"energy:\n  battery_power: notanentity\n",
		"energy:\n  battery_level: notanentity\n",
		"energy:\n  load_power: [{entity: sensor.x, invert: maybe}]\n",
	} {
		if _, err := ParseMapping([]byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	// The guess fills solar and load from names, never the battery or grid keys; and it writes the short form.
	auto := AutoMapping(d)
	if auto.Energy == nil || auto.Energy.GridPower.Entity != "" || auto.Energy.BatteryPower.Entity != "" || auto.Energy.BatteryLevel != "" {
		t.Errorf("the guess filled battery or grid keys: %+v", auto.Energy)
	}
	out, _ := yaml.Marshal(auto)
	if !strings.Contains(string(out), "solar_power: sensor.pv_power\n") || strings.Contains(string(out), "invert") {
		t.Errorf("atrium_auto's energy: %s", out)
	}
	// The file's long form reads back as the long form.
	out, _ = yaml.Marshal(m)
	if !strings.Contains(string(out), "grid_power:\n        entity: sensor.grid_power\n        invert: true") && !strings.Contains(string(out), "invert: true") {
		t.Errorf("marshalled mapping: %s", out)
	}
}

// Once Home Assistant has run for ten minutes, a restored entity (registered, not provided by its integration since
// start) is not the add-on's own pick for a one-entity slot, and one with a live twin of the same name in the same
// area is a ghost that rooms and lists leave out. Before that, nothing changes. The file may still name a restored entity: kept, reported.
func TestRestoredEntitiesOnceHomeAssistantHasSettled(t *testing.T) {
	restored := func(d Dump) Dump {
		d.Entities = append(d.Entities,
			map[string]any{"entity_id": "light.kitchen_old", "area_id": "kitchen", "platform": "hue", "config_entry_id": "hue-entry"},
			map[string]any{"entity_id": "light.kitchen_winter_lamp", "area_id": "kitchen", "platform": "shelly", "config_entry_id": "shelly-entry"},
			map[string]any{"entity_id": "camera.front_old", "platform": "unifiprotect", "device_id": "bell-old", "config_entry_id": "protect-entry"},
			map[string]any{"entity_id": "camera.garage_doorbell", "platform": "ring"},
			map[string]any{"entity_id": "sensor.kitchen_old_temperature", "area_id": "kitchen", "platform": "hue", "config_entry_id": "hue-entry"},
			map[string]any{"entity_id": "sensor.kitchen_temperature", "area_id": "kitchen", "platform": "hue", "config_entry_id": "hue-entry"},
			map[string]any{"entity_id": "light.kitchen_island_shelly", "area_id": "kitchen", "platform": "shelly", "config_entry_id": "shelly-entry"},
			map[string]any{"entity_id": "switch.strip_1", "area_id": "kitchen", "device_id": "strip", "platform": "zha"},
			map[string]any{"entity_id": "switch.strip_3", "area_id": "kitchen", "device_id": "strip", "platform": "zha"},
			// Plain names: a live "Lamp" in the bedroom says nothing about the kitchen's; a live "Smart Plug" device in the
			// bedroom is not the kitchen's back under a new id; the kitchen's own "Heater" device is.
			map[string]any{"entity_id": "light.kitchen_lamp", "area_id": "kitchen", "platform": "zha"},
			map[string]any{"entity_id": "light.bed_lamp", "area_id": "bed", "platform": "zha"},
			map[string]any{"entity_id": "switch.plug_old", "area_id": "kitchen", "device_id": "plug-old", "platform": "zha"},
			map[string]any{"entity_id": "switch.plug_new", "area_id": "bed", "device_id": "plug-new", "platform": "zha"},
			map[string]any{"entity_id": "switch.heater_old", "area_id": "kitchen", "device_id": "heater-old", "platform": "zha"},
			map[string]any{"entity_id": "switch.heater", "area_id": "kitchen", "device_id": "heater", "platform": "zha"},
		)
		d.Devices = append(d.Devices, map[string]any{"id": "bell", "name": "Front"}, map[string]any{"id": "bell-old", "name": "Front"}, map[string]any{"id": "strip", "name": "Kitchen Strip"},
			map[string]any{"id": "plug-old", "name": "Smart Plug"}, map[string]any{"id": "plug-new", "name": "Smart Plug"}, map[string]any{"id": "heater-old", "name": "Heater"}, map[string]any{"id": "heater", "name": "Heater"})
		d.States["light.kitchen_lamp"] = &State{State: "unavailable", Attributes: j(`{"friendly_name":"Lamp","restored":true}`).(map[string]any)}
		d.States["light.bed_lamp"] = &State{State: "on", Attributes: j(`{"friendly_name":"Lamp"}`).(map[string]any)}
		d.States["switch.plug_old"] = &State{State: "unavailable", Attributes: j(`{"friendly_name":"Smart Plug","restored":true}`).(map[string]any)}
		d.States["switch.plug_new"] = &State{State: "on", Attributes: j(`{"friendly_name":"Smart Plug"}`).(map[string]any)}
		d.States["switch.heater_old"] = &State{State: "unavailable", Attributes: j(`{"friendly_name":"Heater","restored":true}`).(map[string]any)}
		d.States["switch.heater"] = &State{State: "off", Attributes: j(`{"friendly_name":"Heater"}`).(map[string]any)}
		d.States["light.kitchen_island_shelly"] = &State{State: "unavailable", Attributes: j(`{"friendly_name":"Kitchen Island lights","restored":true}`).(map[string]any)}
		d.States["switch.strip_1"] = &State{State: "on", Attributes: j(`{"friendly_name":"Kitchen Strip outlet 1"}`).(map[string]any)}
		d.States["switch.strip_3"] = &State{State: "unavailable", Attributes: j(`{"friendly_name":"Kitchen Strip outlet 3","restored":true}`).(map[string]any)}
		d.States["light.kitchen_old"] = &State{State: "unavailable", Attributes: j(`{"friendly_name":"Kitchen Main lights","restored":true}`).(map[string]any)}
		d.States["light.kitchen_winter_lamp"] = &State{State: "unavailable", Attributes: j(`{"friendly_name":"Winter lamp","restored":true}`).(map[string]any)}
		d.States["camera.front_old"] = &State{State: "unavailable", Attributes: j(`{"friendly_name":"Front - Old channel","restored":true}`).(map[string]any)}
		d.States["camera.garage_doorbell"] = &State{State: "unavailable", Attributes: j(`{"friendly_name":"Garage doorbell","restored":true}`).(map[string]any)}
		d.States["sensor.kitchen_old_temperature"] = &State{State: "unavailable", Attributes: j(`{"friendly_name":"Old kitchen temperature","device_class":"temperature","restored":true}`).(map[string]any)}
		d.States["sensor.kitchen_temperature"] = &State{State: "21.5", Attributes: j(`{"friendly_name":"Kitchen temperature","device_class":"temperature","unit_of_measurement":"°C"}`).(map[string]any)}
		d.ConfigEntries = list(`[{"entry_id":"hue-entry","state":"loaded"},{"entry_id":"shelly-entry","state":"setup_retry"},{"entry_id":"protect-entry","state":"loaded"}]`)
		d.Running = true
		return d
	}
	room := func(h House, id string) Room {
		for _, r := range h.Rooms {
			if r.ID == id {
				return r
			}
		}
		t.Fatalf("no room %s", id)
		return Room{}
	}
	mapping := mapped(t, "version: 2\nrooms:\n  - id: bed\n    entities: [light.kitchen_winter_lamp]\n")

	// Just started: restored entities are like any other, and nothing is listed as stale.
	young := restored(smallHome())
	young.RunningFor = time.Minute
	if young.Settled() {
		t.Fatal("a minute in is not settled")
	}
	h, problems := Render(mapping, young, Config{})
	if k := room(h, "kitchen"); !contains(k.Entities, "light.kitchen_old") || !contains(k.Entities, "light.kitchen_winter_lamp") {
		t.Errorf("before settling the kitchen keeps every entity: %v", k.Entities)
	}
	if !reflect.DeepEqual(h.Security.Doorbells, []string{"camera.front", "camera.garage_doorbell"}) || !contains(h.Security.Cameras, "camera.front_old") {
		t.Errorf("before settling the restored cameras count: doorbells %v cameras %v", h.Security.Doorbells, h.Security.Cameras)
	}
	if len(problems) != 0 {
		t.Errorf("before settling nothing is reported: %v", problems)
	}
	if s := Stale(young); len(s) != 0 {
		t.Errorf("before settling nothing is stale: %v", s)
	}

	// Settled: ghosts go, other restored entities stay but are never the add-on's pick, the file's line is reported.
	settled := restored(smallHome())
	settled.RunningFor = 11 * time.Minute
	if !settled.Settled() {
		t.Fatal("eleven minutes in is settled")
	}
	h, problems = Render(mapping, settled, Config{})
	k := room(h, "kitchen")
	if contains(k.Entities, "light.kitchen_old") {
		t.Errorf("light.kitchen_old has a live twin (Kitchen Main lights) and is a ghost: %v", k.Entities)
	}
	if !contains(k.Entities, "light.kitchen_island_shelly") {
		t.Errorf("a restored light whose integration is still retrying is offline, not a ghost, whatever its name: %v", k.Entities)
	}
	if !contains(k.Entities, "switch.strip_3") {
		t.Errorf("a dead outlet on a device whose other outlets are live is not a ghost (no other device took its place): %v", k.Entities)
	}
	if !contains(k.Entities, "light.kitchen_lamp") || !contains(room(h, "bed").Entities, "light.bed_lamp") {
		t.Errorf("a live \"Lamp\" in another room is another lamp, so the kitchen's stays (unavailable): %v", k.Entities)
	}
	if !contains(k.Entities, "switch.plug_old") {
		t.Errorf("a live \"Smart Plug\" device in another room is another plug, not this one back: %v", k.Entities)
	}
	if contains(k.Entities, "switch.heater_old") || !contains(k.Entities, "switch.heater") {
		t.Errorf("a live \"Heater\" device in the same room is this one back under a new id, so the old one is a ghost: %v", k.Entities)
	}
	if k.Temperature != "sensor.kitchen_temperature" {
		t.Errorf("the room's temperature is the live sensor, not the restored one: %q", k.Temperature)
	}
	if !contains(room(h, "bed").Entities, "light.kitchen_winter_lamp") {
		t.Errorf("the file's restored entity is kept: %v", room(h, "bed").Entities)
	}
	if !reflect.DeepEqual(problems, []string{"room bed: light.kitchen_winter_lamp is restored: Home Assistant has not seen it since it started, so it shows as unavailable"}) {
		t.Errorf("the file's restored entity is reported, not refused: %v", problems)
	}
	if !reflect.DeepEqual(h.Security.Doorbells, []string{"camera.front"}) {
		t.Errorf("the doorbell is never a restored camera once settled: %v", h.Security.Doorbells)
	}
	if contains(h.Security.Cameras, "camera.front_old") {
		t.Errorf("camera.front_old is on a device named like the live Front camera's and is a ghost: %v", h.Security.Cameras)
	}
	if !contains(h.Security.Cameras, "camera.garage_doorbell") {
		t.Errorf("a restored camera with no twin stays listed (shown unavailable): %v", h.Security.Cameras)
	}
	if !contains(k.Entities, "light.kitchen_winter_lamp") && !contains(room(h, "bed").Entities, "light.kitchen_winter_lamp") {
		t.Error("a restored light without a twin is still the room's")
	}
	// Stale: restored, and its integration is loaded, so it is not coming back. Not the one whose integration is still retrying.
	stale := Stale(settled)
	ids := []string{}
	for _, s := range stale {
		ids = append(ids, s.ID)
	}
	if !reflect.DeepEqual(ids, []string{"camera.front_old", "light.kitchen_old", "sensor.kitchen_old_temperature"}) {
		t.Errorf("stale: %v", ids)
	}
	if stale[1].Name != "Kitchen Main lights" || stale[1].Platform != "hue" {
		t.Errorf("stale entries carry the name and platform: %+v", stale[1])
	}
	// Not running (starting up, or the dump could not tell): never settled, whatever the duration says.
	settled.Running = false
	if settled.Settled() || len(Stale(settled)) != 0 {
		t.Error("not running is not settled")
	}
}
