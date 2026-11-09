package house

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Version 3 of the file: the house beyond rooms (docs/mapping.md, "Version 3"). Nothing here is guessed; the file
// alone fills these, and the add-on resolves each to names, units, states and actions for the apps to draw.

// MARK: The file's shapes

type AttentionMapping struct {
	BatteryLow  int            `yaml:"battery_low,omitempty"`
	Offline     bool           `yaml:"offline,omitempty"`
	OpenTooLong map[string]int `yaml:"open_too_long,omitempty"` // garage, doors, car_doors → minutes
	Include     []string       `yaml:"include,omitempty"`
}

type WaterMapping struct {
	Flow        string        `yaml:"flow,omitempty"`
	Today       string        `yaml:"today,omitempty"`
	Date        string        `yaml:"date,omitempty"` // the day the today figure covers, for a meter whose data arrives late
	Week        string        `yaml:"week,omitempty"`
	Month       string        `yaml:"month,omitempty"`
	Pressure    string        `yaml:"pressure,omitempty"`
	Temperature string        `yaml:"temperature,omitempty"`
	Valve       *ValveMapping `yaml:"valve,omitempty"`
	Mode        string        `yaml:"mode,omitempty"`
	Alerts      []string      `yaml:"alerts,omitempty"`
}

type ValveMapping struct {
	Entity   string `yaml:"entity"`
	OpenOnly bool   `yaml:"openOnly,omitempty"`
}

type ClimateMapping struct {
	Thermostats []ThermostatMapping    `yaml:"thermostats,omitempty"`
	Air         []AirMapping           `yaml:"air,omitempty"`
	Bands       map[string]BandMapping `yaml:"bands,omitempty"`
}

type ThermostatMapping struct {
	Entity string   `yaml:"entity"`
	Serves []string `yaml:"serves,omitempty"`
	Action string   `yaml:"action,omitempty"` // how to read hvac_action: reported (default) | setpoint
}

type AirMapping struct {
	Room        string `yaml:"room"`
	CO2         string `yaml:"co2,omitempty"`
	Temperature string `yaml:"temperature,omitempty"`
	Humidity    string `yaml:"humidity,omitempty"`
	VOC         string `yaml:"voc,omitempty"`
	PM25        string `yaml:"pm2_5,omitempty"`
	AQI         string `yaml:"aqi,omitempty"`
	Vape        string `yaml:"vape,omitempty"`
}

type BandMapping struct {
	Fair float64 `yaml:"fair"`
	Poor float64 `yaml:"poor"`
}

type ApplianceMapping struct {
	Name        string          `yaml:"name"`
	Room        string          `yaml:"room,omitempty"`
	Status      string          `yaml:"status,omitempty"`
	Program     string          `yaml:"program,omitempty"`
	Phase       string          `yaml:"phase,omitempty"`
	Remaining   string          `yaml:"remaining,omitempty"`
	Temperature string          `yaml:"temperature,omitempty"`
	Target      string          `yaml:"target,omitempty"`
	Door        string          `yaml:"door,omitempty"`
	Finished    []string        `yaml:"finished,omitempty"`
	Actions     []ButtonMapping `yaml:"actions,omitempty"`
}

type SeasonalMapping struct {
	Name   string   `yaml:"name"`
	Room   string   `yaml:"room,omitempty"`
	From   string   `yaml:"from"`  // month-day
	Until  string   `yaml:"until"` // month-day; earlier than from wraps the year
	Lights []string `yaml:"lights"`
}

type VehicleMapping struct {
	Name              string          `yaml:"name"`
	Tracker           string          `yaml:"tracker,omitempty"`
	Battery           string          `yaml:"battery,omitempty"`
	Fuel              string          `yaml:"fuel,omitempty"`
	Range             string          `yaml:"range,omitempty"`
	Low               float64         `yaml:"low,omitempty"`
	Charging          string          `yaml:"charging,omitempty"`
	ChargerPower      string          `yaml:"charger_power,omitempty"`
	ChargeLimit       string          `yaml:"charge_limit,omitempty"`
	ChargeSwitch      string          `yaml:"charge_switch,omitempty"`
	EnergyAdded       string          `yaml:"energy_added,omitempty"`
	Lock              string          `yaml:"lock,omitempty"`
	Doors             []string        `yaml:"doors,omitempty"`
	Windows           []string        `yaml:"windows,omitempty"`
	Openings          []string        `yaml:"openings,omitempty"`
	Climate           string          `yaml:"climate,omitempty"`
	InsideTemperature string          `yaml:"inside_temperature,omitempty"`
	Tyres             []string        `yaml:"tyres,omitempty"`
	Warnings          []string        `yaml:"warnings,omitempty"`
	Trip              *TripMapping    `yaml:"trip,omitempty"`
	Awake             string          `yaml:"awake,omitempty"`
	Actions           []ButtonMapping `yaml:"actions,omitempty"`
	Charger           *ChargerMapping `yaml:"charger,omitempty"`
}

