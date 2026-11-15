package house

import (
	"encoding/json"
	"sort"
	"testing"
)

// What a house looks like must not depend on what is on when it is built: the same house with every entity off
// or unavailable (keeping only what an entity declares about itself, not what it reports now) builds the same.
// A television that was off lost its remote's volume between two installs before this held.
func TestTheHouseDoesNotChangeWithTheMoment(t *testing.T) {
	declared := map[string]bool{"friendly_name": true, "device_class": true, "unit_of_measurement": true, "state_class": true,
		"supported_features": true, "min_temp": true, "max_temp": true, "hvac_modes": true, "supported_color_modes": true,
		"icon": true, "source": true, "meter_period": true, "options": true, "entity_picture": true, "restored": true}
	moment := func(d Dump, state string) Dump {
		out := d
		out.States = map[string]*State{}
		for id, s := range d.States {
			attrs := map[string]any{}
			for k, v := range s.Attributes {
				if declared[k] {
					attrs[k] = v
				}
			}
			out.States[id] = &State{State: state, Attributes: attrs, LastChanged: s.LastChanged}
		}
		return out
	}
	build := func(m Mapping, d Dump) string {
		h, _ := Render(m, d, Config{})
		// Scenes come most recently used first, on purpose: the order follows use, the list does not.
		sort.Slice(h.Scenes, func(i, j int) bool { return h.Scenes[i].Entity < h.Scenes[j].Entity })
		data, _ := json.MarshalIndent(h, "", " ")
		return string(data)
	}
	for name, c := range map[string]struct {
		m Mapping
		d Dump
	}{"small home": {Mapping{}, smallHome()}, "small home, mapped": {mapped(t, smallMapping), smallHome()}, "flat": {Mapping{}, lindenFlat()}} {
		want := build(c.m, c.d)
		for _, state := range []string{"off", "unavailable"} {
			if got := build(c.m, moment(c.d, state)); got != want {
				t.Errorf("%s: everything %s builds another house:\n%s", name, state, firstDifference(want, got))
			}
		}
	}
}

func firstDifference(a, b string) string {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	from := max(0, i-300)
	return "as built: …" + a[from:min(len(a), i+200)] + "\n  instead: …" + b[from:min(len(b), i+200)]
}
