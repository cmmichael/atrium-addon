package house

import (
	"encoding/json"
	"strings"
	"testing"
)

// A second house, unlike the first in every way the add-on might quietly assume: a flat in Europe, metric, euros,
// Zigbee lights through ZHA, Frigate and Reolink cameras, Google Cast speakers, no UniFi, no Sonos, no Music
// Assistant, no Tailscale-era doorbell, and no layout file. Nothing here is anyone's real home.
func lindenFlat() Dump {
	d := Dump{Name: "Linden Flat", Currency: "EUR", States: map[string]*State{}, Running: true}
	d.Floors = nil // a flat: Home Assistant has no floors at all
	d.Areas = list(`[{"area_id":"wohnzimmer","name":"Wohnzimmer"},{"area_id":"kueche","name":"Küche"},{"area_id":"schlafzimmer","name":"Schlafzimmer"},{"area_id":"flur","name":"Flur"},{"area_id":"balkon","name":"Balkon"}]`)
	d.Devices = list(`[{"id":"z1","area_id":"wohnzimmer"},{"id":"z2","area_id":"kueche"},{"id":"cam-balkon","area_id":"balkon"},{"id":"cam-flur","area_id":"flur"},{"id":"nest-mini","area_id":"kueche"}]`)
	reg := func(id, area, device, platform string) map[string]any {
		e := map[string]any{"entity_id": id, "platform": platform}
		if area != "" {
			e["area_id"] = area
		}
		if device != "" {
			e["device_id"] = device
		}
		return e
	}
	d.Entities = []map[string]any{
		reg("light.stehlampe", "", "z1", "zha"), reg("light.decke_wohnzimmer", "wohnzimmer", "", "zha"),
		reg("light.kueche", "", "z2", "zha"), reg("light.nachttisch", "schlafzimmer", "", "hue"),
		reg("climate.heizung_wohnzimmer", "wohnzimmer", "", "tado"), reg("climate.heizung_schlafzimmer", "schlafzimmer", "", "tado"),
		reg("sensor.wohnzimmer_temperatur", "wohnzimmer", "", "zha"), reg("sensor.wohnzimmer_luftfeuchtigkeit", "wohnzimmer", "", "zha"),
		reg("cover.rollladen_schlafzimmer", "schlafzimmer", "", "shelly"),
		reg("camera.balkon", "", "cam-balkon", "frigate"), reg("binary_sensor.balkon_person_occupancy", "", "cam-balkon", "frigate"),
		reg("camera.flur", "", "cam-flur", "reolink"), reg("binary_sensor.flur_visitor", "", "cam-flur", "reolink"),
		reg("binary_sensor.wohnungstuer", "flur", "", "zha"), reg("lock.wohnungstuer", "flur", "", "nuki"),
		reg("binary_sensor.rauchmelder_flur", "flur", "", "zha"),
		reg("media_player.kueche", "", "nest-mini", "cast"), reg("media_player.wohnzimmer_tv", "wohnzimmer", "", "cast"),
		reg("scene.abend", "wohnzimmer", "", "hue"),
	}
	st := func(id, state, attrs string) {
		a, _ := j(attrs).(map[string]any)
		d.States[id] = &State{State: state, Attributes: a}
	}
	st("light.stehlampe", "on", `{"friendly_name":"Stehlampe"}`)
	st("light.decke_wohnzimmer", "off", `{"friendly_name":"Wohnzimmer Decke"}`)
	st("light.kueche", "off", `{"friendly_name":"Küche"}`)
	st("light.nachttisch", "off", `{"friendly_name":"Nachttisch"}`)
	st("climate.heizung_wohnzimmer", "heat", `{"friendly_name":"Heizung Wohnzimmer","min_temp":5,"max_temp":25,"temperature":21,"current_temperature":20.5}`)
	st("climate.heizung_schlafzimmer", "heat", `{"friendly_name":"Heizung Schlafzimmer","min_temp":5,"max_temp":25,"temperature":18}`)
	st("sensor.wohnzimmer_temperatur", "20.5", `{"friendly_name":"Wohnzimmer Temperatur","device_class":"temperature","unit_of_measurement":"°C"}`)
	st("sensor.wohnzimmer_luftfeuchtigkeit", "48", `{"friendly_name":"Wohnzimmer Luftfeuchtigkeit","device_class":"humidity","unit_of_measurement":"%"}`)
	st("cover.rollladen_schlafzimmer", "closed", `{"friendly_name":"Rollladen","device_class":"shutter"}`)
	st("camera.balkon", "idle", `{"friendly_name":"Balkon"}`)
	st("binary_sensor.balkon_person_occupancy", "off", `{"friendly_name":"Balkon Person","device_class":"occupancy"}`)
	st("camera.flur", "idle", `{"friendly_name":"Flur Fluent"}`)
	st("binary_sensor.flur_visitor", "off", `{"friendly_name":"Flur Besucher"}`)
	st("binary_sensor.wohnungstuer", "off", `{"friendly_name":"Wohnungstür","device_class":"door"}`)
	st("lock.wohnungstuer", "locked", `{"friendly_name":"Wohnungstür"}`)
	st("binary_sensor.rauchmelder_flur", "off", `{"friendly_name":"Rauchmelder Flur","device_class":"smoke"}`)
	st("media_player.kueche", "off", `{"friendly_name":"Küche Lautsprecher","device_class":"speaker"}`)
	st("media_player.wohnzimmer_tv", "off", `{"friendly_name":"Wohnzimmer TV","device_class":"tv"}`)
	st("scene.abend", "2026-03-01T19:00:00+00:00", `{"friendly_name":"Abend"}`)
	st("weather.forecast_home", "rainy", `{"temperature":9,"temperature_unit":"°C"}`)
	st("person.mira", "not_home", `{}`)
	return d
}