type TripMapping struct {
	Destination string `yaml:"destination,omitempty"`
	Arrival     string `yaml:"arrival,omitempty"`
	Distance    string `yaml:"distance,omitempty"`
}

type ChargerMapping struct {
	Status        string `yaml:"status,omitempty"`
	Power         string `yaml:"power,omitempty"`
	SessionEnergy string `yaml:"session_energy,omitempty"`
}

type PoolMapping struct {
	Temperature    string       `yaml:"temperature,omitempty"`
	AirTemperature string       `yaml:"air_temperature,omitempty"`
	Pump           *PumpMapping `yaml:"pump,omitempty"`
	Spa            string       `yaml:"spa,omitempty"`
	Heaters        []string     `yaml:"heaters,omitempty"`
	Lights         []string     `yaml:"lights,omitempty"`
	ReadOnly       []string     `yaml:"readOnly,omitempty"`
}

type PumpMapping struct {
	Running string `yaml:"running,omitempty"`
	Power   string `yaml:"power,omitempty"`
	RPM     string `yaml:"rpm,omitempty"`
	Switch  string `yaml:"switch,omitempty"`
}

type DayMapping struct {
	Calendars []string          `yaml:"calendars,omitempty"`
	Hide      []string          `yaml:"hide,omitempty"`
	Drives    []DriveMapping    `yaml:"drives,omitempty"`
	Reminders []ReminderMapping `yaml:"reminders,omitempty"`
	Schedules []ScheduleMapping `yaml:"schedules,omitempty"`
}

type DriveMapping struct {
	Name   string `yaml:"name"`
	Sensor string `yaml:"sensor"`
	Days   Days   `yaml:"days,omitempty"`
	At     string `yaml:"at,omitempty"` // HH:MM
}

// Days is `weekdays`, `daily` or a list of ISO weekdays (1 = Monday … 7 = Sunday).
type Days []int

func (d *Days) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		switch n.Value {
		case "weekdays":
			*d = Days{1, 2, 3, 4, 5}
			return nil
		case "daily":
			*d = Days{1, 2, 3, 4, 5, 6, 7}
			return nil
		}
		return fmt.Errorf("days is weekdays, daily or a list of ISO weekdays (1 = Monday … 7 = Sunday); not %q", n.Value)
	}
	var list []int
	if err := n.Decode(&list); err != nil {
		return err
	}
	for _, x := range list {
		if x < 1 || x > 7 {
			return fmt.Errorf("days: %d is not an ISO weekday (1 = Monday … 7 = Sunday)", x)
		}
	}
	*d = list
	return nil
}

func (d Days) MarshalYAML() (any, error) { return []int(d), nil }

type ReminderMapping struct {
	Title    string         `yaml:"title"`
	Entity   string         `yaml:"entity"` // on = done
	Weekday  int            `yaml:"weekday"`
	From     string         `yaml:"from"`
	Until    string         `yaml:"until"`
	Severity string         `yaml:"severity,omitempty"` // info | attention
	Detail   string         `yaml:"detail,omitempty"`
	Pictures []string       `yaml:"pictures,omitempty"` // media-source ids
	Action   *ButtonMapping `yaml:"action,omitempty"`
}

type ScheduleMapping struct {
	Name    string            `yaml:"name"`
	Enabled string            `yaml:"enabled,omitempty"`
	Times   map[string]string `yaml:"times,omitempty"` // label → input_datetime
}

