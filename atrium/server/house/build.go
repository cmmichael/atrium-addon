package house

import (
	"sort"
	"strings"
)

// Integrations whose locks, doors and covers belong to a car, not the house.
var vehicles = set("tesla_fleet", "tesla_custom", "teslemetry", "tessie", "toyota", "toyota_na", "bmw_connected_drive", "subaru",
	"kia_uvo", "hyundai_kia_connect", "volvo", "volvooncall", "fordpass", "mazda", "nissan_leaf", "onstar", "polestar_api",
	"audiconnect", "volkswagencarnet", "renault", "mbapi2020", "rivian", "lucidmotors", "porscheconnect")

var hazards = set("smoke", "moisture", "gas", "carbon_monoxide")

// Domains that a room shows by default; anything else needs Show in the config.
var roomOrder = map[string]int{"light": 0, "switch": 1, "cover": 2, "climate": 3, "fan": 4, "media_player": 5, "lock": 6}

type ent struct {
	id, domain, area, device, platform, name, class string
}

// Build turns Home Assistant's areas, floors, devices and entities into the layout, with the overlay (when
// the house publishes one) and the owner's configuration applied on top.
func Build(d Dump, c Config) House {
	deviceArea := map[string]string{}
	for _, dev := range d.Devices {
		if id, _ := dev["id"].(string); id != "" {
			if a, _ := dev["area_id"].(string); a != "" {
				deviceArea[id] = a
			}
		}
	}
	registry := map[string]map[string]any{}
	for _, e := range d.Entities {
		if id, _ := e["entity_id"].(string); id != "" {
			registry[id] = e
		}
	}

	byID := map[string]ent{}
	for id, st := range d.States {
		e := registry[id]
		if e != nil && (e["disabled_by"] != nil || e["hidden_by"] != nil || e["entity_category"] != nil) {
			continue
		}
		dev, _ := e["device_id"].(string)
		area, _ := e["area_id"].(string)
		if area == "" {
			area = deviceArea[dev]
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
		byID[id] = ent{id: id, domain: domain(id), area: area, device: dev, platform: platform, name: name, class: st.str("device_class")}
	}
	ents := make([]ent, 0, len(byID))
	for _, e := range byID {
		ents = append(ents, e)
	}
	sort.Slice(ents, func(i, j int) bool { return ents[i].id < ents[j].id })
	all := func(dom string) []ent {
		var out []ent
		for _, e := range ents {
			if e.domain == dom {
				out = append(out, e)
			}
		}
		return out
	}
	exists := func(id string) string {
		if id != "" && d.States[id] != nil {
			return id
		}
		return ""
	}
	hidden := func(id string) bool { return c.Entities[id].Hidden }
	house0, _ := d.Overlay["house"].(map[string]any)
	devices0, _ := d.Overlay["devices"].(map[string]any)
	type overlayDevice struct {
		key, entity string
		value       map[string]any
	}
	overlayDevices := func(prefix string) []overlayDevice {
		var out []overlayDevice
		for _, k := range sortedKeys(devices0) {
			if !strings.HasPrefix(k, prefix) {
				continue
			}
			v := devices0[k]
			obj, _ := v.(map[string]any)
			id, _ := v.(string)
			if id == "" && obj != nil {
				id, _ = obj["entity"].(string)
			}
			if id = exists(id); id != "" {
				out = append(out, overlayDevice{strings.TrimPrefix(k, prefix), id, obj})
			}
		}
		return out
	}

	h := newHouse(firstOf(c.Name, d.Name, "Home"))

	// Speakers first: rooms list theirs.
	if list, _ := get(d.Overlay, "music", "speakers").([]any); len(list) > 0 {
		for _, sp := range list {
			spo, _ := sp.(map[string]any)
			id := exists(firstOf(str(spo["sonos"]), str(spo["entity"])))
			if id == "" || hidden(id) {
				continue
			}
			h.Speakers = append(h.Speakers, id)
			if n := str(spo["name"]); n != "" {
				h.Names[id] = n
			}
		}
	} else {
		var found, own []string
		for _, e := range all("media_player") {
			if e.class == "speaker" && !hidden(e.id) {
				found = append(found, e.id)
				if e.platform != "music_assistant" { // Music Assistant mirrors players that exist already
					own = append(own, e.id)
				}
			}
		}
		if len(own) > 0 {
			found = own
		}
		h.Speakers = append(h.Speakers, found...)
	}
	isSpeaker := set(h.Speakers...)

	// Rooms: one per area that has something to control.
	curated := map[string]bool{}
	if lights, _ := d.Overlay["lights"].(map[string]any); lights != nil {
		for k, v := range lights {
			if k != "_help" {
				for _, id := range strs(v) {
					curated[id] = true
				}
			}
		}
	}
	type roomOverlay struct {
		extra     []string
		temp, hum string
	}
	roomCfg := map[string]*roomOverlay{}
	if rooms, _ := d.Overlay["rooms"].(map[string]any); rooms != nil {
		for _, k := range sortedKeys(rooms) {
			cfg, _ := rooms[k].(map[string]any)
			area := strings.ToLower(str(cfg["area"]))
			if area == "" {
				continue
			}
			ro := roomCfg[area]
			if ro == nil {
				ro = &roomOverlay{}
				roomCfg[area] = ro
			}
			ro.extra = append(ro.extra, strs(cfg["extra"])...)
			ro.temp, ro.hum = firstOf(ro.temp, str(cfg["temp"])), firstOf(ro.hum, str(cfg["hum"]))
		}
	}
	// Where the configuration moves entities to.
	moved := map[string][]string{} // area id → entity ids
	for _, id := range sortedKeys(c.Entities) {
		if ec := c.Entities[id]; ec.Room != "" && exists(id) != "" {
			moved[ec.Room] = append(moved[ec.Room], id)
		}
	}
	roomOrderKey := map[string]int{}
	for _, a := range d.Areas {
		aid, _ := a["area_id"].(string)
		aname, _ := a["name"].(string)
		if aid == "" || aname == "" {
			continue
		}
		rc := c.Rooms[aid]
		ro := roomCfg[strings.ToLower(aname)]
		var ids, hiddenHere []string
		add := func(id string) {
			if !contains(ids, id) {
				ids = append(ids, id)
			}
		}
		for _, e := range ents {
			if e.area != aid || vehicles[e.platform] || c.Entities[e.id].Room != "" {
				continue
			}
			if hidden(e.id) {
				hiddenHere = append(hiddenHere, e.id)
				continue
			}
			show := c.Entities[e.id].Show
			switch e.domain {
			case "light":
				show = show || len(curated) == 0 || curated[e.id]
			case "switch":
				show = show || e.platform != "browser_mod" // a kiosk's screen switches are not the room's
			case "cover", "fan", "lock":
				show = true
			case "climate":
				show = show || heatsARoom(d.States[e.id])
			case "media_player":
				show = show || isSpeaker[e.id]
			}
			if show {
				add(e.id)
			}
		}
		if ro != nil {
			for _, x := range ro.extra {
				if exists(x) != "" && !hidden(x) {
					add(x)
				}
			}
		}
		for _, x := range moved[aid] {
			add(x)
		}
		if t := exists(rc.Thermostat); t != "" {
			add(t)
		}
		if len(ids) == 0 && len(hiddenHere) == 0 {
			continue
		}
		sort.SliceStable(ids, func(i, j int) bool {
			oi, oj := rank(roomOrder, domain(ids[i])), rank(roomOrder, domain(ids[j]))
			if oi != oj {
				return oi < oj
			}
			return firstOf(byID[ids[i]].name, ids[i]) < firstOf(byID[ids[j]].name, ids[j])
		})
		ids = ordered(ids, rc.Entities)
		temp := ""
		if ro != nil {
			temp = exists(ro.temp)
		}
		if temp == "" {
			temp = exists(str(a["temperature_entity_id"]))
		}
		if temp == "" {
			for _, e := range ents {
				if e.area == aid && e.domain == "sensor" && e.class == "temperature" && isRoomTemperature(d.States[e.id]) {
					temp = e.id
					break
				}
			}
		}
		hum := ""
		if ro != nil {
			hum = exists(ro.hum)
		}
		if hum == "" {
			hum = exists(str(a["humidity_entity_id"]))
		}
		floor := firstOf(rc.Floor, str(a["floor_id"]), "other")
		name := firstOf(rc.Name, aname)
		room := Room{ID: aid, Name: name, Floor: floor, Icon: iconFor(name), Entities: ids, Temperature: temp, Humidity: hum, Hidden: hiddenHere}
		if rc.Hidden || len(ids) == 0 {
			h.HiddenRooms = append(h.HiddenRooms, room) // hidden, or nothing left to show: an editor can bring it back
			continue
		}
		h.Rooms = append(h.Rooms, room)
		if rc.Order != nil {
			roomOrderKey[aid] = *rc.Order
		}
		for _, id := range ids {
			if domain(id) != "media_player" {
				h.Names[id] = short(firstOf(byID[id].name, pretty(id)), aname)
			} else if h.Names[id] == "" {
				h.Names[id] = aname
			}
		}
	}

	// Floors: the registry's by level, the configuration's own after, "Elsewhere" for rooms with none.
	type leveled struct {
		f     Floor
		level float64
	}
	var levels []leveled
	for _, f := range d.Floors {
		id, _ := f["floor_id"].(string)
		n, _ := f["name"].(string)
		if id == "" || n == "" {
			continue
		}
		lv, _ := f["level"].(float64)
		levels = append(levels, leveled{Floor{id, firstOf(c.Floors[id].Name, n)}, lv})
	}
	sort.SliceStable(levels, func(i, j int) bool { return levels[i].level < levels[j].level })
	known := map[string]bool{}
	for _, l := range levels {
		h.Floors = append(h.Floors, l.f)
		known[l.f.ID] = true
	}
	for _, id := range sortedKeys(c.Floors) {
		if !known[id] {
			h.Floors = append(h.Floors, Floor{id, firstOf(c.Floors[id].Name, pretty(id))})
			known[id] = true
		}
	}
	other := false
	for i := range h.Rooms {
		if !known[h.Rooms[i].Floor] {
			h.Rooms[i].Floor = "other"
			other = true
		}
	}
	if other {
		name := "Elsewhere"
		if len(h.Floors) == 0 {
			name = "Rooms"
		}
		h.Floors = append(h.Floors, Floor{"other", firstOf(c.Floors["other"].Name, name)})
	}
	sort.SliceStable(h.Floors, func(i, j int) bool {
		oi, oj := c.Floors[h.Floors[i].ID].Order, c.Floors[h.Floors[j].ID].Order
		switch {
		case oi != nil && oj != nil:
			return *oi < *oj
		case oi != nil:
			return true
		default:
			return false
		}
	})
	floorRank := map[string]int{}
	for i, f := range h.Floors {
		floorRank[f.ID] = i
	}
	sort.SliceStable(h.Rooms, func(i, j int) bool {
		a, b := h.Rooms[i], h.Rooms[j]
		if floorRank[a.Floor] != floorRank[b.Floor] {
			return floorRank[a.Floor] < floorRank[b.Floor]
		}
		oa, okA := roomOrderKey[a.ID]
		ob, okB := roomOrderKey[b.ID]
		switch {
		case okA && okB:
			return oa < ob
		case okA != okB:
			return okA
		}
		return a.Name < b.Name
	})
	for _, id := range h.Speakers {
		if h.Names[id] == "" {
			h.Names[id] = firstOf(byID[id].name, pretty(id))
		}
	}

	// Scenes.
	if list, _ := d.Overlay["scenes"].([]any); len(list) > 0 {
		for _, s := range list {
			so, _ := s.(map[string]any)
			if id := exists(str(so["entity"])); id != "" && !hidden(id) {
				h.Scenes = append(h.Scenes, SceneRef{Entity: id, Name: firstOf(str(so["name"]), pretty(id)), Group: str(so["group"])})
			}
		}
	} else {
		scenes := all("scene")
		sort.SliceStable(scenes, func(i, j int) bool { return scenes[i].name < scenes[j].name })
		for _, s := range scenes {
			if len(h.Scenes) == 24 {
				break
			}
			if !hidden(s.id) {
				h.Scenes = append(h.Scenes, SceneRef{Entity: s.id, Name: s.name})
			}
		}
	}

	// The whole-house readouts.
	h.Summary.Weather = firstOf(exists(str(d.Overlay["weather"])), exists("weather.forecast_home"), firstID(all("weather")))
	h.Summary.OutdoorTemp = exists(str(house0["outdoor_temp"]))
	h.Summary.IndoorTemp = exists(str(house0["indoor_temp"]))
	h.Summary.Alarm = firstOf(exists(str(house0["alarm"])), firstID(all("alarm_control_panel")))
	for _, p := range strs(house0["people"]) {
		if exists(p) != "" {
			h.Summary.People = append(h.Summary.People, p)
		}
	}
	if len(h.Summary.People) == 0 {
		for _, p := range all("person") {
			h.Summary.People = append(h.Summary.People, p.id)
		}
	}

	// Security.
	cams := overlayDevices("cam-")
	if len(cams) == 0 {
		for _, cam := range all("camera") {
			if !vehicles[cam.platform] && !hidden(cam.id) {
				h.Security.Cameras = append(h.Security.Cameras, cam.id)
				h.Names[cam.id] = cleanCamera(firstOf(cam.name, pretty(cam.id)))
			}
		}
	} else {
		for _, cam := range cams {
			if hidden(cam.entity) || contains(h.Security.Cameras, cam.entity) {
				continue
			}
			h.Security.Cameras = append(h.Security.Cameras, cam.entity)
			h.Names[cam.entity] = pretty(cam.key)
			for _, ev := range strs(cam.value["events"]) {
				if strings.Contains(ev, "doorbell") && exists(ev) != "" {
					if !contains(h.Security.Doorbells, ev) {
						h.Security.Doorbells = append(h.Security.Doorbells, ev)
					}
					h.Security.CameraFor[ev] = cam.entity
				}
			}
		}
	}
	for _, e := range all("event") {
		if e.class != "doorbell" || contains(h.Security.Doorbells, e.id) || hidden(e.id) {
			continue
		}
		h.Security.Doorbells = append(h.Security.Doorbells, e.id)
		for _, cam := range all("camera") {
			if e.device != "" && cam.device == e.device {
				h.Security.CameraFor[e.id] = cam.id
				break
			}
		}
	}
	for _, l := range all("lock") {
		if !vehicles[l.platform] && !hidden(l.id) {
			h.Security.Locks = append(h.Security.Locks, l.id)
		}
	}
	doors := append(overlayDevices("door-"), overlayDevices("gate-")...)
	if len(doors) == 0 {
		var found []string
		for _, cv := range all("cover") {
			if (cv.class == "garage" || cv.class == "gate" || cv.class == "door") && !vehicles[cv.platform] && !hidden(cv.id) {
				found = append(found, cv.id)
			}
		}
		for _, bs := range all("binary_sensor") {
			if (bs.class == "door" || bs.class == "garage_door") && bs.area != "" && !vehicles[bs.platform] && !hidden(bs.id) {
				found = append(found, bs.id)
			}
		}
		if len(found) > 24 {
			found = found[:24]
		}
		h.Security.Entries = append(h.Security.Entries, found...)
	} else {
		for _, dr := range doors {
			id := firstOf(exists(str(dr.value["cover"])), dr.entity)
			if hidden(id) || contains(h.Security.Entries, id) {
				continue
			}
			h.Security.Entries = append(h.Security.Entries, id)
			h.Names[id] = pretty(dr.key)
			if dr.value["cover"] != nil {
				h.Names[id] += " door"
			}
		}
	}
	for _, bs := range all("binary_sensor") {
		if hazards[bs.class] && !vehicles[bs.platform] && !hidden(bs.id) {
			h.Security.Hazards = append(h.Security.Hazards, bs.id)
		}
	}
	if leak := exists(str(get(house0, "water", "leak"))); leak != "" && !contains(h.Security.Hazards, leak) {
		h.Security.Hazards = append(h.Security.Hazards, leak)
	}
	for _, id := range concat(h.Security.Locks, h.Security.Entries, h.Security.Hazards) {
		if h.Names[id] == "" {
			h.Names[id] = firstOf(byID[id].name, pretty(id))
		}
	}

	// Energy: exact with an overlay, a best guess from names without one.
	var powers, energies []ent
	for _, s := range all("sensor") {
		if s.class == "power" {
			powers = append(powers, s)
		}
		if s.class == "energy" && (strings.Contains(s.id, "today") || strings.Contains(s.id, "daily")) {
			energies = append(energies, s)
		}
	}
	named := func(list []ent, words []string, not ...string) string {
		for _, e := range list {
			n := strings.ToLower(e.id + " " + e.name)
			if containsAny(n, words) && !containsAny(n, not) {
				return e.id
			}
		}
		return ""
	}
	h.Energy.SolarPower = firstOf(exists(str(house0["solar_power"])), named(powers, []string{"solar", "pv_power", "production"}))
	for _, id := range strs(house0["house_power"]) {
		if exists(id) != "" {
			h.Energy.LoadPower = append(h.Energy.LoadPower, id)
		}
	}
	if len(h.Energy.LoadPower) == 0 {
		if id := named(powers, []string{"consumption", "house_power", "home_power", "load_power", "main_panel"}, "solar", "production"); id != "" {
			h.Energy.LoadPower = append(h.Energy.LoadPower, id)
		}
	}
	h.Energy.SolarToday = firstOf(exists(str(house0["solar_today"])), named(energies, []string{"solar", "production"}))
	h.Energy.UsedToday = firstOf(exists(str(house0["house_today"])), named(energies, []string{"consumption", "house", "home", "main_panel"}, "solar", "production"))
	var circuits []string
	if cm, _ := house0["circuits"].(map[string]any); cm != nil {
		for _, k := range sortedKeys(cm) {
			if k != "_help" && exists(k) != "" {
				circuits = append(circuits, k)
			}
		}
	}
	if len(circuits) > 0 {
		h.Energy.Consumers = append(h.Energy.Consumers, circuits...)
	} else {
		taken := set(append([]string{h.Energy.SolarPower}, h.Energy.LoadPower...)...)
		for _, p := range powers {
			if len(h.Energy.Consumers) == 40 {
				break
			}
			if !taken[p.id] && !vehicles[p.platform] && !hidden(p.id) {
				h.Energy.Consumers = append(h.Energy.Consumers, p.id)
			}
		}
	}
	for _, id := range h.Energy.Consumers {
		h.Names[id] = cleanPower(firstOf(byID[id].name, pretty(id)))
	}
	h.Energy.WaterFlow = exists(str(get(house0, "water", "flow")))
	h.Energy.WaterToday = exists(str(get(house0, "water", "today")))

	// Hidden things keep a name, so an editor can show them.
	for _, r := range append(h.Rooms, h.HiddenRooms...) {
		for _, id := range append(r.Entities, r.Hidden...) {
			if h.Names[id] == "" {
				h.Names[id] = short(firstOf(byID[id].name, pretty(id)), r.Name)
			}
		}
	}
	// The owner's names win over everything.
	for id, ec := range c.Entities {
		if ec.Name != "" && exists(id) != "" {
			h.Names[id] = ec.Name
		}
	}
	return h
}

// MARK: Judgement calls

// A thermostat's range spans room temperature (20 °C or 70 °F); a fridge's or a freezer's does not.
func heatsARoom(s *State) bool {
	lo, okLo := s.num("min_temp")
	hi, okHi := s.num("max_temp")
	if !okLo || !okHi {
		return true
	}
	return (lo <= 20 && 20 <= hi) || (lo <= 70 && 70 <= hi)
}

func isRoomTemperature(s *State) bool {
	if s == nil {
		return false
	}
	v, ok := parseFloat(s.State)
	if !ok {
		return false
	}
	if strings.Contains(s.str("unit_of_measurement"), "F") {
		return 50 <= v && v <= 95
	}
	return 10 <= v && v <= 35
}

func pretty(id string) string {
	words := id
	if i := strings.LastIndex(id, "."); i >= 0 {
		words = id[i+1:]
	}
	return capitalizedFirst(strings.NewReplacer("_", " ", "-", " ").Replace(words))
}

// "Family Room Floor Lamp" in Family Room → "Floor lamp".
func short(name, room string) string {
	for _, prefix := range []string{room, strings.ReplaceAll(room, " Room", ""), strings.ReplaceAll(room, "'s", "")} {
		if strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix)+" ") && len(name) > len(prefix)+1 {
			return capitalizedFirst(strings.TrimSpace(name[len(prefix)+1:]))
		}
	}
	return name
}

