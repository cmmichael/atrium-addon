package house

import (
	"fmt"
	"strings"
)

// Widgets: what the phone's Home Screen and Lock Screen show of the house. The file names the items; the add-on
// resolves each to a state to show and an action for a tap; the widget fits them to its size.

// autoWidgets is the default set: the house's scenes, a Home widget, and one widget per room that has lights or
// scenes. Built from the rendered house, so a hidden room or scene is not in them.
func autoWidgets(h House) []WidgetMapping {
	var out []WidgetMapping
	if len(h.Scenes) > 0 {
		w := WidgetMapping{ID: "scenes", Name: "Scenes"}
		for _, s := range h.Scenes {
			if s.Room == "" {
				w.Items = append(w.Items, WidgetItemMapping{Scene: s.Entity})
			}
		}
		if len(w.Items) == 0 {
			for _, s := range h.Scenes {
				w.Items = append(w.Items, WidgetItemMapping{Scene: s.Entity})
			}
		}
		out = append(out, w)
	}
	home := WidgetMapping{ID: "home", Name: "Home"}
	if h.Summary.Alarm != "" {
		home.Items = append(home.Items, WidgetItemMapping{Word: "alarm"})
	}
	if h.Energy.SolarPower != "" {
		home.Items = append(home.Items, WidgetItemMapping{Word: "solar"})
	}
	if h.Summary.OutdoorTemp != "" {
		home.Items = append(home.Items, WidgetItemMapping{Readout: h.Summary.OutdoorTemp})
	}
	for _, r := range h.Rooms {
		if len(lightsOf(r)) > 0 {
			home.Items = append(home.Items, WidgetItemMapping{Lights: r.ID})
		}
	}
	if len(home.Items) > 0 {
		out = append(out, home)
	}
	for _, r := range h.Rooms {
		w := WidgetMapping{ID: "room:" + r.ID, Name: r.Name}
		if len(lightsOf(r)) > 0 {
			w.Items = append(w.Items, WidgetItemMapping{Lights: r.ID})
		}
		for _, s := range h.Scenes {
			if s.Room == r.ID {
				w.Items = append(w.Items, WidgetItemMapping{Scene: s.Entity})
			}
		}
		for _, id := range r.Entities {
			if d := domain(id); d == "switch" || d == "fan" || d == "input_boolean" {
				w.Items = append(w.Items, WidgetItemMapping{Entity: id})
			}
		}
		if r.Temperature != "" {
			w.Items = append(w.Items, WidgetItemMapping{Readout: r.Temperature})
		}
		if len(w.Items) > 1 || (len(w.Items) == 1 && w.Items[0].Readout == "") {
			out = append(out, w)
		}
	}
	return out
}

func lightsOf(r Room) []string {
	var out []string
	for _, id := range r.Entities {
		if domain(id) == "light" {
			out = append(out, id)
		}
	}
	return out
}