type PanelMapping struct {
	ID      string          `yaml:"id,omitempty"`
	Name    string          `yaml:"name"`
	Icon    string          `yaml:"icon,omitempty"`
	Room    string          `yaml:"room,omitempty"`
	Rows    []RowMapping    `yaml:"rows,omitempty"`
	Actions []ButtonMapping `yaml:"actions,omitempty"`
}

// RoutinesMapping: leaving and arriving. The apps build the leaving check from what they already show (open
// entries, unlocked doors, lights on, the valve, the cars) and add `check`; `actions` are the "lock up" and
// "I'm home" buttons.
type RoutinesMapping struct {
	Leaving  *RoutineMapping `yaml:"leaving,omitempty"`
	Arriving *RoutineMapping `yaml:"arriving,omitempty"`
}

type RoutineMapping struct {
	Actions []ButtonMapping `yaml:"actions,omitempty"`
	Check   []string        `yaml:"check,omitempty"` // entities to flag while on, open or unlocked, beyond the usual
}

type RowMapping struct {
	Entity string `yaml:"entity"`
	Name   string `yaml:"name,omitempty"`
}

// MARK: house.json

type Attention struct {
	BatteryLow  int            `json:"batteryLow,omitempty"`
	Offline     bool           `json:"offline,omitempty"`
	OpenTooLong map[string]int `json:"openTooLong,omitempty"`
	Include     []string       `json:"include,omitempty"`
}

type Water struct {
	Flow         string   `json:"flow,omitempty"`
	Today        string   `json:"today,omitempty"`
	Date         string   `json:"date,omitempty"` // a timestamp or date sensor: the day today's figure is for
	Week         string   `json:"week,omitempty"`
	Month        string   `json:"month,omitempty"`
	Pressure     string   `json:"pressure,omitempty"`
	Temperature  string   `json:"temperature,omitempty"`
	Valve        *Valve   `json:"valve,omitempty"`
	Mode         string   `json:"mode,omitempty"`
	ModeSettable bool     `json:"modeSettable,omitempty"` // a select: the apps may change it
	Alerts       []string `json:"alerts,omitempty"`
}

type Valve struct {
	Entity   string `json:"entity"`
	OpenOnly bool   `json:"openOnly,omitempty"`
}

type Climate struct {
	Thermostats []Thermostat    `json:"thermostats,omitempty"`
	Air         []Air           `json:"air,omitempty"`
	Bands       map[string]Band `json:"bands,omitempty"`
}

type Thermostat struct {
	Entity string   `json:"entity"`
	Serves []string `json:"serves"`
	// "setpoint": the apps count a reported "heating" only while the target is above the current temperature
	// (a "cooling" only while it is below). Absent: hvac_action as Home Assistant reports it.
	Action string `json:"action,omitempty"`
}

type Air struct {
	Room        string `json:"room"`
	CO2         string `json:"co2,omitempty"`
	Temperature string `json:"temperature,omitempty"`
	Humidity    string `json:"humidity,omitempty"`
	VOC         string `json:"voc,omitempty"`
	PM25        string `json:"pm2_5,omitempty"`
	AQI         string `json:"aqi,omitempty"`
	Vape        string `json:"vape,omitempty"`
}

type Band struct {
	Fair float64 `json:"fair"`
	Poor float64 `json:"poor"`
}

type Appliance struct {
	Name        string         `json:"name"`
	Room        string         `json:"room,omitempty"`
	Status      string         `json:"status,omitempty"`
	Program     string         `json:"program,omitempty"`
	Phase       string         `json:"phase,omitempty"`
	Remaining   string         `json:"remaining,omitempty"`
	Temperature string         `json:"temperature,omitempty"`
	Target      string         `json:"target,omitempty"`
	Door        string         `json:"door,omitempty"`
	Finished    []string       `json:"finished"`
	Actions     []RemoteButton `json:"actions,omitempty"`
}

type Seasonal struct {
	Name   string   `json:"name"`
	Room   string   `json:"room,omitempty"`
	From   string   `json:"from"`
	Until  string   `json:"until"`
	Active bool     `json:"active"`
	Lights []string `json:"lights"`
}

