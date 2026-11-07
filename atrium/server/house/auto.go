package house

import (
	"sort"
	"strings"
)

// The judgment calls the add-on makes by itself, from what Home Assistant has, so every screen has something
// sensible before anyone writes a line of atrium.yaml. A section in the file replaces the add-on's guess for
// that section wholesale; the rooms take overrides. These are heuristics: readable, few, and each one earned
// by a real house.

// Integrations whose locks, doors, covers, switches and climates belong to a car, not the house.
var vehicles = set("tesla_fleet", "tesla_custom", "teslemetry", "tessie", "toyota", "toyota_na", "bmw_connected_drive", "subaru",
	"kia_uvo", "hyundai_kia_connect", "volvo", "volvooncall", "fordpass", "mazda", "nissan_leaf", "onstar", "polestar_api",
	"audiconnect", "volkswagencarnet", "renault", "mbapi2020", "rivian", "lucidmotors", "porscheconnect")

// Integrations that are a wall tablet or a browser: their "lights" and "players" are a screen, not the room's.
var kiosks = set("browser_mod", "fully_kiosk", "wallpanel")

// Integrations that are appliances and nothing else (ovens, dishwashers, fridges): their lights, switches and
// climates are the appliance's own, not the room's. Hubs that also carry real lights (SmartThings) are not here.
var appliances = set("miele", "home_connect", "lg_thinq", "bosch_shc", "electrolux", "whirlpool")

var entryClasses = set("door", "window", "garage_door", "opening", "gate")
var hazardClasses = set("smoke", "gas", "carbon_monoxide", "moisture", "safety")

// shows says whether a room shows this entity of its own accord: what a person walks in and touches.
func (x *index) shows(e ent) bool {
	if vehicles[e.platform] || kiosks[e.platform] || appliances[e.platform] {
		return false
	}
	switch e.domain {
	case "light", "cover", "fan", "lock", "switch":
		return true
	case "climate":
		return heatsARoom(x.d.States[e.id])
	case "media_player":
		return x.isSpeaker[e.id]
	}
	return false
}

// index is the dump, cross-referenced once per render.
type index struct {
	d          Dump
	byID       map[string]ent
	ents       []ent                      // by id
	deviceHas  map[string]map[string]bool // device id → domains it has (a camera's speaker is not a room's)
	isSpeaker  map[string]bool
	deviceArea map[string]string
	deviceName map[string]string
	registry   map[string]map[string]any
}

func newIndex(d Dump, music *MusicMapping) *index {
	x := &index{d: d, byID: map[string]ent{}, deviceHas: map[string]map[string]bool{}, isSpeaker: map[string]bool{}, deviceArea: map[string]string{}, deviceName: map[string]string{}}
	for _, dev := range d.Devices {
		if id, _ := dev["id"].(string); id != "" {
			x.deviceArea[id], _ = dev["area_id"].(string)
			x.deviceName[id] = firstOf(str(dev["name_by_user"]), str(dev["name"]))
		}
	}
	registry := map[string]map[string]any{}
	x.registry = registry
	for _, e := range d.Entities {
		if id, _ := e["entity_id"].(string); id != "" {
			registry[id] = e
			if dev, _ := e["device_id"].(string); dev != "" {
				if x.deviceHas[dev] == nil {
					x.deviceHas[dev] = map[string]bool{}
				}
				x.deviceHas[dev][domain(id)] = true
			}
		}
	}
	for id, st := range d.States {
		e := registry[id]
		if e != nil && (e["disabled_by"] != nil || e["hidden_by"] != nil || e["entity_category"] != nil) {
			continue
		}
		dev, _ := e["device_id"].(string)
		area, _ := e["area_id"].(string)
		if area == "" {
			area = x.deviceArea[dev]
		}
		platform, _ := e["platform"].(string)
		name := st.str("friendly_name")
		if name == "" {
			name, _ = e["name"].(string)
		}
		if name == "" {
			name, _ = e["original_name"].(string)
		}
		if name == "" {
			name = pretty(id)
		}
		class := st.str("device_class")
		if class == "" {
			class, _ = e["original_device_class"].(string)
		}
		x.byID[id] = ent{id: id, domain: domain(id), area: area, device: dev, platform: platform, name: name, class: class}
	}
	x.ents = make([]ent, 0, len(x.byID))
	for _, e := range x.byID {
		x.ents = append(x.ents, e)
	}
	sort.Slice(x.ents, func(i, j int) bool { return x.ents[i].id < x.ents[j].id })

	// Speakers: the file's, else by device class — less Music Assistant's mirrors of players that exist already,
	// less a camera's or doorbell's own speaker.
	if music != nil {
		for _, id := range music.Speakers {
			x.isSpeaker[id] = true
		}
		return x
	}
	var all, own []string
	for _, e := range x.ents {
		if e.domain != "media_player" || e.class != "speaker" || kiosks[e.platform] || x.deviceHas[e.device]["camera"] {
			continue
		}
		all = append(all, e.id)
		if e.platform != "music_assistant" {
			own = append(own, e.id)
		}
	}
	if len(own) > 0 {
		all = own
	}
	for _, id := range all {
		x.isSpeaker[id] = true
	}
	return x
}

