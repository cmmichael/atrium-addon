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
	deviceMAC  map[string]string // the device's MAC address, when the registry has one: two integrations' devices for one television share it
	registry   map[string]map[string]any
	// Restored entities, once Home Assistant has settled (see Dump.Settled). A ghost is a restored entity with a
	// live twin: another entity of its domain, not restored, in the same area, with the same name or on a device of
	// the same name; the device came back under a new id and the old entity was left behind. The area counts only
	// when both are known: two plain "Lamp"s in different rooms are two lamps, one of them unplugged for the season,
	// but an entity with no area (its device is gone from the registry) is matched house-wide. Only an entity whose
	// integration is loaded can be a ghost: while a hub is offline, every one of its entities is restored, and a
	// live light of the same name elsewhere says nothing about them. Ghosts are left out of what the add-on decides by itself; other
	// restored entities stay (a device that is off for the winter is still the room's) but are never the add-on's
	// own pick for a slot that takes one entity.
	settled bool
	ghost   map[string]bool
}

// pickable says whether the add-on may choose this entity by itself for a slot that takes one (the doorbell, a
// thermostat, the solar sensor, the outdoor temperature): not a restored one, once Home Assistant has settled.
func (x *index) pickable(e ent) bool { return !(x.settled && e.restored) }

// live is the entities of a domain the add-on may list by itself: all of them, less the ghosts.
func (x *index) live(dom string) []ent {
	var out []ent
	for _, e := range x.all(dom) {
		if !x.ghost[e.id] {
			out = append(out, e)
		}
	}
	return out
}

// firstPickable is the first of these the add-on may pick by itself.
func (x *index) firstPickable(es []ent) string {
	for _, e := range es {
		if x.pickable(e) {
			return e.id
		}
	}
	return ""
}

