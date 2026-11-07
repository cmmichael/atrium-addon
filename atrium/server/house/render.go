package house

import (
	"fmt"
	"sort"
	"strings"
)

// Integrations whose locks, doors and covers belong to a car, not the house.
var vehicles = set("tesla_fleet", "tesla_custom", "teslemetry", "tessie", "toyota", "toyota_na", "bmw_connected_drive", "subaru",
	"kia_uvo", "hyundai_kia_connect", "volvo", "volvooncall", "fordpass", "mazda", "nissan_leaf", "onstar", "polestar_api",
	"audiconnect", "volkswagencarnet", "renault", "mbapi2020", "rivian", "lucidmotors", "porscheconnect")

// The order entities take within a room.
var roomOrder = map[string]int{"light": 0, "switch": 1, "cover": 2, "climate": 3, "fan": 4, "media_player": 5, "lock": 6}

// The room for entities Home Assistant has in no area, and the floor for rooms on no floor.
const (
	unassignedRoom = "unassigned"
	otherFloor     = "other"
)

type ent struct {
	id, domain, area, platform, name, class string
}

// Render draws the house: the rooms from Home Assistant's areas, floors, devices and entities (so a new device is
// there on the next build), the mapping's overrides and custom screens on top of that, then the owner's in-app
// preferences on top of both. Returns what in the mapping could not be used (entities Home Assistant does not
// have, areas it does not know) so the set-up page and the log can say so.
func Render(m Mapping, d Dump, c Config) (House, []string) {
	var problems []string
	problem := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	// MARK: What Home Assistant has
	deviceArea := map[string]string{}
	for _, dev := range d.Devices {
		if id, _ := dev["id"].(string); id != "" {
			deviceArea[id], _ = dev["area_id"].(string)
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
		byID[id] = ent{id: id, domain: domain(id), area: area, platform: platform, name: name, class: st.str("device_class")}
	}
	ents := make([]ent, 0, len(byID))
	for _, e := range byID {
		ents = append(ents, e)
	}
	sort.Slice(ents, func(i, j int) bool { return ents[i].id < ents[j].id })
	haName := func(id string) string {
		if e, ok := byID[id]; ok {
			return e.name
		}
		if st := d.States[id]; st != nil && st.str("friendly_name") != "" {
			return st.str("friendly_name")
		}
		return pretty(id)
	}

	// exists keeps an entity id Home Assistant has; anything else is dropped and reported, once.
	reported := map[string]bool{}
	exists := func(where, id string) string {
		if id == "" {
			return ""
		}
		if d.States[id] != nil {
			return id
		}
		if !reported[id] {
			reported[id] = true
			problem("%s: Home Assistant has no %s", where, id)
		}
		return ""
	}
	has := func(id string) string { // an id Home Assistant has, or nothing; not reported (it was its own pick)
		if d.States[id] != nil {
			return id
		}
		return ""
	}
	existing := func(where string, ids []string) []string {
		out := []string{}
		for _, id := range ids {
			if id = exists(where, id); id != "" && !contains(out, id) {
				out = append(out, id)
			}
		}
		return out
	}
	hidden := func(id string) bool { return c.Entities[id].Hidden || m.hides(id, byID[id].platform) }
	visible := func(where string, ids []string) []string {
		out := []string{}
		for _, id := range existing(where, ids) {
			if !hidden(id) {
				out = append(out, id)
			}
		}
		return out
	}

	h := newHouse(firstOf(c.Name, m.Name, d.Name, "Home"))

	// MARK: Floors — Home Assistant's by level, the mapping's and the owner's own after, ranked where asked
	floorRankKey := map[string]int{}
	floorMapping := map[string]FloorMapping{}
	for _, f := range m.Floors {
		floorMapping[f.ID] = f
		if f.Rank != nil {
			floorRankKey[f.ID] = *f.Rank
		}
	}
	type leveled struct {
		f     Floor
		level float64
	}
	var levels []leveled
	known := map[string]bool{}
	for _, f := range d.Floors {
		id, _ := f["floor_id"].(string)
		n, _ := f["name"].(string)
		if id == "" || n == "" || known[id] {
			continue
		}
		lv, _ := f["level"].(float64)
		levels = append(levels, leveled{Floor{id, firstOf(c.Floors[id].Name, floorMapping[id].Name, n)}, lv})
		known[id] = true
	}
	sort.SliceStable(levels, func(i, j int) bool { return levels[i].level < levels[j].level })
	for _, l := range levels {
		h.Floors = append(h.Floors, l.f)
	}
	for _, f := range m.Floors {
		if !known[f.ID] {
			h.Floors = append(h.Floors, Floor{f.ID, firstOf(c.Floors[f.ID].Name, f.Name, pretty(f.ID))})
			known[f.ID] = true
		}
	}
	for _, id := range sortedKeys(c.Floors) {
		if !known[id] {
			h.Floors = append(h.Floors, Floor{id, firstOf(c.Floors[id].Name, pretty(id))})
			known[id] = true
		}
	}

	// MARK: Rooms — one per area, plus the mapping's own, plus one for what is in no area
	isSpeaker := map[string]bool{}
	if m.Music != nil {
		for _, id := range m.Music.Speakers {
			isSpeaker[id] = true
		}
	}
	var own []string // speakers by device class, less Music Assistant's mirrors of players that exist already
	for _, e := range ents {
		if e.domain == "media_player" && e.class == "speaker" {
			isSpeaker[e.id] = true
			if e.platform != "music_assistant" {
				own = append(own, e.id)
			}
		}
	}
	if len(own) > 0 && m.Music == nil {
		isSpeaker = set(own...)
	}
	moved := map[string][]string{} // the owner's moves, by room id
	for _, id := range sortedKeys(c.Entities) {
		if ec := c.Entities[id]; ec.Room != "" && d.States[id] != nil {
			moved[ec.Room] = append(moved[ec.Room], id)
		}
	}
	movedAway := func(id string) bool { return c.Entities[id].Room != "" }
	roomMapping := map[string]RoomMapping{}
	for _, rm := range m.Rooms {
		roomMapping[rm.ID] = rm
	}
	// shows says whether a room shows this entity of its own accord (the mapping and the owner can add others).
	shows := func(e ent) bool {
		if vehicles[e.platform] {
			return false
		}
		switch e.domain {
		case "light", "cover", "fan", "lock":
			return true
		case "switch":
			return e.platform != "browser_mod" // a kiosk's screen switches are not the room's
		case "climate":
			return heatsARoom(d.States[e.id])
		case "media_player":
			return isSpeaker[e.id]
		}
		return false
	}
	type area struct {
		id, name, floor, temp, hum string
		ents                       []ent
	}
	areas := []*area{}
	byArea := map[string]*area{}
	for _, a := range d.Areas {
		id, _ := a["area_id"].(string)
		name, _ := a["name"].(string)
		if id == "" || name == "" || byArea[id] != nil {
			continue
		}
		ar := &area{id: id, name: name, floor: str(a["floor_id"]), temp: str(a["temperature_entity_id"]), hum: str(a["humidity_entity_id"])}
		areas = append(areas, ar)
		byArea[id] = ar
	}
	sort.SliceStable(areas, func(i, j int) bool { return areas[i].name < areas[j].name })
	for _, rm := range m.Rooms { // the mapping's own rooms, after the areas
		if byArea[rm.ID] == nil && rm.ID != unassignedRoom {
			ar := &area{id: rm.ID, name: firstOf(rm.Name, pretty(rm.ID))}
			if len(rm.Entities) == 0 {
				problem("room %s: Home Assistant has no area %s, and the room lists no entities", rm.ID, rm.ID)
			}
			areas = append(areas, ar)
			byArea[rm.ID] = ar
		}
	}
	unassigned := &area{id: unassignedRoom, name: "Unassigned"}
	for _, e := range ents {
		if ar := byArea[e.area]; ar != nil {
			ar.ents = append(ar.ents, e)
		} else if shows(e) {
			unassigned.ents = append(unassigned.ents, e)
		}
	}
	if len(unassigned.ents) > 0 && byArea[unassignedRoom] == nil {
		areas = append(areas, unassigned)
		byArea[unassignedRoom] = unassigned
	}

	roomRankKey, roomOrderKey := map[string]int{}, map[string]int{}
	for _, ar := range areas {
		rm, rc := roomMapping[ar.id], c.Rooms[ar.id]
		where := "room " + ar.id
		var ids, hiddenHere []string
		add := func(id string) {
			if id != "" && !contains(ids, id) {
				ids = append(ids, id)
			}
		}
		for _, e := range ar.ents {
			switch {
			case hidden(e.id) || contains(rm.Hide.Entities, e.id):
				if shows(e) || c.Entities[e.id].Show {
					hiddenHere = append(hiddenHere, e.id)
				}
			case movedAway(e.id):
			case shows(e) || c.Entities[e.id].Show:
				add(e.id)
			}
		}
		for _, id := range existing(where, rm.Entities) {
			if !hidden(id) && !movedAway(id) {
				add(id)
			}
		}
		if t := exists(where, firstOf(rc.Thermostat, rm.Thermostat)); t != "" && !hidden(t) {
			add(t)
		}
		for _, id := range moved[ar.id] {
			if !hidden(id) {
				add(id)
			}
		}
		sort.SliceStable(ids, func(i, j int) bool {
			oi, oj := rank(roomOrder, domain(ids[i])), rank(roomOrder, domain(ids[j]))
			if oi != oj {
				return oi < oj
			}
			return haName(ids[i]) < haName(ids[j])
		})
		ids = ordered(ids, rc.Entities)
		for _, id := range rm.Hide.Entities {
			exists(where, id)
		}
		name := firstOf(rc.Name, rm.Name, ar.name)
		temp := exists(where, rm.Temperature)
		if temp == "" {
			temp = has(ar.temp) // the area's own pick, if Home Assistant has one
		}
		if temp == "" {
			for _, e := range ar.ents {
				if e.domain == "sensor" && e.class == "temperature" && isRoomTemperature(d.States[e.id]) {
					temp = e.id
					break
				}
			}
		}
		hum := exists(where, rm.Humidity)
		if hum == "" {
			hum = has(ar.hum)
		}
		if hum == "" {
			for _, e := range ar.ents {
				if e.domain == "sensor" && e.class == "humidity" {
					hum = e.id
					break
				}
			}
		}
		floor := firstOf(rc.Floor, rm.Floor, ar.floor)
		if floor != "" && !known[floor] {
			if rm.Floor == floor {
				problem("%s: floor %q is not a floor", where, floor)
			}
			floor = ""
		}
		if floor == "" {
			floor = otherFloor
		}
		room := Room{ID: ar.id, Name: name, Floor: floor, Icon: firstOf(rm.Icon, iconFor(name)), Entities: ids, Temperature: temp, Humidity: hum, Hidden: hiddenHere}
		if room.Entities == nil {
			room.Entities = []string{}
		}
		if rc.Hidden || rm.Hide.All || len(ids) == 0 {
			h.HiddenRooms = append(h.HiddenRooms, room) // hidden, or nothing to show: the editor can bring it back
			continue
		}
		h.Rooms = append(h.Rooms, room)
		if rm.Rank != nil {
			roomRankKey[ar.id] = *rm.Rank
		}
		if rc.Order != nil {
			roomOrderKey[ar.id] = *rc.Order
		}
		for _, id := range ids {
			if domain(id) == "media_player" {
				h.Names[id] = firstOf(m.Names[id], name)
			} else {
				h.Names[id] = firstOf(m.Names[id], short(haName(id), name))
			}
		}
	}
	// The owner's moves to a room that does not exist make nothing; say so once.
	for _, id := range sortedKeys(moved) {
		if byArea[id] == nil {
			problem("preferences move %v to room %q, which there is not", moved[id], id)
		}
	}
	if !known[otherFloor] {
		for _, r := range h.Rooms {
			if r.Floor == otherFloor {
				name := "Elsewhere"
				if len(h.Floors) == 0 {
					name = "Rooms"
				}
				h.Floors = append(h.Floors, Floor{otherFloor, firstOf(c.Floors[otherFloor].Name, floorMapping[otherFloor].Name, name)})
				break
			}
		}
	}
	// Floors: the owner's order first, then the mapping's rank, then as they are (level, then the rest).
	sort.SliceStable(h.Floors, func(i, j int) bool {
		return compare(c.Floors[h.Floors[i].ID].Order, c.Floors[h.Floors[j].ID].Order, floorRankKey, h.Floors[i].ID, h.Floors[j].ID) < 0
	})
	floorRank := map[string]int{}
	for i, f := range h.Floors {
		floorRank[f.ID] = i
	}
	// Rooms: by floor; within one, Unassigned last, then the owner's order, then the mapping's rank, then by name.
	sort.SliceStable(h.Rooms, func(i, j int) bool {
		a, b := h.Rooms[i], h.Rooms[j]
		if floorRank[a.Floor] != floorRank[b.Floor] {
			return floorRank[a.Floor] < floorRank[b.Floor]
		}
		if (a.ID == unassignedRoom) != (b.ID == unassignedRoom) {
			return b.ID == unassignedRoom
		}
		var oa, ob *int
		if o, ok := roomOrderKey[a.ID]; ok {
			oa = &o
		}
		if o, ok := roomOrderKey[b.ID]; ok {
			ob = &o
		}
		if r := compare(oa, ob, roomRankKey, a.ID, b.ID); r != 0 {
			return r < 0
		}
		return a.Name < b.Name
	})

	// MARK: Scenes — the mapping's first, then every other scene by name
	sceneSeen := map[string]bool{}
	for _, s := range m.Scenes {
		if id := exists("scenes", s.Entity); id != "" && !hidden(id) && !sceneSeen[id] {
			sceneSeen[id] = true
			h.Scenes = append(h.Scenes, SceneRef{Entity: id, Name: firstOf(c.Entities[id].Name, s.Name, m.Names[id], haName(id))})
		}
	}
	var rest []SceneRef
	for _, e := range ents {
		if e.domain == "scene" && !sceneSeen[e.id] && !hidden(e.id) {
			rest = append(rest, SceneRef{Entity: e.id, Name: firstOf(c.Entities[e.id].Name, m.Names[e.id], e.name)})
		}
	}
	sort.SliceStable(rest, func(i, j int) bool { return rest[i].Name < rest[j].Name })
	h.Scenes = append(h.Scenes, rest...)

	// MARK: The screens only the mapping can fill
	h.Summary.Weather = exists("home", m.Home.Weather)
	h.Summary.OutdoorTemp = exists("home", m.Home.OutdoorTemperature)
	h.Summary.IndoorTemp = exists("home", m.Home.IndoorTemperature)
	h.Summary.Alarm = exists("home", m.Home.Alarm)
	h.Summary.People = existing("home", m.Home.People)

	tab := func(name string) bool { v, ok := c.Tabs[name]; return !ok || v }
	if s := m.Security; s != nil && tab("security") {
		h.Security.Cameras = visible("security", s.Cameras)
		h.Security.Doorbells = visible("security", s.Doorbells)
		h.Security.Locks = visible("security", s.Locks)
		h.Security.Entries = visible("security", s.Entries)
		h.Security.Hazards = visible("security", s.Hazards)
		for _, entry := range sortedKeys(s.CameraFor) {
			if e, cam := exists("security", entry), exists("security", s.CameraFor[entry]); e != "" && cam != "" {
				h.Security.CameraFor[e] = cam
			}
		}
		for _, id := range concat(h.Security.Cameras, h.Security.Doorbells, h.Security.Locks, h.Security.Entries, h.Security.Hazards) {
			if h.Names[id] == "" {
				h.Names[id] = firstOf(m.Names[id], haName(id))
			}
		}
	}
	if e := m.Energy; e != nil && tab("energy") {
		h.Energy.SolarPower = exists("energy", e.SolarPower)
		h.Energy.LoadPower = existing("energy", e.LoadPower)
		h.Energy.SolarToday = exists("energy", e.SolarToday)
		h.Energy.UsedToday = exists("energy", e.UsedToday)
		h.Energy.Consumers = visible("energy", e.Consumers)
		h.Energy.WaterFlow = exists("energy", e.WaterFlow)
		h.Energy.WaterToday = exists("energy", e.WaterToday)
		for _, id := range h.Energy.Consumers {
			if h.Names[id] == "" {
				h.Names[id] = firstOf(m.Names[id], haName(id))
			}
		}
	}
	if mu := m.Music; mu != nil && tab("music") {
		h.Speakers = visible("music", mu.Speakers)
		for _, id := range h.Speakers {
			if h.Names[id] == "" {
				h.Names[id] = firstOf(m.Names[id], haName(id))
			}
		}
	}
	// The owner's renames win over everything.
	for _, id := range sortedKeys(c.Entities) {
		if n := c.Entities[id].Name; n != "" && d.States[id] != nil {
			h.Names[id] = n
		}
	}
	return h, problems
}

// compare orders two things: an explicit order (the owner's) first, then a rank (the mapping's); 0 when neither
// says.
func compare(oa, ob *int, ranks map[string]int, a, b string) int {
	switch {
	case oa != nil && ob != nil:
		return *oa - *ob
	case oa != nil:
		return -1
	case ob != nil:
		return 1
	}
	ra, okA := ranks[a]
	rb, okB := ranks[b]
	switch {
	case okA && okB:
		return ra - rb
	case okA:
		return -1
	case okB:
		return 1
	}
	return 0
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