// renderWidget resolves one widget of the file against the rendered house. Items Home Assistant does not have are
// dropped and reported; a widget with nothing left is dropped.
func renderWidget(wm WidgetMapping, h House, d Dump, hidden func(string) bool) (Widget, []string) {
	var problems []string
	name := firstOf(wm.Name, wm.ID, "Widget")
	problem := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf("widget %s: %s", name, fmt.Sprintf(format, args...)))
	}
	w := Widget{ID: firstOf(wm.ID, slugOf(name)), Name: name}
	exists := func(id string) bool {
		if id != "" && d.States[id] != nil {
			return true
		}
		problem("Home Assistant has no %s", id)
		return false
	}
	display := func(id string) string {
		if n := h.Names[id]; n != "" {
			return n
		}
		if st := d.States[id]; st != nil && st.str("friendly_name") != "" {
			return st.str("friendly_name")
		}
		return pretty(id)
	}
	rooms := map[string]Room{}
	for _, r := range h.Rooms {
		rooms[r.ID] = r
	}
	scenes := map[string]SceneRef{}
	for _, s := range h.Scenes {
		scenes[s.Entity] = s
	}
	seen := map[string]bool{}
	add := func(it WidgetItem) {
		if seen[it.ID] {
			problem("%s is listed twice", it.ID)
			return
		}
		seen[it.ID] = true
		w.Items = append(w.Items, it)
	}
	for _, im := range wm.Items {
		switch {
		case im.Word == "alarm":
			if h.Summary.Alarm == "" {
				problem("the house has no alarm")
				continue
			}
			add(WidgetItem{ID: "alarm", Kind: "alarm", Name: "Alarm", Icon: "shield.fill", Entity: h.Summary.Alarm})
		case im.Word == "solar":
			if h.Energy.SolarPower == "" {
				problem("the house has no solar sensor")
				continue
			}
			add(WidgetItem{ID: "solar", Kind: "readout", Name: "Solar", Icon: "sun.max.fill", Entity: h.Energy.SolarPower, Unit: unitOf(d, h.Energy.SolarPower)})
		case im.Scene != "":
			if !exists(im.Scene) {
				continue
			}
			if dom := domain(im.Scene); dom != "scene" && dom != "script" {
				problem("%s is not a scene or a script", im.Scene)
				continue
			}
			if hidden(im.Scene) {
				continue
			}
			n := firstOf(scenes[im.Scene].Name, display(im.Scene))
			add(WidgetItem{ID: im.Scene, Kind: "scene", Name: n, Icon: "sparkles", Entity: im.Scene,
				Action: &Action{Domain: domain(im.Scene), Service: "turn_on", Entity: im.Scene}})
		case im.Entity != "":
			if !exists(im.Entity) || hidden(im.Entity) {
				continue
			}
			dom := domain(im.Entity)
			icon, ok := toggleIcons[dom]
			if !ok {
				problem("%s cannot be toggled; use readout: for a sensor, or a button with an action", im.Entity)
				continue
			}
			add(WidgetItem{ID: im.Entity, Kind: "toggle", Name: display(im.Entity), Icon: icon, Entity: im.Entity,
				Action: &Action{Domain: dom, Service: "toggle", Entity: im.Entity}})
		case im.Lights != "":
			r, ok := rooms[im.Lights]
			if !ok {
				problem("no room %s", im.Lights)
				continue
			}
			lights := lightsOf(r)
			if len(lights) == 0 {
				problem("room %s has no lights", r.Name)
				continue
			}
			ids := make([]any, len(lights))
			for i, l := range lights {
				ids[i] = l
			}
			add(WidgetItem{ID: "lights:" + r.ID, Kind: "lights", Name: r.Name, Icon: "lightbulb.fill", Entities: lights,
				Action: &Action{Domain: "light", Service: "turn_off", Data: map[string]any{"entity_id": ids}}})
		case im.Readout != "":
			if !exists(im.Readout) {
				continue
			}
			add(WidgetItem{ID: im.Readout, Kind: "readout", Name: display(im.Readout), Icon: readoutIcon(d, im.Readout), Entity: im.Readout, Unit: unitOf(d, im.Readout)})
		case im.Action != nil:
			if im.Name == "" {
				problem("a button needs a name")
				continue
			}
			a, err := im.Action.action()
			if err != nil {
				problem("button %s: %v", im.Name, err)
				continue
			}
			if a.Entity != "" && !exists(a.Entity) {
				continue
			}
			add(WidgetItem{ID: "button:" + slugOf(im.Name), Kind: "button", Name: im.Name, Icon: firstOf(im.Icon, "circle.fill"), Action: &a})
		default:
			problem("an item needs scene:, entity:, lights:, readout:, alarm, solar or an action")
		}
	}
	// The per-size lists refer to items by the ids given in the file; a room's lights by the room's id.
	resolve := func(size string, refs []string) []string {
		var out []string
		for _, ref := range refs {
			id := ref
			switch {
			case seen["lights:"+ref]:
				id = "lights:" + ref
			case seen["button:"+slugOf(ref)]:
				id = "button:" + slugOf(ref)
			}
			if !seen[id] {
				problem("%s names %s, which is not one of its items", size, ref)
				continue
			}
			out = append(out, id)
		}
		return out
	}
	w.Small, w.Medium, w.Large, w.Lock = resolve("small", wm.Small), resolve("medium", wm.Medium), resolve("large", wm.Large), resolve("lock", wm.Lock)
	return w, problems
}

var toggleIcons = map[string]string{"light": "lightbulb.fill", "switch": "power", "fan": "fan.fill", "input_boolean": "switch.2", "media_player": "hifispeaker.fill"}

func unitOf(d Dump, id string) string {
	if st := d.States[id]; st != nil {
		return st.str("unit_of_measurement")
	}
	return ""
}

func readoutIcon(d Dump, id string) string {
	class := ""
	if st := d.States[id]; st != nil {
		class = st.str("device_class")
	}
	switch class {
	case "temperature":
		return "thermometer.medium"
	case "humidity":
		return "humidity.fill"
	case "power", "energy":
		return "bolt.fill"
	case "battery":
		return "battery.75percent"
	case "illuminance":
		return "sun.max.fill"
	}
	return "gauge.with.dots.needle.33percent"
}

func slugOf(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "_"):
			b.WriteRune('_')
		}
	}
	return strings.TrimSuffix(b.String(), "_")
}
