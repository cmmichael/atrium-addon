package house

import (
	"fmt"
	"sort"
)

// Render turns the declared mapping into the layout the apps draw, with Home Assistant's names, the owner's
// in-app preferences on top, and a list of what in the mapping could not be used (entities Home Assistant does
// not have, floors that are not declared), so the set-up page and the log can say so.
func Render(m Mapping, d Dump, c Config) (House, []string) {
	var problems []string
	problem := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	// Names: Home Assistant's friendly names, else the registry's, else the id.
	registry := map[string]map[string]any{}
	for _, e := range d.Entities {
		if id, _ := e["entity_id"].(string); id != "" {
			registry[id] = e
		}
	}
	haName := func(id string) string {
		if st := d.States[id]; st != nil {
			if n := st.str("friendly_name"); n != "" {
				return n
			}
		}
		if e := registry[id]; e != nil {
			if n, _ := e["name"].(string); n != "" {
				return n
			}
			if n, _ := e["original_name"].(string); n != "" {
				return n
			}
		}
		return pretty(id)
	}
	areaName := map[string]string{}
	for _, a := range d.Areas {
		if id, _ := a["area_id"].(string); id != "" {
			areaName[id], _ = a["name"].(string)
		}
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
	existing := func(where string, ids []string) []string {
		out := []string{}
		for _, id := range ids {
			if id = exists(where, id); id != "" && !contains(out, id) {
				out = append(out, id)
			}
		}
		return out
	}
	hidden := func(id string) bool { return c.Entities[id].Hidden }
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

	// Floors: the mapping's in its order, then the owner's own, then "Elsewhere" if a room needs it.
	known := map[string]bool{}
	for _, f := range m.Floors {
		if known[f.ID] {
			continue
		}
		known[f.ID] = true
		h.Floors = append(h.Floors, Floor{f.ID, firstOf(c.Floors[f.ID].Name, f.Name, pretty(f.ID))})
	}
	for _, id := range sortedKeys(c.Floors) {
		if !known[id] {
			known[id] = true
			h.Floors = append(h.Floors, Floor{id, firstOf(c.Floors[id].Name, pretty(id))})
		}
	}

	// Where the owner moved entities to, by room id.
	moved := map[string][]string{}
	for _, id := range sortedKeys(c.Entities) {
		if ec := c.Entities[id]; ec.Room != "" && d.States[id] != nil {
			moved[ec.Room] = append(moved[ec.Room], id)
		}
	}
	movedAway := func(id string) bool { return c.Entities[id].Room != "" }

	roomOrderKey := map[string]int{}
	lastFloor := ""
	if len(h.Floors) > 0 {
		lastFloor = h.Floors[len(h.Floors)-1].ID
	}
	for _, rm := range m.Rooms {
		rc := c.Rooms[rm.ID]
		where := "room " + rm.ID
		var ids, hiddenHere []string
		add := func(id string) {
			if id != "" && !contains(ids, id) {
				ids = append(ids, id)
			}
		}
		for _, id := range existing(where, rm.Entities) {
			switch {
			case hidden(id):
				hiddenHere = append(hiddenHere, id)
			case movedAway(id):
			default:
				add(id)
			}
		}
		if t := exists(where, firstOf(rc.Thermostat, rm.Thermostat)); t != "" && !hidden(t) {
			add(t)
		}
		for _, id := range moved[rm.ID] {
			if !hidden(id) {
				add(id)
			}
		}
		ids = ordered(ids, rc.Entities)
		name := firstOf(rc.Name, rm.Name, areaName[rm.ID], pretty(rm.ID))
		floor := firstOf(rc.Floor, rm.Floor, lastFloor)
		if floor != "" && !known[floor] {
			if rm.Floor == floor {
				problem("%s: floor %q is not in floors", where, floor)
			}
			floor = ""
		}
		if floor == "" {
			floor = "other"
		}
		room := Room{ID: rm.ID, Name: name, Floor: floor, Icon: firstOf(rm.Icon, iconFor(name)), Entities: ids,
			Temperature: exists(where, rm.Temperature), Humidity: exists(where, rm.Humidity), Hidden: hiddenHere}
		if room.Entities == nil {
			room.Entities = []string{}
		}
		if rc.Hidden || len(ids) == 0 {
			h.HiddenRooms = append(h.HiddenRooms, room) // hidden, or nothing left to show: the editor can bring it back
			continue
		}
		h.Rooms = append(h.Rooms, room)
		if rc.Order != nil {
			roomOrderKey[rm.ID] = *rc.Order
		}
		for _, id := range ids {
			if domain(id) == "media_player" {
				h.Names[id] = firstOf(m.Names[id], name)
			} else {
				h.Names[id] = firstOf(m.Names[id], short(haName(id), name))
			}
		}
	}
	// The owner's moves to a room the mapping does not have make nothing; say so once.
	for _, id := range sortedKeys(moved) {
		found := false
		for _, rm := range m.Rooms {
			if rm.ID == id {
				found = true
			}
		}
		if !found {
			problem("preferences move %v to room %q, which the mapping does not have", moved[id], id)
		}
	}
	if !known["other"] {
		for _, r := range h.Rooms {
			if r.Floor == "other" {
				name := "Elsewhere"
				if len(h.Floors) == 0 {
					name = "Rooms"
				}
				h.Floors = append(h.Floors, Floor{"other", firstOf(c.Floors["other"].Name, name)})
				break
			}
		}
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
	// Rooms keep the mapping's order within a floor; the owner's order, where given, comes first.
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
		return false
	})

	// Scenes.
	for _, s := range m.Scenes {
		if id := exists("scenes", s.Entity); id != "" && !hidden(id) {
			h.Scenes = append(h.Scenes, SceneRef{Entity: id, Name: firstOf(c.Entities[id].Name, s.Name, m.Names[id], haName(id))})
		}
	}

	// The Home screen's readouts.
	h.Summary.Weather = exists("home", m.Home.Weather)
	h.Summary.OutdoorTemp = exists("home", m.Home.OutdoorTemperature)
	h.Summary.IndoorTemp = exists("home", m.Home.IndoorTemperature)
	h.Summary.Alarm = exists("home", m.Home.Alarm)
	h.Summary.People = existing("home", m.Home.People)

	// Security, Energy, Music: each only when the mapping has the section and the owner has not hidden the tab.
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