func TestASecondHouseUnlikeTheFirstNeedsNoFile(t *testing.T) {
	d := lindenFlat()
	h, problems := Render(Mapping{}, d, Config{})
	eq(t, "problems", problems, []string(nil))
	eq(t, "name", h.Name, "Linden Flat")
	eq(t, "rooms", names(h.Rooms), []string{"Flur", "Küche", "Schlafzimmer", "Wohnzimmer"}) // no floors: by name; the balcony has only a camera
	byName := map[string]Room{}
	for _, r := range h.Rooms {
		byName[r.Name] = r
	}
	eq(t, "living", byName["Wohnzimmer"].Entities, []string{"light.stehlampe", "light.decke_wohnzimmer", "climate.heizung_wohnzimmer"}) // the television is a remote, not a room control
	eq(t, "living temperature", byName["Wohnzimmer"].Temperature, "sensor.wohnzimmer_temperatur")
	eq(t, "living humidity", byName["Wohnzimmer"].Humidity, "sensor.wohnzimmer_luftfeuchtigkeit")
	eq(t, "cameras", h.Security.Cameras, []string{"camera.balkon", "camera.flur"})
	eq(t, "locks", h.Security.Locks, []string{"lock.wohnungstuer"})
	eq(t, "entries", h.Security.Entries, []string{"binary_sensor.wohnungstuer"})
	eq(t, "hazards", h.Security.Hazards, []string{"binary_sensor.rauchmelder_flur"})
	eq(t, "speakers", h.Speakers, []string{"media_player.kueche"})
	if h.MusicAssistant != nil {
		t.Errorf("no Music Assistant here: %+v", h.MusicAssistant)
	}
	eq(t, "scenes", h.Scenes, []SceneRef{{Entity: "scene.abend", Name: "Abend", Room: "wohnzimmer"}})
	// Nothing the apps get speaks of the first house's makers or money.
	data, _ := json.Marshal(h)
	for _, word := range []string{"unifi", "sonos", "music_assistant", "$"} {
		if strings.Contains(strings.ToLower(string(data)), word) {
			t.Errorf("the second house's JSON mentions %q", word)
		}
	}
}

func TestASecondHouseBillsInItsOwnCurrency(t *testing.T) {
	d := lindenFlat()
	d.Entities = append(d.Entities, map[string]any{"entity_id": "sensor.strom_netto", "platform": "demo"})
	d.States["sensor.strom_netto"] = &State{State: "112", Attributes: map[string]any{"unit_of_measurement": "kWh"}}
	m, err := ParseMapping([]byte("version: 3\nenergy:\n  tariff:\n    net: sensor.strom_netto\n    tiers: [{ rate: 0.32 }]\n"))
	if err != nil {
		t.Fatal(err)
	}
	h, problems := Render(m, d, Config{})
	eq(t, "problems", problems, []string(nil))
	if h.Energy.Tariff == nil || h.Energy.Tariff.Currency != "€" {
		t.Errorf("tariff: %+v", h.Energy.Tariff)
	}
}