func (x *index) all(dom string) []ent {
	var out []ent
	for _, e := range x.ents {
		if e.domain == dom {
			out = append(out, e)
		}
	}
	return out
}

func (x *index) ids(es []ent) []string {
	out := []string{}
	for _, e := range es {
		out = append(out, e.id)
	}
	return out
}

// AutoMapping is what the add-on decides by itself for the custom screens, in the file's own shape, so a
// Claude (or the owner) can read the guesses, keep the right ones and override the rest.
func AutoMapping(d Dump) Mapping {
	x := newIndex(d, nil)
	m := Mapping{Version: 2}
	m.Home = x.autoHome()
	if s := x.autoSecurity(); s.any() {
		m.Security = &s
	}
	if e := x.autoEnergy(); e.any() {
		m.Energy = &e
	}
	if mu := x.autoMusic(); len(mu.Speakers) > 0 {
		m.Music = &mu
	}
	return m
}

func (x *index) autoHome() HomeMapping {
	h := HomeMapping{People: []string{}}
	if ws := x.all("weather"); len(ws) > 0 {
		h.Weather = ws[0].id
		for _, w := range ws {
			if w.id == "weather.forecast_home" || w.id == "weather.home" {
				h.Weather = w.id
			}
		}
	}
	if as := x.all("alarm_control_panel"); len(as) > 0 {
		h.Alarm = as[0].id
	}
	for _, p := range x.all("person") {
		h.People = append(h.People, p.id)
	}
	// An outdoor temperature: a sensor that says so in its name, not in a room; else the weather's.
	for _, e := range x.all("sensor") {
		n := strings.ToLower(e.id + " " + e.name)
		if e.class == "temperature" && (strings.Contains(n, "outdoor") || strings.Contains(n, "outside") || strings.Contains(n, "exterior")) {
			h.OutdoorTemperature = e.id
			break
		}
	}
	return h
}

func (x *index) autoSecurity() SecurityMapping {
	s := SecurityMapping{Cameras: []string{}, Doorbells: []string{}, CameraFor: map[string]string{}, Locks: []string{}, Entries: []string{}, Hazards: []string{}}
	var cams []ent
	for _, c := range x.all("camera") {
		// UniFi Protect's G4/G6 doorbells have a second, downward "package" camera: a channel, not a camera.
		if strings.Contains(c.id, "package_camera") || strings.HasSuffix(strings.ToLower(c.name), "package camera") {
			continue
		}
		if !vehicles[c.platform] && !kiosks[c.platform] {
			cams = append(cams, c)
			s.Cameras = append(s.Cameras, c.id)
		}
	}
	for _, c := range cams {
		bell := strings.Contains(c.id, "doorbell") || strings.Contains(strings.ToLower(c.name), "doorbell")
		if !bell && c.device != "" {
			for _, e := range x.ents {
				if e.device == c.device && (e.domain == "binary_sensor" || e.domain == "event") && (e.class == "doorbell" || strings.Contains(e.id, "doorbell")) {
					bell = true
					break
				}
			}
		}
		if bell {
			s.Doorbells = append(s.Doorbells, c.id)
		}
	}
	for _, l := range x.all("lock") {
		if !vehicles[l.platform] {
			s.Locks = append(s.Locks, l.id)
		}
	}
	var entries []ent
	for _, c := range x.all("cover") {
		if (c.class == "garage" || c.class == "gate" || c.class == "door") && !vehicles[c.platform] {
			entries = append(entries, c)
		}
	}
	for _, b := range x.all("binary_sensor") {
		if entryClasses[b.class] && !vehicles[b.platform] {
			entries = append(entries, b)
		}
	}
	for _, e := range entries {
		s.Entries = append(s.Entries, e.id)
		for _, c := range cams { // the camera that sees this entry: same device, else same area
			if e.device != "" && c.device == e.device {
				s.CameraFor[e.id] = c.id
				break
			}
		}
		if s.CameraFor[e.id] == "" && e.area != "" {
			for _, c := range cams {
				if c.area == e.area {
					s.CameraFor[e.id] = c.id
					break
				}
			}
		}
	}
	for _, b := range x.all("binary_sensor") {
		if hazardClasses[b.class] && !vehicles[b.platform] {
			s.Hazards = append(s.Hazards, b.id)
		}
	}
	s.Doors = x.autoDoors()
	return s
}

