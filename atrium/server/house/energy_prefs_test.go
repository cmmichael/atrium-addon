package house

import "testing"

// A home whose sensors carry no English word: the Energy dashboard's own preferences say which meters are solar,
// water and which devices to show, and the power sensor beside each meter on its device is what is drawn.
func TestTheEnergyDashboardNamesTheMeters(t *testing.T) {
	d := lindenFlat()
	d.Devices = append(d.Devices, list(`[{"id":"wr","area_id":"balkon"},{"id":"wm","area_id":"kueche"},{"id":"spuel","area_id":"kueche"},{"id":"wasser"}]`)...)
	reg := func(id, device, platform string) map[string]any {
		return map[string]any{"entity_id": id, "device_id": device, "platform": platform}
	}
	d.Entities = append(d.Entities,
		reg("sensor.wechselrichter_ertrag_gesamt", "wr", "solaredge"), reg("sensor.wechselrichter_leistung", "wr", "solaredge"),
		reg("sensor.waschmaschine_verbrauch", "wm", "shelly"), reg("sensor.waschmaschine_leistung", "wm", "shelly"),
		reg("sensor.spuelmaschine_verbrauch", "spuel", "shelly"), reg("sensor.spuelmaschine_leistung", "spuel", "shelly"),
		reg("sensor.wasserzaehler", "wasser", "zha"),
		reg("sensor.ertrag_heute", "", "utility_meter"), reg("sensor.wasser_heute", "", "utility_meter"),
		reg("sensor.ertrag_monat", "", "utility_meter"),
	)
	st := func(id, state, attrs string) {
		a, _ := j(attrs).(map[string]any)
		d.States[id] = &State{State: state, Attributes: a}
	}
	st("sensor.wechselrichter_ertrag_gesamt", "18234.5", `{"device_class":"energy","unit_of_measurement":"kWh","state_class":"total_increasing"}`)
	st("sensor.wechselrichter_leistung", "0", `{"device_class":"power","unit_of_measurement":"W"}`)
	st("sensor.waschmaschine_verbrauch", "312.1", `{"device_class":"energy","unit_of_measurement":"kWh"}`)
	st("sensor.waschmaschine_leistung", "0", `{"device_class":"power","unit_of_measurement":"W"}`)
	st("sensor.spuelmaschine_verbrauch", "120.9", `{"device_class":"energy","unit_of_measurement":"kWh"}`)
	st("sensor.spuelmaschine_leistung", "1800", `{"device_class":"power","unit_of_measurement":"W"}`)
	st("sensor.wasserzaehler", "812.3", `{"device_class":"water","unit_of_measurement":"m³"}`)
	st("sensor.ertrag_heute", "3.2", `{"device_class":"energy","unit_of_measurement":"kWh","source":"sensor.wechselrichter_ertrag_gesamt","meter_period":"daily"}`)
	st("sensor.ertrag_monat", "80.2", `{"device_class":"energy","unit_of_measurement":"kWh","source":"sensor.wechselrichter_ertrag_gesamt","meter_period":"monthly"}`)
	st("sensor.wasser_heute", "0.21", `{"device_class":"water","unit_of_measurement":"m³","source":"sensor.wasserzaehler","meter_period":"daily"}`)
	d.EnergyPrefs = j(`{"energy_sources":[{"type":"solar","stat_energy_from":"sensor.wechselrichter_ertrag_gesamt"},
		{"type":"water","stat_energy_from":"sensor.wasserzaehler"},{"type":"grid","flow_from":[{"stat_energy_from":"netz:bezug"}],"flow_to":[]}],
		"device_consumption":[{"stat_consumption":"sensor.waschmaschine_verbrauch"},{"stat_consumption":"sensor.spuelmaschine_verbrauch"},{"stat_consumption":"sensor.gone"}]}`).(map[string]any)

	h, _ := Render(Mapping{}, d, Config{})
	e := h.Energy
	if e.SolarPower != "sensor.wechselrichter_leistung" || e.SolarToday != "sensor.ertrag_heute" || e.WaterToday != "sensor.wasser_heute" {
		t.Errorf("solar %q, solar today %q, water today %q", e.SolarPower, e.SolarToday, e.WaterToday)
	}
	// The dashboard's devices in its order, whatever they draw now (the washing machine is idle).
	eq(t, "consumers", e.Consumers, []string{"sensor.waschmaschine_leistung", "sensor.spuelmaschine_leistung"})

	// A gateway with several power sensors on one device: the one sharing the meter's words.
	x := newIndex(Dump{Devices: list(`[{"id":"envoy"}]`), Entities: []map[string]any{
		reg("sensor.envoy_lifetime_energy_production", "envoy", "enphase_envoy"), reg("sensor.envoy_current_power_production", "envoy", "enphase_envoy"),
		reg("sensor.envoy_current_power_consumption", "envoy", "enphase_envoy"), reg("sensor.envoy_current_net_power_consumption", "envoy", "enphase_envoy"),
		reg("sensor.meter_a_energy", "envoy", "enphase_envoy"),
	}, States: map[string]*State{
		"sensor.envoy_lifetime_energy_production":    {Attributes: map[string]any{"device_class": "energy"}},
		"sensor.envoy_current_power_production":      {Attributes: map[string]any{"device_class": "power"}},
		"sensor.envoy_current_power_consumption":     {Attributes: map[string]any{"device_class": "power"}},
		"sensor.envoy_current_net_power_consumption": {Attributes: map[string]any{"device_class": "power"}},
		"sensor.meter_a_energy":                      {Attributes: map[string]any{"device_class": "energy"}},
	}}, nil)
	if got := x.powerOnDevice("sensor.envoy_lifetime_energy_production"); got != "sensor.envoy_current_power_production" {
		t.Errorf("the Envoy's production meter's power sensor: %q", got)
	}
	if got := x.powerOnDevice("sensor.meter_a_energy"); got != "" {
		t.Errorf("no sensor shares a word, so several tie and none is picked: %q", got)
	}
}