type Vehicle struct {
	Name              string         `json:"name"`
	Tracker           string         `json:"tracker,omitempty"`
	Battery           string         `json:"battery,omitempty"`
	Fuel              string         `json:"fuel,omitempty"`
	Range             string         `json:"range,omitempty"`
	Low               float64        `json:"low,omitempty"`
	Charging          string         `json:"charging,omitempty"`
	ChargerPower      string         `json:"chargerPower,omitempty"`
	ChargeLimit       string         `json:"chargeLimit,omitempty"`
	ChargeSwitch      string         `json:"chargeSwitch,omitempty"`
	EnergyAdded       string         `json:"energyAdded,omitempty"`
	Lock              string         `json:"lock,omitempty"`
	Doors             []string       `json:"doors,omitempty"`
	Windows           []string       `json:"windows,omitempty"`
	Openings          []string       `json:"openings,omitempty"`
	Climate           string         `json:"climate,omitempty"`
	InsideTemperature string         `json:"insideTemperature,omitempty"`
	Tyres             []string       `json:"tyres,omitempty"`
	Warnings          []string       `json:"warnings,omitempty"`
	Trip              *Trip          `json:"trip,omitempty"`
	Awake             string         `json:"awake,omitempty"`
	Actions           []RemoteButton `json:"actions,omitempty"`
	Charger           *Charger       `json:"charger,omitempty"`
}

type Trip struct {
	Destination string `json:"destination,omitempty"`
	Arrival     string `json:"arrival,omitempty"`
	Distance    string `json:"distance,omitempty"`
}

type Charger struct {
	Status        string `json:"status,omitempty"`
	Power         string `json:"power,omitempty"`
	SessionEnergy string `json:"sessionEnergy,omitempty"`
}

type Pool struct {
	Temperature    string   `json:"temperature,omitempty"`
	AirTemperature string   `json:"airTemperature,omitempty"`
	Pump           *Pump    `json:"pump,omitempty"`
	Spa            string   `json:"spa,omitempty"`
	Heaters        []string `json:"heaters,omitempty"`
	Lights         []string `json:"lights,omitempty"`
	ReadOnly       []string `json:"readOnly,omitempty"`
}

type Pump struct {
	Running string `json:"running,omitempty"`
	Power   string `json:"power,omitempty"`
	RPM     string `json:"rpm,omitempty"`
	Switch  string `json:"switch,omitempty"`
}

type Day struct {
	Calendars []string   `json:"calendars"`
	Drives    []Drive    `json:"drives,omitempty"`
	Reminders []Reminder `json:"reminders,omitempty"`
	Schedules []Schedule `json:"schedules,omitempty"`
}

type Drive struct {
	Name   string `json:"name"`
	Sensor string `json:"sensor"`
	Days   []int  `json:"days"`
	At     string `json:"at,omitempty"`
}

type Reminder struct {
	Title    string        `json:"title"`
	Entity   string        `json:"entity"`
	Weekday  int           `json:"weekday"`
	From     string        `json:"from"`
	Until    string        `json:"until"`
	Severity string        `json:"severity"`
	Detail   string        `json:"detail,omitempty"`
	Pictures []string      `json:"pictures,omitempty"`
	Action   *RemoteButton `json:"action,omitempty"`
}

type Schedule struct {
	Name    string            `json:"name"`
	Enabled string            `json:"enabled,omitempty"`
	Times   map[string]string `json:"times"`
}

type Routines struct {
	Leaving  *Routine `json:"leaving,omitempty"`
	Arriving *Routine `json:"arriving,omitempty"`
}

type Routine struct {
	Actions []RemoteButton `json:"actions"`
	Check   []string       `json:"check"`
}

type Panel struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Icon    string         `json:"icon,omitempty"`
	Room    string         `json:"room,omitempty"`
	Rows    []Row          `json:"rows"`
	Actions []RemoteButton `json:"actions,omitempty"`
}

type Row struct {
	Entity string `json:"entity"`
	Name   string `json:"name"`
	Toggle bool   `json:"toggle,omitempty"` // a switch, light, input_boolean, lock or automation row: tap toggles
}

var defaultBands = map[string]Band{"co2": {1000, 1400}, "pm2_5": {12, 35}, "voc": {220, 660}, "aqi": {50, 100}}
var defaultFinished = []string{"finished", "program_ended"}

