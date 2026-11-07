package house

import (
	"encoding/json"
	"reflect"
	"testing"
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

// A small Home Assistant: two areas on one floor, a fridge that is not a thermostat, a car that is not a door.
func smallHome(overlay string) Dump {
	d := Dump{Name: "Test Home", States: map[string]*State{}}
	d.Floors = list(`[{"floor_id":"up","name":"Upstairs","level":1},{"floor_id":"down","name":"Downstairs","level":0}]`)
	d.Areas = list(`[{"area_id":"kitchen","name":"Kitchen","floor_id":"down"},{"area_id":"bed","name":"Bedroom","floor_id":"up"},{"area_id":"shed","name":"Shed","floor_id":null},{"area_id":"empty","name":"Empty","floor_id":"down"}]`)
	d.Devices = list(`[{"id":"dev1","area_id":"kitchen"},{"id":"car","area_id":"shed"}]`)
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
	st("weather.forecast_home", "sunny", `{}`)
	st("person.a", "home", `{}`)
	st("alarm_control_panel.house", "disarmed", `{}`)
	if overlay != "" {
		d.Overlay, _ = j(overlay).(map[string]any)
	}
	return d
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

func TestLayoutComesFromHomeAssistantAlone(t *testing.T) {
	h := Build(smallHome(""), Config{})
	eq(t, "name", h.Name, "Test Home")
	eq(t, "floors", floorNames(h.Floors), []string{"Downstairs", "Upstairs", "Elsewhere"})
	eq(t, "rooms", names(h.Rooms), []string{"Kitchen", "Bedroom", "Shed"}) // by floor; the empty area is left out
	kitchen := h.Rooms[0]
	eq(t, "kitchen", kitchen.Entities, []string{"light.kitchen_island", "light.kitchen_group", "switch.kitchen_kettle", "climate.downstairs", "media_player.kitchen"}) // no hidden light, no fridge
	eq(t, "island", h.Names["light.kitchen_island"], "Island lights")
	eq(t, "thermostat", h.Names["climate.downstairs"], "Thermostat")
	eq(t, "kettle", h.Names["switch.kitchen_kettle"], "Kettle")
	eq(t, "locks", h.Security.Locks, []string{}) // the car's lock is not the house's
	eq(t, "hazards", h.Security.Hazards, []string{"binary_sensor.smoke"})
	eq(t, "scenes", h.Scenes, []SceneRef{{Entity: "scene.movie", Name: "Movie"}})
	eq(t, "speakers", h.Speakers, []string{"media_player.kitchen"})
	eq(t, "weather", h.Summary.Weather, "weather.forecast_home")
	eq(t, "people", h.Summary.People, []string{"person.a"})
	eq(t, "alarm", h.Summary.Alarm, "alarm_control_panel.house")
	for _, r := range h.Rooms {
		for _, id := range r.Entities {
			if smallHome("").States[id] == nil {
				t.Errorf("%s shows %s, which has no state", r.Name, id)
			}
		}
	}
}

func TestTheOverlayOnlyRefinesTheLayout(t *testing.T) {
	h := Build(smallHome(`{"lights":{"_help":"x","a":"light.kitchen_group"},"scenes":[{"entity":"scene.movie","name":"🎬 Film"}],"rooms":{"k":{"area":"Kitchen","extra":["cover.bed_shade"]}}}`), Config{})
	eq(t, "kitchen", h.Rooms[0].Entities, []string{"light.kitchen_group", "switch.kitchen_kettle", "cover.bed_shade", "climate.downstairs", "media_player.kitchen"})
	eq(t, "rooms", names(h.Rooms), []string{"Kitchen", "Bedroom"}) // the shed's only light is not curated
	eq(t, "scene", h.Scenes[0].Name, "🎬 Film")
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
	h := Build(smallHome(""), c)
	eq(t, "name", h.Name, "Our House")
	eq(t, "floors", floorNames(h.Floors), []string{"Downstairs", "Outside", "Upstairs"}) // ordered floors first, the rest by level
	eq(t, "rooms", names(h.Rooms), []string{"Kitchen", "Shed"})                          // the bedroom's only control moved to the kitchen, so it is not a room any more
	eq(t, "kitchen", h.Rooms[0].Entities, []string{"media_player.kitchen", "light.kitchen_group", "switch.kitchen_kettle", "cover.bed_shade", "climate.hall", "climate.downstairs"})
	eq(t, "kettle", h.Names["switch.kitchen_kettle"], "Kettle plug")
	eq(t, "hall thermostat named for the kitchen", h.Names["climate.hall"], "Hall Thermostat")
	if _, dormant := h.Names["light.gone"]; dormant {
		t.Error("a dormant override leaked into the layout")
	}
	// The light comes back: its override applies again, nothing to do.
	d := smallHome("")
	d.Entities = append(d.Entities, map[string]any{"entity_id": "light.gone", "area_id": "shed", "platform": "demo"})
	d.States["light.gone"] = &State{State: "on", Attributes: map[string]any{}}
	eq(t, "returned", Build(d, c).Names["light.gone"], "A light that no longer exists")
}

func TestHiddenRoomsAndBadConfigs(t *testing.T) {
	h := Build(smallHome(""), Config{Rooms: map[string]RoomConfig{"kitchen": {Hidden: true}}})
	eq(t, "rooms", names(h.Rooms), []string{"Bedroom", "Shed"})
	eq(t, "hidden rooms", names(h.HiddenRooms), []string{"Kitchen"})
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
	data, err := json.Marshal(Build(smallHome(""), Config{}))
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