// autoDoors finds doors whose lock is a button: an unlock button on the same device as a door contact
// (UniFi Access, and Nuki/SwitchBot-style "open" buttons).
func (x *index) autoDoors() []DoorMapping {
	doors := []DoorMapping{}
	for _, b := range x.all("button") {
		n := strings.ToLower(b.id + " " + b.name)
		if b.device == "" || vehicles[b.platform] || !(strings.Contains(n, "unlock") || (b.platform == "unifi_access" && !strings.Contains(n, "restart"))) {
			continue
		}
		d := DoorMapping{Unlock: b.id}
		for _, e := range x.ents {
			if e.device == b.device && e.domain == "binary_sensor" && (e.class == "door" || e.class == "lock" || e.class == "opening") {
				d.Contact = e.id
				break
			}
		}
		if dev := x.deviceName[b.device]; dev != "" {
			d.Name = dev
		}
		doors = append(doors, d)
	}
	return doors
}

func (s SecurityMapping) any() bool {
	return len(s.Cameras)+len(s.Locks)+len(s.Entries)+len(s.Hazards)+len(s.Doors) > 0
}

func (x *index) autoEnergy() EnergyMapping {
	e := EnergyMapping{LoadPower: []string{}, Consumers: []string{}}
	var powers, energies []ent
	for _, s := range x.all("sensor") {
		switch s.class {
		case "power":
			if !vehicles[s.platform] {
				powers = append(powers, s)
			}
		case "energy":
			if n := strings.ToLower(s.id); strings.Contains(n, "today") || strings.Contains(n, "daily") {
				energies = append(energies, s)
			}
		}
	}
	named := func(list []ent, words []string, not ...string) string {
		for _, s := range list {
			n := strings.ToLower(s.id + " " + s.name)
			if containsAny(n, words) && !containsAny(n, not) {
				return s.id
			}
		}
		return ""
	}
	e.SolarPower = named(powers, []string{"solar", "pv_power", "production"})
	if load := named(powers, []string{"consumption", "house_power", "home_power", "load_power", "main_panel", "total_power"}, "solar", "production"); load != "" {
		e.LoadPower = []string{load}
	}
	e.SolarToday = named(energies, []string{"solar", "production"})
	e.UsedToday = named(energies, []string{"consumption", "house", "home", "main_panel"}, "solar", "production")
	// Consumers: the circuits drawing the most right now, a dozen of them; the totals above are not consumers.
	taken := set(e.SolarPower)
	for _, id := range e.LoadPower {
		taken[id] = true
	}
	type drawing struct {
		id string
		w  float64
	}
	var ds []drawing
	for _, s := range powers {
		if taken[s.id] {
			continue
		}
		w, ok := parseFloat(x.d.States[s.id].State)
		if !ok {
			continue
		}
		if strings.EqualFold(x.d.States[s.id].str("unit_of_measurement"), "kW") {
			w *= 1000
		}
		if w > 0 {
			ds = append(ds, drawing{s.id, w})
		}
	}
	sort.SliceStable(ds, func(i, j int) bool { return ds[i].w > ds[j].w })
	for i, d := range ds {
		if i == 12 {
			break
		}
		e.Consumers = append(e.Consumers, d.id)
	}
	for _, s := range x.all("sensor") {
		if s.class == "volume_flow_rate" && e.WaterFlow == "" {
			e.WaterFlow = s.id
		}
		if s.class == "water" && e.WaterToday == "" {
			if n := strings.ToLower(s.id); strings.Contains(n, "today") || strings.Contains(n, "daily") {
				e.WaterToday = s.id
			}
		}
	}
	return e
}

func (e EnergyMapping) any() bool {
	return e.SolarPower != "" || len(e.LoadPower) > 0 || e.SolarToday != "" || e.UsedToday != "" || len(e.Consumers) > 0 || e.WaterFlow != ""
}

func (x *index) autoMusic() MusicMapping {
	m := MusicMapping{Speakers: []string{}}
	for _, e := range x.ents {
		if x.isSpeaker[e.id] {
			m.Speakers = append(m.Speakers, e.id)
		}
	}
	return m
}

// cameraName drops what UniFi Protect adds to a camera's name: "Front Door - Entry High resolution channel" →
// "Front Door - Entry".
func cameraName(n string) string {
	for _, suffix := range []string{" high resolution channel", " medium resolution channel", " low resolution channel", " insecure", " camera"} {
		if strings.HasSuffix(strings.ToLower(n), suffix) && len(n) > len(suffix) {
			n = n[:len(n)-len(suffix)]
		}
	}
	return strings.TrimSpace(n)
}
