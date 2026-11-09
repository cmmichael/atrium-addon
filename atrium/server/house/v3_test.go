package house

import (
	"strings"
	"testing"
	"time"
)

// A version 3 file: every section resolved into the house, the rules applied, what cannot be used reported.
func TestVersionThreeSectionsAreResolved(t *testing.T) {
	d := smallHome()
	add := func(id, state, attrs string) {
		d.Entities = append(d.Entities, map[string]any{"entity_id": id, "platform": "demo"})
		d.States[id] = &State{State: state, Attributes: j(attrs).(map[string]any)}
	}
	add("sensor.water_flow", "0.4", `{"unit_of_measurement":"gal/min"}`)
	add("sensor.water_today", "71", `{"unit_of_measurement":"gal"}`)
	add("switch.water_valve", "on", `{}`)
	add("sensor.water_mode", "home", `{}`)
	add("binary_sensor.leak_laundry", "off", `{"device_class":"moisture"}`)
	add("sensor.kitchen_co2", "650", `{"unit_of_measurement":"ppm"}`)
	add("sensor.dishwasher_status", "running", `{}`)
	add("binary_sensor.dishwasher_door", "off", `{}`)
	add("light.tree", "off", `{}`)
	add("device_tracker.car", "home", `{}`)
	add("sensor.car_battery", "64", `{"unit_of_measurement":"%"}`)
	add("binary_sensor.car_awake", "off", `{}`)
	add("button.car_flash", "unknown", `{}`)
	add("sensor.pool_air", "70", `{"unit_of_measurement":"°F"}`)
	add("switch.pool_pump", "on", `{}`)
	add("calendar.family", "off", `{}`)
	add("calendar.birthdays", "off", `{}`)
	add("sensor.travel_school", "18", `{"unit_of_measurement":"min"}`)
	add("input_boolean.bins_out", "off", `{}`)
	add("input_boolean.shades_schedule", "on", `{}`)
	add("input_datetime.shades_open", "07:00:00", `{}`)
	add("sensor.beans_left", "40", `{"unit_of_measurement":"%"}`)
	add("automation.stuffy_fan", "on", `{}`)

	m, err := ParseMapping([]byte(strings.TrimSpace(`
version: 3
home:
  attention: { battery_low: 20, offline: true, open_too_long: { garage: 30, windows: 5 }, include: [binary_sensor.smoke, binary_sensor.nothing] }
energy:
  water:
    flow: sensor.water_flow
    today: sensor.water_today
    valve: { entity: switch.water_valve, openOnly: true }
    mode: sensor.water_mode
    alerts: [binary_sensor.leak_laundry]
climate:
  thermostats: [{ entity: climate.hall, serves: [kitchen, shed, attic] }]
  air: [{ room: kitchen, co2: sensor.kitchen_co2 }, { co2: sensor.kitchen_co2 }]
  bands: { co2: { fair: 900, poor: 1300 }, smoke: { fair: 1, poor: 2 } }
appliances:
  - { name: Dishwasher, room: kitchen, status: sensor.dishwasher_status, door: binary_sensor.dishwasher_door, actions: [{ name: Start, action: script.matrix_ps5 }] }
seasonal:
  - { name: Tree, room: bed, from: 11-15, until: 01-10, lights: [light.tree] }
  - { name: Bad, from: 13-01, until: 01-10, lights: [light.tree] }
vehicles:
  - name: Car
    tracker: device_tracker.car
    battery: sensor.car_battery
    low: 20
    awake: binary_sensor.car_awake
    actions: [{ name: Flash, icon: lightbulb, action: button.car_flash }]
    charger: { power: sensor.nothing }
pool:
  air_temperature: sensor.pool_air
  pump: { switch: switch.pool_pump }
  readOnly: [switch.pool_pump]
day:
  calendars: [calendar.family, calendar.birthdays]
  hide: [calendar.birthdays]
  drives: [{ name: School run, sensor: sensor.travel_school, days: weekdays, at: "07:40" }, { name: Sunday, sensor: sensor.travel_school, days: [7], at: "9:00" }]
  reminders: [{ title: Bins out, entity: input_boolean.bins_out, weekday: 3, from: "18:00", until: "08:00", severity: attention, action: { name: Check, action: script.matrix_ps5 } }]
  schedules: [{ name: Shades, enabled: input_boolean.shades_schedule, times: { Open: input_datetime.shades_open, Close: input_datetime.nothing } }]
panels:
  - { name: Coffee, icon: cup.and.saucer, room: kitchen, rows: [{ entity: sensor.beans_left, name: Beans }, { entity: automation.stuffy_fan }], actions: [{ name: Brew, action: script.matrix_ps5 }] }
`)))
	if err != nil {
		t.Fatal(err)
	}
	h, problems := Render(m, d, Config{})
	// Rendered the same whatever the day, but the season flag is today's: check it against the clock.
	eq(t, "attention", *h.Summary.Attention, Attention{BatteryLow: 20, Offline: true, OpenTooLong: map[string]int{"garage": 30, "doors": 10, "car_doors": 10}, Include: []string{"binary_sensor.smoke"}})
	eq(t, "water", *h.Energy.Water, Water{Flow: "sensor.water_flow", Today: "sensor.water_today", Valve: &Valve{Entity: "switch.water_valve", OpenOnly: true}, Mode: "sensor.water_mode", Alerts: []string{"binary_sensor.leak_laundry"}})
	eq(t, "the old readouts are filled from the card", []string{h.Energy.WaterFlow, h.Energy.WaterToday}, []string{"sensor.water_flow", "sensor.water_today"})
	eq(t, "the valve is under security", h.Security.Valves, []Valve{{Entity: "switch.water_valve", OpenOnly: true}})
	eq(t, "thermostats", h.Climate.Thermostats, []Thermostat{{Entity: "climate.hall", Serves: []string{"kitchen", "shed"}}})
	eq(t, "bands", h.Climate.Bands["co2"], Band{900, 1300})
	eq(t, "air", h.Climate.Air, []Air{{Room: "kitchen", CO2: "sensor.kitchen_co2"}})
	var kitchen, shed Room
	for _, r := range h.Rooms {
		if r.ID == "kitchen" {
			kitchen = r
		}
		if r.ID == "shed" {
			shed = r
		}
	}
	// The kitchen has a thermostat of its own, which wins; the shed has none and gets the hall's.
	if contains(kitchen.Entities, "climate.hall") || !contains(shed.Entities, "climate.hall") {
		t.Errorf("serves: kitchen %v, shed %v", kitchen.Entities, shed.Entities)
	}
	eq(t, "appliances", h.Appliances, []Appliance{{Name: "Dishwasher", Room: "kitchen", Status: "sensor.dishwasher_status", Door: "binary_sensor.dishwasher_door", Finished: defaultFinished,
		Actions: []RemoteButton{{Name: "Start", Action: Action{Domain: "script", Service: "turn_on", Entity: "script.matrix_ps5"}}}}})
	if len(h.Seasonal) != 1 || h.Seasonal[0].Room != "bed" || h.Seasonal[0].Active != inSeason(time.Now(), 1115, 110) {
		t.Errorf("seasonal: %+v", h.Seasonal)
	}
	eq(t, "vehicles", h.Vehicles, []Vehicle{{Name: "Car", Tracker: "device_tracker.car", Battery: "sensor.car_battery", Low: 20, Awake: "binary_sensor.car_awake",
		Doors: []string{}, Windows: []string{}, Openings: []string{}, Tyres: []string{}, Warnings: []string{},
		Actions: []RemoteButton{{Name: "Flash", Icon: "lightbulb", Action: Action{Domain: "button", Service: "press", Entity: "button.car_flash"}}}, Charger: &Charger{}}})
	eq(t, "pool", *h.Pool, Pool{AirTemperature: "sensor.pool_air", Pump: &Pump{Switch: "switch.pool_pump"}, Heaters: []string{}, Lights: []string{}, ReadOnly: []string{"switch.pool_pump"}})
	eq(t, "calendars", h.Day.Calendars, []string{"calendar.family"})
	eq(t, "drives", h.Day.Drives, []Drive{{Name: "School run", Sensor: "sensor.travel_school", Days: []int{1, 2, 3, 4, 5}, At: "07:40"}, {Name: "Sunday", Sensor: "sensor.travel_school", Days: []int{7}}})
	eq(t, "reminders", h.Day.Reminders, []Reminder{{Title: "Bins out", Entity: "input_boolean.bins_out", Weekday: 3, From: "18:00", Until: "08:00", Severity: "attention",
		Action: &RemoteButton{Name: "Check", Action: Action{Domain: "script", Service: "turn_on", Entity: "script.matrix_ps5"}}}})
	eq(t, "schedules", h.Day.Schedules, []Schedule{{Name: "Shades", Enabled: "input_boolean.shades_schedule", Times: map[string]string{"Open": "input_datetime.shades_open"}}})
	eq(t, "panels", h.Panels, []Panel{{ID: "coffee", Name: "Coffee", Icon: "cup.and.saucer", Room: "kitchen",
		Rows:    []Row{{Entity: "sensor.beans_left", Name: "Beans"}, {Entity: "automation.stuffy_fan", Name: "Stuffy fan", Toggle: true}},
		Actions: []RemoteButton{{Name: "Brew", Action: Action{Domain: "script", Service: "turn_on", Entity: "script.matrix_ps5"}}}}})
	want := []string{
		"attention: Home Assistant has no binary_sensor.nothing",
		"attention: open_too_long knows garage, doors and car_doors, not windows",
		"climate: bands knows co2, pm2_5, voc and aqi, not smoke",
		"climate: no room attic",
		"air: an entry needs a room",
		"seasonal Bad: needs a name, from and until as month-day (11-15)",
		"vehicle Car: Home Assistant has no sensor.nothing",
		"drive Sunday: at \"9:00\" is not a time (HH:MM)",
		"schedule Shades: Home Assistant has no input_datetime.nothing",
	}
	eq(t, "problems", problems, want)
}

