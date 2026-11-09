package house

import (
	"strings"
	"testing"
)

func widgetIDs(ws []Widget) []string {
	out := []string{}
	for _, w := range ws {
		out = append(out, w.ID)
	}
	return out
}

func itemIDs(w Widget) []string {
	out := []string{}
	for _, it := range w.Items {
		out = append(out, it.ID)
	}
	return out
}

func widget(ws []Widget, id string) Widget {
	for _, w := range ws {
		if w.ID == id {
			return w
		}
	}
	return Widget{}
}

// With nothing in the file, the widgets are the house's scenes, a Home widget and one per room with lights or scenes.
func TestWidgetsComeByThemselves(t *testing.T) {
	h, problems := Render(Mapping{}, smallHome(), Config{})
	eq(t, "problems", problems, []string(nil))
	ids := widgetIDs(h.Widgets)
	if !contains(ids, "scenes") || !contains(ids, "home") || !contains(ids, "room:kitchen") {
		t.Fatalf("widgets: %v", ids)
	}
	scenes := widget(h.Widgets, "scenes")
	if !contains(itemIDs(scenes), "scene.movie") || scenes.Items[0].Kind != "scene" || scenes.Items[0].Action.Service != "turn_on" {
		t.Errorf("the scenes widget: %+v", scenes.Items)
	}
	kitchen := widget(h.Widgets, "room:kitchen")
	if !contains(itemIDs(kitchen), "lights:kitchen") {
		t.Fatalf("the kitchen widget: %v", itemIDs(kitchen))
	}
	lights := kitchen.Items[0]
	if lights.Kind != "lights" || !contains(lights.Entities, "light.kitchen_group") || lights.Action.Service != "turn_off" || lights.Action.Data["entity_id"] == nil {
		t.Errorf("the kitchen's lights item: %+v", lights)
	}
	for _, w := range h.Widgets {
		if len(w.Items) == 0 {
			t.Errorf("widget %s has no items", w.ID)
		}
	}
}

// The file's widgets: every kind of item, the per-size lists, and what cannot be used reported, not fatal.
func TestTheFileMapsWidgets(t *testing.T) {
	m, err := ParseMapping([]byte(strings.TrimSpace(`
version: 2
widgets:
  - id: evening
    name: Evening
    items:
      - scene: scene.movie
      - entity: light.kitchen_island
      - lights: kitchen
      - readout: sensor.shed_pipe_temperature
      - alarm
      - solar
      - { name: Matrix PS5, icon: gamecontroller, action: script.matrix_ps5 }
      - scene: scene.nothing
      - entity: sensor.shed_pipe_temperature
      - lights: attic
    small: [scene.movie]
    medium: [scene.movie, kitchen, Matrix PS5, nowhere]
`)))
	if err != nil {
		t.Fatal(err)
	}
	h, problems := Render(m, smallHome(), Config{})
	eq(t, "widgets", widgetIDs(h.Widgets), []string{"evening"})
	w := h.Widgets[0]
	eq(t, "items", itemIDs(w), []string{"scene.movie", "light.kitchen_island", "lights:kitchen", "sensor.shed_pipe_temperature", "alarm", "button:matrix_ps5"})
	eq(t, "small", w.Small, []string{"scene.movie"})
	eq(t, "medium", w.Medium, []string{"scene.movie", "lights:kitchen", "button:matrix_ps5"})
	kinds := []string{}
	for _, it := range w.Items {
		kinds = append(kinds, it.Kind)
	}
	eq(t, "kinds", kinds, []string{"scene", "toggle", "lights", "readout", "alarm", "button"})
	if w.Items[1].Action.Service != "toggle" || w.Items[3].Unit != "°C" || w.Items[3].Icon != "thermometer.medium" || w.Items[5].Action.Entity != "script.matrix_ps5" || w.Items[5].Icon != "gamecontroller" {
		t.Errorf("items: %+v", w.Items)
	}
	want := []string{
		"widget Evening: the house has no solar sensor",
		"widget Evening: Home Assistant has no scene.nothing",
		"widget Evening: sensor.shed_pipe_temperature cannot be toggled; use readout: for a sensor, or a button with an action",
		"widget Evening: no room attic",
		"widget Evening: medium names nowhere, which is not one of its items",
	}
	eq(t, "problems", problems, want)
}

// A word that is not one of the two is refused when the file is read.
func TestAWidgetItemMustBeAKnownForm(t *testing.T) {
	_, err := ParseMapping([]byte("version: 2\nwidgets:\n  - name: X\n    items: [camera]\n"))
	if err == nil || !strings.Contains(err.Error(), "widget item") {
		t.Errorf("err = %v", err)
	}
}