func newIndex(d Dump, music *MusicMapping) *index {
	x := &index{d: d, byID: map[string]ent{}, deviceHas: map[string]map[string]bool{}, isSpeaker: map[string]bool{}, deviceArea: map[string]string{}, deviceName: map[string]string{}, deviceMAC: map[string]string{}, settled: d.Settled(), ghost: map[string]bool{}}
	for _, dev := range d.Devices {
		if id, _ := dev["id"].(string); id != "" {
			x.deviceArea[id], _ = dev["area_id"].(string)
			x.deviceName[id] = firstOf(str(dev["name_by_user"]), str(dev["name"]))
			if conns, ok := dev["connections"].([]any); ok {
				for _, c := range conns {
					if pair, ok := c.([]any); ok && len(pair) == 2 && str(pair[0]) == "mac" {
						x.deviceMAC[id] = strings.ToLower(str(pair[1]))
					}
				}
			}
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
		entry, _ := e["config_entry_id"].(string)
		x.byID[id] = ent{id: id, domain: domain(id), area: area, device: dev, platform: platform, name: name, class: class, restored: st.restored(), entry: entry}
	}
	x.ents = make([]ent, 0, len(x.byID))
	for _, e := range x.byID {
		x.ents = append(x.ents, e)
	}
	sort.Slice(x.ents, func(i, j int) bool { return x.ents[i].id < x.ents[j].id })
	if x.settled {
		type key struct{ domain, name string }
		type live struct{ device, area string }
		liveNames := map[key][]live{}   // the live entities of that domain and name
		liveDevices := map[key][]live{} // the live entities of that domain on a device of that name
		for _, e := range x.ents {
			if e.restored {
				continue
			}
			l := live{e.device, e.area}
			liveNames[key{e.domain, strings.ToLower(e.name)}] = append(liveNames[key{e.domain, strings.ToLower(e.name)}], l)
			if n := x.deviceName[e.device]; n != "" {
				liveDevices[key{e.domain, strings.ToLower(n)}] = append(liveDevices[key{e.domain, strings.ToLower(n)}], l)
			}
		}
		sameArea := func(a, b string) bool { return a == "" || b == "" || a == b }
		loaded := d.loadedEntries()
		for _, e := range x.ents {
			if !e.restored || (loaded != nil && e.entry != "" && !loaded[e.entry]) {
				continue
			}
			ghost := false
			for _, l := range liveNames[key{e.domain, strings.ToLower(e.name)}] {
				if sameArea(e.area, l.area) {
					ghost = true
				}
			}
			if n := x.deviceName[e.device]; n != "" && !ghost {
				// Another device of the same name is the same device back under a new id. The entity's own device
				// having live siblings says nothing: one endpoint of a power strip can die while the rest report.
				for _, l := range liveDevices[key{e.domain, strings.ToLower(n)}] {
					if l.device != e.device && sameArea(e.area, l.area) {
						ghost = true
					}
				}
			}
			if ghost {
				x.ghost[e.id] = true
			}
		}
	}

	// Speakers: the file's, else by device class — less Music Assistant's mirrors of players that exist already,
	// less a camera's or doorbell's own speaker.
	if music != nil {
		for _, id := range music.Speakers {
			x.isSpeaker[id] = true
		}
		return x
	}
	var all, own []string
	for _, e := range x.live("media_player") {
		if e.class != "speaker" || kiosks[e.platform] || x.deviceHas[e.device]["camera"] {
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
	if rs := x.autoRemotes(); len(rs) > 0 {
		m.Remotes = rs
	}
	// The widgets follow the rendered house (they point at its scenes and rooms), so the auto mapping leaves them
	// to the renderer; atrium_auto shows them from the house it renders.
	return m
}

func (x *index) autoHome() HomeMapping {
	h := HomeMapping{People: []string{}}
	if ws := x.all("weather"); len(ws) > 0 {
		h.Weather = x.firstPickable(ws)
		for _, w := range ws {
			if (w.id == "weather.forecast_home" || w.id == "weather.home") && x.pickable(w) {
				h.Weather = w.id
			}
		}
	}
	h.Alarm = x.firstPickable(x.all("alarm_control_panel"))
	for _, p := range x.live("person") {
		h.People = append(h.People, p.id)
	}
	// An outdoor temperature: a sensor that says so in its name, not in a room; else the weather's.
	for _, e := range x.all("sensor") {
		n := strings.ToLower(e.id + " " + e.name)
		if e.class == "temperature" && x.pickable(e) && (strings.Contains(n, "outdoor") || strings.Contains(n, "outside") || strings.Contains(n, "exterior")) {
			h.OutdoorTemperature = e.id
			break
		}
	}
	return h
}

func (x *index) autoSecurity() SecurityMapping {
	s := SecurityMapping{Cameras: []string{}, Doorbells: []string{}, CameraFor: map[string]string{}, Locks: []string{}, Entries: []string{}, Hazards: []string{}}
	var cams []ent
	for _, c := range x.live("camera") {
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
		if !x.pickable(c) { // the doorbell is one camera: not a restored one, once settled
			continue
		}
		bell := strings.Contains(c.id, "doorbell") || strings.Contains(strings.ToLower(c.name), "doorbell")
		// The ring itself: the device's doorbell event (its state is the last ring's time) or binary sensor, so
		// the apps can tell a ring from the camera's own state. An event entity is preferred over a binary sensor.
		ring := ""
		if c.device != "" {
			for _, e := range x.ents {
				if e.device == c.device && (e.domain == "binary_sensor" || e.domain == "event") && (e.class == "doorbell" || strings.Contains(e.id, "doorbell")) {
					bell = true
					if ring == "" || e.domain == "event" {
						ring = e.id
					}
				}
			}
		}
		if bell {
			s.Doorbells = append(s.Doorbells, c.id)
			if ring != "" {
				if s.Rings == nil {
					s.Rings = map[string]string{}
				}
				s.Rings[c.id] = ring
			}
		}
	}
	for _, l := range x.live("lock") {
		if !vehicles[l.platform] {
			s.Locks = append(s.Locks, l.id)
		}
	}
	var entries []ent
	for _, c := range x.live("cover") {
		if (c.class == "garage" || c.class == "gate" || c.class == "door") && !vehicles[c.platform] {
			entries = append(entries, c)
		}
	}
	for _, b := range x.live("binary_sensor") {
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
	for _, b := range x.live("binary_sensor") {
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
	for _, b := range x.live("button") {
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
	// Solar, load, the day's totals, consumers and water are guessed from names. grid_power, battery_power and
	// battery_level are not: a sign convention cannot be read off a name, so the file (or the skill's draft, which
	// marks its guess "# check") says which sensors those are.
	e := EnergyMapping{LoadPower: []PowerRef{}, Consumers: []string{}}
	var powers, energies []ent
	for _, s := range x.live("sensor") {
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
			if x.pickable(s) && containsAny(n, words) && !containsAny(n, not) {
				return s.id
			}
		}
		return ""
	}
	e.SolarPower = PowerRef{Entity: named(powers, []string{"solar", "pv_power", "production"})}
	if load := named(powers, []string{"consumption", "house_power", "home_power", "load_power", "main_panel", "total_power"}, "solar", "production", "grid", "battery"); load != "" {
		e.LoadPower = []PowerRef{{Entity: load}}
	}
	e.SolarToday = named(energies, []string{"solar", "production"})
	e.UsedToday = named(energies, []string{"consumption", "house", "home", "main_panel"}, "solar", "production")
	// Consumers: the circuits drawing the most right now, a dozen of them; the totals above are not consumers.
	taken := set(e.SolarPower.Entity)
	for _, ref := range e.LoadPower {
		taken[ref.Entity] = true
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
		if !x.pickable(s) {
			continue
		}
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
	return e.SolarPower.Entity != "" || len(e.LoadPower) > 0 || e.GridPower.Entity != "" || e.BatteryPower.Entity != "" || e.BatteryLevel != "" ||
		e.SolarToday != "" || e.UsedToday != "" || len(e.Consumers) > 0 || e.WaterFlow != ""
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

// ringFor is the ring entity on a doorbell camera's device: its doorbell event (preferred: its state is the last
// ring's time) or binary sensor; "" when the device has none.
func (x *index) ringFor(camera string) string {
	c := x.byID[camera]
	if c.device == "" {
		return ""
	}
	ring := ""
	for _, e := range x.ents {
		if e.device == c.device && (e.domain == "binary_sensor" || e.domain == "event") && (e.class == "doorbell" || strings.Contains(e.id, "doorbell")) {
			if ring == "" || e.domain == "event" {
				ring = e.id
			}
		}
	}
	return ring
}
