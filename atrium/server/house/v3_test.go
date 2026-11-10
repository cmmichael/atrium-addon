package house

import (
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
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
	add("sensor.water_last_day", "2026-11-05T23:00:00+00:00", `{"device_class":"timestamp"}`)
	add("switch.water_valve", "on", `{}`)
	add("sensor.bill_net", "1230", `{"unit_of_measurement":"kWh","last_reset":"2026-10-27T07:00:00+00:00"}`)
	add("sensor.water_mode", "home", `{}`)
	add("binary_sensor.leak_laundry", "off", `{"device_class":"moisture"}`)
	add("sensor.kitchen_co2", "650", `{"unit_of_measurement":"ppm"}`)
	add("sensor.dishwasher_status", "running", `{}`)
	add("binary_sensor.dishwasher_door", "off", `{"device_class":"door"}`)
	add("binary_sensor.laundry_dryer_door", "on", `{"device_class":"door","friendly_name":"Tumble dryer Door"}`)
	add("conversation.gemini", "unknown", `{}`)
	add("conversation.not_an_agent", "unknown", `{}`)
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
  ask: conversation.gemini
  attention: { battery_low: 20, offline: true, open_too_long: { garage: 30, windows: 5 }, include: [binary_sensor.smoke, binary_sensor.nothing] }
energy:
  water:
    flow: sensor.water_flow
    today: sensor.water_today
    date: sensor.water_last_day
    valve: { entity: switch.water_valve, openOnly: true }
    mode: sensor.water_mode
    alerts: [binary_sensor.leak_laundry]
  tariff:
    net: sensor.bill_net
    period_days: 60
    period_start: "2026-08-27"
    tiers: [{ up_to: 700, rate: 0.26304 }, { up_to: 2100, rate: 0.32164 }, { rate: 0.40866 }]
    fixed: 45.40
    tax: 0.10
    estimate: true
    seasons:
      - { name: high, from: "06-01", to: "09-30", tiers: [{ up_to: 700, rate: 0.26304 }, { up_to: 2100, rate: 0.32164 }, { rate: 0.40866 }] }
      - { name: low, from: "10-01", to: "05-31", tiers: [{ up_to: 700, rate: 0.2729 }, { rate: 0.3315 }] }
climate:
  thermostats:
    - { entity: climate.hall, serves: [kitchen, shed, attic] }
    - { entity: climate.downstairs, action: setpoint }
    - { entity: climate.fridge, action: guess }
  air: [{ room: kitchen, co2: sensor.kitchen_co2 }, { co2: sensor.kitchen_co2 }]
  bands: { co2: { fair: 900, poor: 1300 }, smoke: { fair: 1, poor: 2 } }
appliances:
  - { name: Dishwasher, room: kitchen, status: sensor.dishwasher_status, door: binary_sensor.dishwasher_door, actions: [{ name: Start, action: script.matrix_ps5 }] }
  - { name: Washer, room: empty, status: sensor.dishwasher_status }
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
routines:
  leaving: { actions: [{ name: Lock up, icon: lock, action: script.matrix_ps5 }], check: [binary_sensor.leak_laundry, sensor.gone] }
  arriving: { actions: [] }
`)))
	if err != nil {
		t.Fatal(err)
	}
	h, problems := Render(m, d, Config{})
	// Rendered the same whatever the day, but the season flag is today's: check it against the clock.
	eq(t, "attention", *h.Summary.Attention, Attention{BatteryLow: 20, Offline: true, OpenTooLong: map[string]int{"garage": 30, "doors": 10, "car_doors": 10}, Include: []string{"binary_sensor.smoke"}})
	eq(t, "water", *h.Energy.Water, Water{Flow: "sensor.water_flow", Today: "sensor.water_today", Date: "sensor.water_last_day", Valve: &Valve{Entity: "switch.water_valve", OpenOnly: true}, Mode: "sensor.water_mode", Alerts: []string{"binary_sensor.leak_laundry"}})
	eq(t, "the old readouts are filled from the card", []string{h.Energy.WaterFlow, h.Energy.WaterToday, h.Energy.WaterDate}, []string{"sensor.water_flow", "sensor.water_today", "sensor.water_last_day"})
	eq(t, "the valve is under security", h.Security.Valves, []Valve{{Entity: "switch.water_valve", OpenOnly: true}})
	eq(t, "the ask agent", h.Ask, "conversation.gemini")
	for _, id := range h.Security.Entries {
		if id == "binary_sensor.dishwasher_door" || id == "binary_sensor.laundry_dryer_door" {
			t.Errorf("an appliance's door is among the entries: %s", id)
		}
	}
	eq(t, "tariff", *h.Energy.Tariff, Tariff{Net: "sensor.bill_net", PeriodDays: 60, PeriodStart: "2026-08-27", Tiers: []Tier{{700, 0.26304}, {2100, 0.32164}, {0, 0.40866}}, Fixed: 45.40, Tax: 0.10, Currency: "$", Estimate: true,
		Seasons: []TariffSeason{{Name: "high", From: "06-01", To: "09-30", Tiers: []Tier{{700, 0.26304}, {2100, 0.32164}, {0, 0.40866}}}, {Name: "low", From: "10-01", To: "05-31", Tiers: []Tier{{700, 0.2729}, {0, 0.3315}}}}})
	eq(t, "thermostats", h.Climate.Thermostats, []Thermostat{{Entity: "climate.hall", Serves: []string{"kitchen", "shed"}},
		{Entity: "climate.downstairs", Serves: []string{}, Action: "setpoint"}, {Entity: "climate.fridge", Serves: []string{}}})
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
		Actions: []RemoteButton{{Name: "Start", Action: Action{Domain: "script", Service: "turn_on", Entity: "script.matrix_ps5"}}}},
		{Name: "Washer", Room: "empty", Status: "sensor.dishwasher_status", Finished: defaultFinished}})
	// The "Empty" area has nothing of its own; the washer gives it a reason to show.
	shown := false
	for _, r := range h.Rooms {
		if r.ID == "empty" {
			shown = true
		}
	}
	if !shown {
		t.Error("a room with only an appliance is not shown")
	}
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
	eq(t, "routines", *h.Routines, Routines{Leaving: &Routine{Actions: []RemoteButton{{Name: "Lock up", Icon: "lock", Action: Action{Domain: "script", Service: "turn_on", Entity: "script.matrix_ps5"}}}, Check: []string{"binary_sensor.leak_laundry"}},
		Arriving: &Routine{Actions: []RemoteButton{}, Check: []string{}}})
	eq(t, "panels", h.Panels, []Panel{{ID: "coffee", Name: "Coffee", Icon: "cup.and.saucer", Room: "kitchen",
		Rows:    []Row{{Entity: "sensor.beans_left", Name: "Beans"}, {Entity: "automation.stuffy_fan", Name: "Stuffy fan", Toggle: true}},
		Actions: []RemoteButton{{Name: "Brew", Action: Action{Domain: "script", Service: "turn_on", Entity: "script.matrix_ps5"}}}}})
	want := []string{
		"attention: Home Assistant has no binary_sensor.nothing",
		"attention: open_too_long knows garage, doors and car_doors, not windows",
		"climate: bands knows co2, pm2_5, voc and aqi, not smoke",
		"climate: no room attic",
		"climate: climate.fridge: action is reported or setpoint, not guess",
		"air: an entry needs a room",
		"seasonal Bad: needs a name, from and until as month-day (11-15)",
		"vehicle Car: Home Assistant has no sensor.nothing",
		"drive Sunday: at \"9:00\" is not a time (HH:MM)",
		"schedule Shades: Home Assistant has no input_datetime.nothing",
		"routines.leaving: Home Assistant has no sensor.gone",
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

// A tariff with a bad tier order, a missing rate or a bad date is reported and left out; a flat one (a single tier
// with just a rate) is fine, with the defaults filled.
func TestTariffIsChecked(t *testing.T) {
	d := smallHome()
	d.Entities = append(d.Entities, map[string]any{"entity_id": "sensor.bill_net", "platform": "demo"})
	d.States["sensor.bill_net"] = &State{State: "12", Attributes: map[string]any{}}
	render := func(yaml string) (*Tariff, []string) {
		m, err := ParseMapping([]byte("version: 3\nenergy:\n  tariff:\n    net: sensor.bill_net\n" + yaml))
		if err != nil {
			t.Fatal(err)
		}
		h, problems := Render(m, d, Config{})
		return h.Energy.Tariff, problems
	}
	if tf, p := render("    tiers: [{ rate: 0.31 }]\n"); tf == nil || tf.PeriodDays != 30 || tf.Currency != "$" || len(p) != 0 {
		t.Errorf("flat: %+v %v", tf, p)
	}
	if tf, p := render("    tiers: [{ up_to: 700, rate: 0.2 }, { up_to: 500, rate: 0.3 }, { rate: 0.4 }]\n"); tf != nil || len(p) != 1 || !strings.Contains(p[0], "tier 2") {
		t.Errorf("order: %+v %v", tf, p)
	}
	if tf, p := render("    tiers: [{ up_to: 700 }, { rate: 0.4 }]\n"); tf != nil || len(p) != 1 || !strings.Contains(p[0], "no rate") {
		t.Errorf("rate: %+v %v", tf, p)
	}
	if tf, p := render("    tiers: [{ rate: 0.4 }]\n    period_start: 27/08/2026\n"); tf != nil || len(p) != 1 || !strings.Contains(p[0], "YYYY-MM-DD") {
		t.Errorf("date: %+v %v", tf, p)
	}
	if _, err := ParseMapping([]byte("version: 2\nenergy:\n  tariff:\n    net: sensor.bill_net\n    tiers: [{ rate: 0.4 }]\n")); err == nil || !strings.Contains(err.Error(), "version: 3") {
		t.Errorf("v2: %v", err)
	}
}

// Seasons need MM-DD dates and tiers of their own; with seasons, the top-level tiers may be left out.
func TestTariffSeasonsAreChecked(t *testing.T) {
	d := smallHome()
	d.Entities = append(d.Entities, map[string]any{"entity_id": "sensor.bill_net", "platform": "demo"})
	d.States["sensor.bill_net"] = &State{State: "12", Attributes: map[string]any{}}
	render := func(yaml string) (*Tariff, []string) {
		m, err := ParseMapping([]byte("version: 3\nenergy:\n  tariff:\n    net: sensor.bill_net\n" + yaml))
		if err != nil {
			t.Fatal(err)
		}
		h, problems := Render(m, d, Config{})
		return h.Energy.Tariff, problems
	}
	if tf, p := render("    seasons: [{ from: \"06-01\", to: \"09-30\", tiers: [{ rate: 0.4 }] }, { from: \"10-01\", to: \"05-31\", tiers: [{ rate: 0.3 }] }]\n"); tf == nil || len(tf.Tiers) != 0 || len(tf.Seasons) != 2 || len(p) != 0 {
		t.Errorf("seasons only: %+v %v", tf, p)
	}
	if tf, p := render("    tiers: [{ rate: 0.3 }]\n    seasons: [{ name: summer, from: \"6/1\", to: \"09-30\", tiers: [{ rate: 0.4 }] }]\n"); tf != nil || len(p) != 1 || !strings.Contains(p[0], "season summer: from and to") {
		t.Errorf("dates: %+v %v", tf, p)
	}
	if tf, p := render("    tiers: [{ rate: 0.3 }]\n    seasons: [{ from: \"06-01\", to: \"09-30\", tiers: [] }]\n"); tf != nil || len(p) != 1 || !strings.Contains(p[0], "season 1: no tiers") {
		t.Errorf("tiers: %+v %v", tf, p)
	}
}

func TestUsedTodayTakesOneMeterOrSeveral(t *testing.T) {
	one, err := ParseMapping([]byte("version: 3\nenergy:\n  used_today: sensor.house_today\n"))
	if err != nil || len(one.Energy.UsedToday) != 1 || one.Energy.UsedToday[0] != "sensor.house_today" {
		t.Fatalf("one meter: %v %+v", err, one.Energy)
	}
	two, err := ParseMapping([]byte("version: 3\nenergy:\n  used_today: [sensor.house_today, sensor.pool_today]\n"))
	if err != nil || len(two.Energy.UsedToday) != 2 || two.Energy.UsedToday[1] != "sensor.pool_today" {
		t.Fatalf("two meters: %v %+v", err, two.Energy)
	}
	if out, _ := yaml.Marshal(one.Energy); !strings.Contains(string(out), "used_today: sensor.house_today\n") {
		t.Errorf("one meter written back as %q", out)
	}
}

func TestMetersWithoutStatisticsAreReported(t *testing.T) {
	d := smallHome()
	for _, id := range []string{"sensor.house_today", "sensor.pool_today"} {
		d.Entities = append(d.Entities, map[string]any{"entity_id": id, "platform": "demo"})
		d.States[id] = &State{State: "3", Attributes: map[string]any{"unit_of_measurement": "kWh"}}
	}
	m, err := ParseMapping([]byte("version: 3\nenergy:\n  used_today: [sensor.house_today, sensor.pool_today]\n"))
	if err != nil {
		t.Fatal(err)
	}
	h, problems := Render(m, d, Config{}) // not asked: nothing said
	if h.Energy.UsedToday != "sensor.house_today" || len(h.Energy.UsedAlso) != 1 || len(problems) != 0 {
		t.Fatalf("meters %q %v, problems %v", h.Energy.UsedToday, h.Energy.UsedAlso, problems)
	}
	d.Statistics = map[string]bool{"sensor.house_today": true}
	if _, problems = Render(m, d, Config{}); len(problems) != 1 || !strings.Contains(problems[0], "sensor.pool_today") || !strings.Contains(problems[0], "statistics") {
		t.Errorf("problems %v", problems)
	}
}