func cleanCamera(name string) string {
	for _, tail := range []string{" High resolution channel", " Medium resolution channel", " Low resolution channel", " High", " Camera"} {
		if strings.HasSuffix(strings.ToLower(name), strings.ToLower(tail)) {
			name = name[:len(name)-len(tail)]
		}
	}
	return name
}

func cleanPower(name string) string {
	for _, tail := range []string{" power minute average", " watts now", " current power", " power", " watts"} {
		if strings.HasSuffix(strings.ToLower(name), tail) {
			name = name[:len(name)-len(tail)]
		}
	}
	return capitalizedFirst(name)
}

var icons = [][2]string{
	{"kitchen", "fork.knife"}, {"dining", "fork.knife.circle"}, {"family", "sofa"}, {"living", "sofa"}, {"lounge", "sofa"}, {"garage", "car"},
	{"gym", "dumbbell"}, {"office", "desktopcomputer"}, {"study", "desktopcomputer"}, {"bath", "shower"}, {"powder", "shower"}, {"laundry", "washer"},
	{"pantry", "cabinet"}, {"stair", "stairs"}, {"hall", "door.left.hand.open"}, {"entry", "door.left.hand.open"}, {"play", "gamecontroller"},
	{"game", "gamecontroller"}, {"server", "server.rack"}, {"bed", "bed.double"}, {"yard", "tree"}, {"garden", "tree"}, {"patio", "tree"},
	{"exterior", "tree"}, {"outside", "tree"}, {"porch", "door.left.hand.open"}, {"deck", "tree"}, {"pool", "figure.pool.swim"}, {"room", "bed.double"},
}

func iconFor(room string) string {
	n := strings.ToLower(room)
	for _, ic := range icons {
		if strings.Contains(n, ic[0]) {
			return ic[1]
		}
	}
	return "square.split.bottomrightquarter"
}