// renderV3 resolves the version 3 sections into the house. exists drops and reports an entity Home Assistant
// does not have (and returns ""); display names a thing. today decides which seasonal sets are active.
func renderV3(m Mapping, h *House, d Dump, exists func(where, id string) string, display func(string) string, problem func(string, ...any), today time.Time) {
	rooms := map[string]bool{}
	for _, r := range h.Rooms {
		rooms[r.ID] = true
	}
	roomOf := func(where, id string) string {
		if id == "" || rooms[id] {
			return id
		}
		problem("%s: no room %s", where, id)
		return ""
	}
	each := func(where string, ids []string) []string {
		out := []string{}
		for _, id := range ids {
			if e := exists(where, id); e != "" {
				out = append(out, e)
			}
		}
		return out
	}
	buttons := func(where string, bs []ButtonMapping) []RemoteButton {
		var out []RemoteButton
		for _, b := range bs {
			a, err := b.Action.action()
			if err != nil {
				problem("%s, action %s: %v", where, b.Name, err)
				continue
			}
			if a.Entity != "" && exists(where, a.Entity) == "" {
				continue
			}
			if b.Name == "" {
				problem("%s: an action needs a name", where)
				continue
			}
			out = append(out, RemoteButton{Name: b.Name, Icon: b.Icon, Action: a})
		}
		return out
	}
	clock := func(where, field, v string) string {
		if v == "" {
			return ""
		}
		if _, err := time.Parse("15:04", v); err != nil || len(v) != 5 {
			problem("%s: %s %q is not a time (HH:MM)", where, field, v)
			return ""
		}
		return v
	}

	if a := m.Home.Attention; a != nil {
		at := &Attention{BatteryLow: a.BatteryLow, Offline: a.Offline, OpenTooLong: map[string]int{"garage": 10, "doors": 10, "car_doors": 10}, Include: each("attention", a.Include)}
		for k, v := range a.OpenTooLong {
			switch k {
			case "garage", "doors", "car_doors":
				at.OpenTooLong[k] = v
			default:
				problem("attention: open_too_long knows garage, doors and car_doors, not %s", k)
			}
		}
		h.Summary.Attention = at
	}

	if m.Energy != nil && m.Energy.Water != nil {
		wm := m.Energy.Water
		w := &Water{Flow: exists("water", wm.Flow), Today: exists("water", wm.Today), Date: exists("water", wm.Date), Week: exists("water", wm.Week), Month: exists("water", wm.Month),
			Pressure: exists("water", wm.Pressure), Temperature: exists("water", wm.Temperature), Alerts: each("water", wm.Alerts)}
		if mode := exists("water", wm.Mode); mode != "" {
			w.Mode, w.ModeSettable = mode, domain(mode) == "select" || domain(mode) == "input_select"
		}
		if wm.Valve != nil {
			if v := exists("water", wm.Valve.Entity); v != "" {
				if dom := domain(v); dom != "valve" && dom != "switch" {
					problem("water: the valve %s is a %s, not a valve or a switch", v, dom)
				} else {
					w.Valve = &Valve{Entity: v, OpenOnly: wm.Valve.OpenOnly}
					h.Security.Valves = append(h.Security.Valves, *w.Valve)
				}
			}
		}
		h.Energy.Water = w
		// Older apps keep their two readouts.
		if h.Energy.WaterFlow == "" {
			h.Energy.WaterFlow = w.Flow
		}
		if h.Energy.WaterToday == "" {
			h.Energy.WaterToday = w.Today
		}
		h.Energy.WaterDate = w.Date
	}

	if m.Energy != nil && m.Energy.Tariff != nil {
		tm := m.Energy.Tariff
		if net := exists("tariff", tm.Net); net != "" {
			t := &Tariff{Net: net, Delivered: exists("tariff", tm.Delivered), Received: exists("tariff", tm.Received), PeriodDays: tm.PeriodDays, PeriodStart: tm.PeriodStart,
				Fixed: tm.Fixed, Tax: tm.Tax, Currency: tm.Currency, Estimate: tm.Estimate, Tiers: []Tier{}}
			if t.PeriodDays <= 0 {
				t.PeriodDays = 30
			}
			if t.Currency == "" {
				t.Currency = "$"
			}
			ok := true
			// tiers checks one list: rates above zero, each up_to above the one before, none on the last.
			tiers := func(where string, list []TierMapping) []Tier {
				out := []Tier{}
				if len(list) == 0 {
					problem("%s: no tiers; one with just a rate is a flat tariff", where)
					ok = false
				}
				for i, tier := range list {
					if tier.Rate <= 0 {
						problem("%s: tier %d has no rate", where, i+1)
						ok = false
					}
					last := i == len(list)-1
					if !last && (tier.UpTo <= 0 || (i > 0 && tier.UpTo <= list[i-1].UpTo)) {
						problem("%s: tier %d needs an up_to above the tier before it (only the last tier has none)", where, i+1)
						ok = false
					}
					if last {
						tier.UpTo = 0
					}
					out = append(out, Tier{UpTo: tier.UpTo, Rate: tier.Rate})
				}
				return out
			}
			if len(tm.Tiers) > 0 || len(tm.Seasons) == 0 {
				t.Tiers = tiers("tariff", tm.Tiers)
			}
			for i, s := range tm.Seasons {
				where := fmt.Sprintf("tariff: season %d", i+1)
				if s.Name != "" {
					where = "tariff: season " + s.Name
				}
				if monthDay(s.From) == 0 || monthDay(s.To) == 0 {
					problem("%s: from and to are MM-DD dates", where)
					ok = false
				}
				t.Seasons = append(t.Seasons, TariffSeason{Name: s.Name, From: s.From, To: s.To, Tiers: tiers(where, s.Tiers)})
			}
			if tm.PeriodStart != "" {
				if _, err := time.Parse("2006-01-02", tm.PeriodStart); err != nil {
					problem("tariff: period_start %q is not a date (YYYY-MM-DD)", tm.PeriodStart)
					ok = false
				}
			}
			if ok {
				h.Energy.Tariff = t
			}
		}
	}

	if c := m.Climate; c != nil {
		cl := &Climate{Bands: map[string]Band{}}
		for k, b := range defaultBands {
			cl.Bands[k] = b
		}
		for k, b := range c.Bands {
			if _, known := defaultBands[k]; !known {
				problem("climate: bands knows co2, pm2_5, voc and aqi, not %s", k)
				continue
			}
			cl.Bands[k] = Band{b.Fair, b.Poor}
		}
		for _, t := range c.Thermostats {
			e := exists("climate", t.Entity)
			if e == "" {
				continue
			}
			if domain(e) != "climate" {
				problem("climate: %s is not a thermostat", e)
				continue
			}
			th := Thermostat{Entity: e, Serves: []string{}}
			switch t.Action {
			case "", "reported":
			case "setpoint":
				th.Action = "setpoint"
			default:
				problem("climate: %s: action is reported or setpoint, not %s", e, t.Action)
			}
			for _, r := range t.Serves {
				if roomOf("climate", r) != "" {
					th.Serves = append(th.Serves, r)
				}
			}
			cl.Thermostats = append(cl.Thermostats, th)
			// A room's thermostat is the climate entity among its tiles: a served room without one gets this one.
			for i := range h.Rooms {
				if !contains(th.Serves, h.Rooms[i].ID) || contains(h.Rooms[i].Entities, e) {
					continue
				}
				hasOwn := false
				for _, id := range h.Rooms[i].Entities {
					if domain(id) == "climate" {
						hasOwn = true
					}
				}
				if !hasOwn {
					h.Rooms[i].Entities = append(h.Rooms[i].Entities, e)
				}
			}
		}
		for _, a := range c.Air {
			room := roomOf("air", a.Room)
			if a.Room == "" {
				problem("air: an entry needs a room")
				continue
			}
			if room == "" {
				continue
			}
			air := Air{Room: room, CO2: exists("air", a.CO2), Temperature: exists("air", a.Temperature), Humidity: exists("air", a.Humidity),
				VOC: exists("air", a.VOC), PM25: exists("air", a.PM25), AQI: exists("air", a.AQI), Vape: exists("air", a.Vape)}
			cl.Air = append(cl.Air, air)
		}
		h.Climate = cl
	}

	for _, a := range m.Appliances {
		if a.Name == "" {
			problem("appliances: an appliance needs a name")
			continue
		}
		where := "appliance " + a.Name
		ap := Appliance{Name: a.Name, Room: roomOf(where, a.Room), Status: exists(where, a.Status), Program: exists(where, a.Program), Phase: exists(where, a.Phase),
			Remaining: exists(where, a.Remaining), Temperature: exists(where, a.Temperature), Target: exists(where, a.Target), Door: exists(where, a.Door),
			Finished: a.Finished, Actions: buttons(where, a.Actions)}
		if ap.Finished == nil {
			ap.Finished = defaultFinished
		}
		h.Appliances = append(h.Appliances, ap)
	}

	for _, s := range m.Seasonal {
		where := "seasonal " + s.Name
		from, until := monthDay(s.From), monthDay(s.Until)
		if s.Name == "" || from == 0 || until == 0 {
			problem("%s: needs a name, from and until as month-day (11-15)", firstOf(where, "seasonal"))
			continue
		}
		lights := each(where, s.Lights)
		if len(lights) == 0 {
			continue
		}
		h.Seasonal = append(h.Seasonal, Seasonal{Name: s.Name, Room: roomOf(where, s.Room), From: s.From, Until: s.Until, Lights: lights, Active: inSeason(today, from, until)})
	}

	for _, v := range m.Vehicles {
		if v.Name == "" {
			problem("vehicles: a vehicle needs a name")
			continue
		}
		where := "vehicle " + v.Name
		ve := Vehicle{Name: v.Name, Tracker: exists(where, v.Tracker), Battery: exists(where, v.Battery), Fuel: exists(where, v.Fuel), Range: exists(where, v.Range), Low: v.Low,
			Charging: exists(where, v.Charging), ChargerPower: exists(where, v.ChargerPower), ChargeLimit: exists(where, v.ChargeLimit), ChargeSwitch: exists(where, v.ChargeSwitch),
			EnergyAdded: exists(where, v.EnergyAdded), Lock: exists(where, v.Lock), Doors: each(where, v.Doors), Windows: each(where, v.Windows), Openings: each(where, v.Openings),
			Climate: exists(where, v.Climate), InsideTemperature: exists(where, v.InsideTemperature), Tyres: each(where, v.Tyres), Warnings: each(where, v.Warnings),
			Awake: exists(where, v.Awake), Actions: buttons(where, v.Actions)}
		if v.Trip != nil {
			ve.Trip = &Trip{Destination: exists(where, v.Trip.Destination), Arrival: exists(where, v.Trip.Arrival), Distance: exists(where, v.Trip.Distance)}
		}
		if v.Charger != nil {
			ve.Charger = &Charger{Status: exists(where, v.Charger.Status), Power: exists(where, v.Charger.Power), SessionEnergy: exists(where, v.Charger.SessionEnergy)}
		}
		h.Vehicles = append(h.Vehicles, ve)
	}

	if p := m.Pool; p != nil {
		pool := &Pool{Temperature: exists("pool", p.Temperature), AirTemperature: exists("pool", p.AirTemperature), Spa: exists("pool", p.Spa),
			Heaters: each("pool", p.Heaters), Lights: each("pool", p.Lights), ReadOnly: each("pool", p.ReadOnly)}
		if p.Pump != nil {
			pool.Pump = &Pump{Running: exists("pool", p.Pump.Running), Power: exists("pool", p.Pump.Power), RPM: exists("pool", p.Pump.RPM), Switch: exists("pool", p.Pump.Switch)}
		}
		h.Pool = pool
	}

	if dm := m.Day; dm != nil {
		day := &Day{Calendars: []string{}}
		hidden := map[string]bool{}
		for _, c := range dm.Hide {
			hidden[c] = true
		}
		for _, c := range each("day", dm.Calendars) {
			if !hidden[c] {
				day.Calendars = append(day.Calendars, c)
			}
		}
		for _, dr := range dm.Drives {
			where := "drive " + dr.Name
			if dr.Name == "" {
				problem("day: a drive needs a name")
				continue
			}
			s := exists(where, dr.Sensor)
			if s == "" {
				continue
			}
			days := []int(dr.Days)
			if days == nil {
				days = []int{1, 2, 3, 4, 5, 6, 7}
			}
			day.Drives = append(day.Drives, Drive{Name: dr.Name, Sensor: s, Days: days, At: clock(where, "at", dr.At)})
		}
		for _, r := range dm.Reminders {
			where := "reminder " + r.Title
			if r.Title == "" || r.Weekday < 1 || r.Weekday > 7 || r.From == "" || r.Until == "" {
				problem("%s: needs a title, an ISO weekday, from and until", firstOf(where, "reminder"))
				continue
			}
			e := exists(where, r.Entity)
			if e == "" {
				continue
			}
			sev := firstOf(r.Severity, "info")
			if sev != "info" && sev != "attention" {
				problem("%s: severity is info or attention, not %s", where, sev)
				sev = "info"
			}
			rem := Reminder{Title: r.Title, Entity: e, Weekday: r.Weekday, From: clock(where, "from", r.From), Until: clock(where, "until", r.Until), Severity: sev,
				Detail: exists(where, r.Detail), Pictures: r.Pictures}
			if r.Action != nil {
				if bs := buttons(where, []ButtonMapping{*r.Action}); len(bs) == 1 {
					rem.Action = &bs[0]
				}
			}
			day.Reminders = append(day.Reminders, rem)
		}
		for _, s := range dm.Schedules {
			where := "schedule " + s.Name
			if s.Name == "" {
				problem("day: a schedule needs a name")
				continue
			}
			sc := Schedule{Name: s.Name, Enabled: exists(where, s.Enabled), Times: map[string]string{}}
			for _, label := range sortedKeys(s.Times) {
				if t := exists(where, s.Times[label]); t != "" {
					sc.Times[label] = t
				}
			}
			day.Schedules = append(day.Schedules, sc)
		}
		h.Day = day
	}

	for _, p := range m.Panels {
		if p.Name == "" {
			problem("panels: a panel needs a name")
			continue
		}
		where := "panel " + p.Name
		pn := Panel{ID: firstOf(p.ID, slugOf(p.Name)), Name: p.Name, Icon: p.Icon, Room: roomOf(where, p.Room), Rows: []Row{}, Actions: buttons(where, p.Actions)}
		for _, r := range p.Rows {
			e := exists(where, r.Entity)
			if e == "" {
				continue
			}
			dom := domain(e)
			pn.Rows = append(pn.Rows, Row{Entity: e, Name: firstOf(r.Name, display(e)), Toggle: dom == "switch" || dom == "light" || dom == "input_boolean" || dom == "lock" || dom == "automation" || dom == "fan"})
		}
		h.Panels = append(h.Panels, pn)
	}

	if rm := m.Routines; rm != nil {
		routine := func(where string, r *RoutineMapping) *Routine {
			if r == nil {
				return nil
			}
			out := &Routine{Actions: buttons(where, r.Actions), Check: []string{}}
			if out.Actions == nil {
				out.Actions = []RemoteButton{}
			}
			for _, id := range r.Check {
				if e := exists(where, id); e != "" {
					out.Check = append(out.Check, e)
				}
			}
			return out
		}
		h.Routines = &Routines{Leaving: routine("routines.leaving", rm.Leaving), Arriving: routine("routines.arriving", rm.Arriving)}
	}
}

// v3Rooms is every room the version 3 sections place something in: such a room shows even with no tiles of its own.
func (m Mapping) v3Rooms() map[string]bool {
	out := map[string]bool{}
	for _, a := range m.Appliances {
		out[a.Room] = true
	}
	for _, p := range m.Panels {
		out[p.Room] = true
	}
	for _, s := range m.Seasonal {
		out[s.Room] = true
	}
	if m.Climate != nil {
		for _, a := range m.Climate.Air {
			out[a.Room] = true
		}
	}
	delete(out, "")
	return out
}

// monthDay parses "11-15" into 1115; 0 when it is not one.
func monthDay(s string) int {
	parts := strings.Split(s, "-")
	if len(parts) != 2 {
		return 0
	}
	m, err1 := strconv.Atoi(parts[0])
	d, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || m < 1 || m > 12 || d < 1 || d > 31 {
		return 0
	}
	return m*100 + d
}

// inSeason says whether today falls between from and until (month-days), wrapping the year when until is earlier.
func inSeason(today time.Time, from, until int) bool {
	t := int(today.Month())*100 + today.Day()
	if from <= until {
		return t >= from && t <= until
	}
	return t >= from || t <= until
}