// A file that uses version 3 sections must say so; an unknown version is refused.
func TestVersionThreeMustBeDeclared(t *testing.T) {
	_, err := ParseMapping([]byte("version: 2\npool:\n  spa: switch.spa\n"))
	if err == nil || !strings.Contains(err.Error(), "write version: 3") {
		t.Errorf("err = %v", err)
	}
	_, err = ParseMapping([]byte("version: 4\n"))
	if err == nil || !strings.Contains(err.Error(), "versions 1, 2 and 3") {
		t.Errorf("err = %v", err)
	}
	_, err = ParseMapping([]byte("version: 3\nday:\n  drives: [{ name: X, sensor: sensor.x, days: [0] }]\n"))
	if err == nil || !strings.Contains(err.Error(), "ISO weekday") {
		t.Errorf("err = %v", err)
	}
	if _, err = ParseMapping([]byte("version: 3\nday:\n  drives: [{ name: X, sensor: sensor.x, days: daily }]\n")); err != nil {
		t.Errorf("daily: %v", err)
	}
}

func TestSeasonsWrapTheYear(t *testing.T) {
	dec := time.Date(2026, 12, 20, 0, 0, 0, 0, time.UTC)
	jan := time.Date(2027, 1, 5, 0, 0, 0, 0, time.UTC)
	jun := time.Date(2027, 6, 5, 0, 0, 0, 0, time.UTC)
	if !inSeason(dec, 1115, 110) || !inSeason(jan, 1115, 110) || inSeason(jun, 1115, 110) || !inSeason(jun, 601, 831) {
		t.Error("seasons")
	}
	if monthDay("13-01") != 0 || monthDay("11-15") != 1115 || monthDay("x") != 0 {
		t.Error("monthDay")
	}
}
