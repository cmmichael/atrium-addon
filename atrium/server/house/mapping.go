package house

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

// Mapping is atrium.yaml: what the automatic layout cannot know. The rooms are drawn from Home Assistant's areas
// and devices on every build, so a new device appears by itself; the file carries the owner's and Claude's
// decisions on top — rooms to hide, rank, rename or move, entities to borrow or drop — and the screens that need
// judgment: the Home readouts, Security, Energy and Music. Its contract is docs/mapping.md in the repository.
type Mapping struct {
	Version int            `yaml:"version"`
	Name    string         `yaml:"name,omitempty"`
	Floors  []FloorMapping `yaml:"floors,omitempty"` // overrides by floor id: name, rank
	Rooms   []RoomMapping  `yaml:"rooms,omitempty"`  // overrides by area id (an id that is not an area makes a room of its own)
	Hide    []string       `yaml:"hide,omitempty"`   // house-wide: entity ids, globs (switch.*_identify) and integrations (integration:tesla)
	Scenes  []SceneMapping `yaml:"scenes,omitempty"` // these first, in this order, with these names; every other scene follows
	Home    HomeMapping    `yaml:"home,omitempty"`
	// Pointers: an absent section means no tab; an empty one means a tab with nothing in it yet.
	Security *SecurityMapping  `yaml:"security,omitempty"`
	Energy   *EnergyMapping    `yaml:"energy,omitempty"`
	Music    *MusicMapping     `yaml:"music,omitempty"`
	Names    map[string]string `yaml:"names,omitempty"`
	// Remotes: absent, one per television the add-on finds; given, exactly these.
	Remotes []RemoteMapping `yaml:"remotes,omitempty"`
	// Widgets: absent, Scenes, Home and one per room; given, exactly these.
	Widgets []WidgetMapping `yaml:"widgets,omitempty"`
	// Version 3: the house beyond rooms (v3.go). Nothing guessed; the file alone fills them.
	Climate    *ClimateMapping    `yaml:"climate,omitempty"`
	Appliances []ApplianceMapping `yaml:"appliances,omitempty"`
	Seasonal   []SeasonalMapping  `yaml:"seasonal,omitempty"`
	Vehicles   []VehicleMapping   `yaml:"vehicles,omitempty"`
	Pool       *PoolMapping       `yaml:"pool,omitempty"`
	Day        *DayMapping        `yaml:"day,omitempty"`
	Panels     []PanelMapping     `yaml:"panels,omitempty"`
	Routines   *RoutinesMapping   `yaml:"routines,omitempty"`
}

// v3 says whether the file uses any version 3 section.
func (m Mapping) v3() bool {
	return m.Climate != nil || m.Appliances != nil || m.Seasonal != nil || m.Vehicles != nil || m.Pool != nil || m.Day != nil || m.Panels != nil ||
		m.Routines != nil || m.Home.Attention != nil || (m.Energy != nil && (m.Energy.Water != nil || m.Energy.Tariff != nil))
}

// WidgetMapping is one Home Screen widget in the file: a name and the items it shows, in order. The phone fits
// the items to the widget's size (one in the small square, a row in the medium, a grid in the large) unless
// `small`, `medium`, `large` or `lock` list which items that size shows, by the ids the items are given here
// (the scene's or entity's id, the room's id for `lights`, "alarm", "solar", a button's name).
type WidgetMapping struct {
	ID     string              `yaml:"id,omitempty"`
	Name   string              `yaml:"name,omitempty"`
	Items  []WidgetItemMapping `yaml:"items"`
	Small  []string            `yaml:"small,omitempty"`
	Medium []string            `yaml:"medium,omitempty"`
	Large  []string            `yaml:"large,omitempty"`
	Lock   []string            `yaml:"lock,omitempty"`
}

// WidgetItemMapping is one item, in the file's short forms: `{scene: scene.movie}`, `{entity: light.island}`
// (a toggle), `{lights: kitchen}` (a room's lights), `{readout: sensor.pool_temperature}`, the words `alarm`
// and `solar`, or `{name: Movie night, icon: film, action: …}` for any action.
type WidgetItemMapping struct {
	Scene   string         `yaml:"scene,omitempty"`
	Entity  string         `yaml:"entity,omitempty"`
	Lights  string         `yaml:"lights,omitempty"`
	Readout string         `yaml:"readout,omitempty"`
	Word    string         `yaml:"-"` // alarm, solar
	Name    string         `yaml:"name,omitempty"`
	Icon    string         `yaml:"icon,omitempty"`
	Action  *ActionMapping `yaml:"action,omitempty"`
}

func (w *WidgetItemMapping) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		switch n.Value {
		case "alarm", "solar":
			*w = WidgetItemMapping{Word: n.Value}
			return nil
		}
		return fmt.Errorf("a widget item is {scene: …}, {entity: …}, {lights: <room>}, {readout: …}, alarm, solar or {name, icon, action}; not %q", n.Value)
	}
	type plain WidgetItemMapping
	var p plain
	if err := n.Decode(&p); err != nil {
		return err
	}
	*w = WidgetItemMapping(p)
	return nil
}

func (w WidgetItemMapping) MarshalYAML() (any, error) {
	if w.Word != "" {
		return w.Word, nil
	}
	type plain WidgetItemMapping
	return plain(w), nil
}

// RemoteMapping is one remote in the file. The keys the integration knows come by themselves from `remote` (its
// remote entity) and `player` (its media player: power, play/pause, what is playing, its source list as inputs);
// `keys`, `inputs` and `buttons` map the rest to Home Assistant scripts and services. `volume` names a receiver
// whose volume the remote drives instead of the television's.
type RemoteMapping struct {
	ID       string                   `yaml:"id,omitempty"`
	Name     string                   `yaml:"name,omitempty"`
	Room     string                   `yaml:"room,omitempty"`
	Remote   string                   `yaml:"remote,omitempty"`
	Player   string                   `yaml:"player,omitempty"`
	Volume   string                   `yaml:"volume,omitempty"`
	Inputs   *InputsMapping           `yaml:"inputs,omitempty"`
	Keys     map[string]ActionMapping `yaml:"keys,omitempty"`
	Buttons  []ButtonMapping          `yaml:"buttons,omitempty"`
	Readouts []string                 `yaml:"readouts,omitempty"` // sensors shown on the remote; default: throughput and power sensors of the set's device
}

// InputsMapping is `inputs: media_player.avr` (its source list, live) or a list of options, each an action and,
// optionally, `when: {select.matrix_out_1: PS5}` for which one is lit.
type InputsMapping struct {
	Entity  string
	Options []InputOptionMapping
}

type InputOptionMapping struct {
	Name   string         `yaml:"name"`
	Action ActionMapping  `yaml:"action"`
	When   map[string]any `yaml:"when,omitempty"`
}

func (in *InputsMapping) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		in.Entity = n.Value
		if domain(in.Entity) == "" {
			return fmt.Errorf("inputs: %q is not an entity id", n.Value)
		}
	case yaml.SequenceNode:
		if err := n.Decode(&in.Options); err != nil {
			return err
		}
		for i, o := range in.Options {
			if o.Name == "" {
				return fmt.Errorf("inputs[%d] has no name", i)
			}
			if len(o.When) > 1 {
				return fmt.Errorf("inputs %q: when takes one entity", o.Name)
			}
			for id := range o.When {
				if domain(id) == "" {
					return fmt.Errorf("inputs %q: when: %q is not an entity id", o.Name, id)
				}
			}
		}
	default:
		return errors.New("inputs: an entity id or a list of options")
	}
	return nil
}

func (in InputsMapping) MarshalYAML() (any, error) {
	if in.Entity != "" {
		return in.Entity, nil
	}
	return in.Options, nil
}

type ButtonMapping struct {
	Name   string        `yaml:"name"`
	Icon   string        `yaml:"icon,omitempty"`
	Action ActionMapping `yaml:"action"`
}

// ActionMapping is a service call in the file's short forms:
//
//	script.movie_night                  # a script or scene: turn_on; a button: press; an automation: trigger;
//	                                    # a switch, light or input_boolean: toggle
//	{remote: remote.tv, command: KEY_X} # remote.send_command
//	{select: select.matrix, option: PS5} # select.select_option
//	{service: media_player.select_source, entity: media_player.avr, data: {source: HDMI 2}}
//	none                                # (a key only) not drawn
type ActionMapping struct {
	Entity  string
	Service string // "domain.service"
	Data    map[string]any
	Remote  string
	Command string
	Select  string
	Option  string
	None    bool
}

func (a *ActionMapping) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Value == None {
			a.None = true
			return nil
		}
		if domain(n.Value) == "" {
			return fmt.Errorf("%q is not an entity id (or none)", n.Value)
		}
		a.Entity = n.Value
	case yaml.MappingNode:
		var m struct {
			Entity  string         `yaml:"entity"`
			Service string         `yaml:"service"`
			Data    map[string]any `yaml:"data"`
			Remote  string         `yaml:"remote"`
			Command string         `yaml:"command"`
			Select  string         `yaml:"select"`
			Option  string         `yaml:"option"`
		}
		if err := n.Decode(&m); err != nil {
			return err
		}
		*a = ActionMapping{Entity: m.Entity, Service: m.Service, Data: m.Data, Remote: m.Remote, Command: m.Command, Select: m.Select, Option: m.Option}
		switch {
		case a.Remote != "" || a.Command != "":
			if a.Remote == "" || a.Command == "" || domain(a.Remote) != "remote" {
				return errors.New("remote and command go together, and remote is a remote.* entity")
			}
		case a.Select != "" || a.Option != "":
			if a.Select == "" || a.Option == "" || domain(a.Select) == "" {
				return errors.New("select and option go together")
			}
		case a.Service != "":
			if !strings.Contains(a.Service, ".") {
				return fmt.Errorf("service %q is not domain.service", a.Service)
			}
			if a.Entity != "" && domain(a.Entity) == "" {
				return fmt.Errorf("%q is not an entity id", a.Entity)
			}
		default:
			return errors.New("an action is an entity id, {remote, command}, {select, option} or {service, entity, data}")
		}
	default:
		return errors.New("an action is an entity id or a mapping")
	}
	return nil
}

func (a ActionMapping) MarshalYAML() (any, error) {
	switch {
	case a.None:
		return None, nil
	case a.Remote != "":
		return map[string]string{"remote": a.Remote, "command": a.Command}, nil
	case a.Select != "":
		return map[string]string{"select": a.Select, "option": a.Option}, nil
	case a.Service != "":
		out := map[string]any{"service": a.Service}
		if a.Entity != "" {
			out["entity"] = a.Entity
		}
		if len(a.Data) > 0 {
			out["data"] = a.Data
		}
		return out, nil
	}
	return a.Entity, nil
}

// action resolves the short form to the call. A zero Action (no domain) means none.
func (a ActionMapping) action() (Action, error) {
	if a.Entity == "" && a.Service == "" && a.Remote == "" && a.Select == "" && !a.None {
		return Action{}, errors.New("has no action: give an entity id, {remote, command}, {select, option} or {service, entity, data}")
	}
	switch {
	case a.None:
		return Action{}, nil
	case a.Remote != "":
		return sendCommand(a.Remote, a.Command), nil
	case a.Select != "":
		return Action{Domain: "select", Service: "select_option", Entity: a.Select, Data: map[string]any{"option": a.Option}}, nil
	case a.Service != "":
		dom, svc, _ := strings.Cut(a.Service, ".")
		return Action{Domain: dom, Service: svc, Entity: a.Entity, Data: a.Data}, nil
	}
	switch d := domain(a.Entity); d {
	case "script", "scene":
		return Action{Domain: d, Service: "turn_on", Entity: a.Entity}, nil
	case "button", "input_button":
		return Action{Domain: d, Service: "press", Entity: a.Entity}, nil
	case "automation":
		return Action{Domain: d, Service: "trigger", Entity: a.Entity}, nil
	case "switch", "light", "input_boolean":
		return Action{Domain: d, Service: "toggle", Entity: a.Entity}, nil
	}
	return Action{}, fmt.Errorf("%s alone does not say what to call: give {service: …, entity: %s}", a.Entity, a.Entity)
}

type FloorMapping struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name,omitempty"`
	Rank *int   `yaml:"rank"` // lower first; unranked floors follow in Home Assistant's level order
}

// None, as a room's thermostat, temperature or humidity, means no readout: not even the automatic pick.
const None = "none"

type RoomMapping struct {
	ID          string   `yaml:"id"`
	Name        string   `yaml:"name,omitempty"`
	Floor       string   `yaml:"floor"`
	Icon        string   `yaml:"icon"`
	Rank        *int     `yaml:"rank"`           // lower first; unranked rooms follow by name
	Hide        Hide     `yaml:"hide,omitempty"` // true: the whole room; a list: these entities in it
	Thermostat  string   `yaml:"thermostat"`     // a climate entity, or "none" for no thermostat here
	Temperature string   `yaml:"temperature"`    // a sensor, or "none" for no readout
	Humidity    string   `yaml:"humidity"`
	Entities    []string `yaml:"entities"` // shown here as well, wherever Home Assistant put them
}

// Hide is `hide: true` or `hide: [light.a, switch.b]`.
type Hide struct {
	All      bool
	Entities []string
}

func (h *Hide) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		var b bool
		if err := n.Decode(&b); err != nil {
			return fmt.Errorf("hide: %q is neither true/false nor a list", n.Value)
		}
		h.All = b
	case yaml.SequenceNode:
		return n.Decode(&h.Entities)
	default:
		return errors.New("hide: must be true, false or a list of entity ids")
	}
	return nil
}

func (h Hide) IsZero() bool { return !h.All && len(h.Entities) == 0 }

type SceneMapping struct {
	Entity string `yaml:"entity"`
	Name   string `yaml:"name,omitempty"`
}

type HomeMapping struct {
	Weather            string   `yaml:"weather,omitempty"`
	OutdoorTemperature string   `yaml:"outdoor_temperature,omitempty"`
	IndoorTemperature  string   `yaml:"indoor_temperature,omitempty"`
	Alarm              string   `yaml:"alarm,omitempty"`
	People             []string `yaml:"people,omitempty"`
	// Version 3: what "needs attention" counts.
	Attention *AttentionMapping `yaml:"attention,omitempty"`
	// The conversation agent the apps ask ("Ask the house"): a conversation.* entity. Without it the apps use
	// Home Assistant's default agent.
	Ask string `yaml:"ask,omitempty"`
}

func (h HomeMapping) set() bool {
	return h.Weather != "" || h.OutdoorTemperature != "" || h.IndoorTemperature != "" || h.Alarm != "" || len(h.People) > 0 || h.Attention != nil || h.Ask != ""
}

type SecurityMapping struct {
	Cameras   []string          `yaml:"cameras,omitempty"`
	Doorbells []string          `yaml:"doorbells,omitempty"`
	Rings     map[string]string `yaml:"rings,omitempty"` // doorbell camera → the ring entity (an event or binary sensor); default: the camera's device's
	CameraFor map[string]string `yaml:"camera_for,omitempty"`
	Locks     []string          `yaml:"locks,omitempty"`
	Entries   []string          `yaml:"entries,omitempty"`
	Hazards   []string          `yaml:"hazards,omitempty"`
	// Doors whose lock is a button (UniFi Access). Absent: the add-on's guess, even when the rest of the section
	// is given; an empty list: none.
	Doors []DoorMapping `yaml:"doors,omitempty"`
}

type DoorMapping struct {
	Unlock  string `yaml:"unlock"`
	Contact string `yaml:"contact,omitempty"`
	Name    string `yaml:"name,omitempty"`
	Access  string `yaml:"access,omitempty"` // the door's access event (who came in, how); default: one on the button's device
}

type EnergyMapping struct {
	SolarPower PowerRef   `yaml:"solar_power,omitempty"`
	LoadPower  []PowerRef `yaml:"load_power,omitempty"`
	// Signed, in Home Assistant's energy-dashboard convention: grid positive when importing, battery positive when
	// discharging. Optional; when exactly one of solar, load, battery and grid is missing the apps derive it from
	// solar + battery + grid = load. A house without battery_power (or battery_level) has no battery.
	GridPower    PowerRef `yaml:"grid_power,omitempty"`
	BatteryPower PowerRef `yaml:"battery_power,omitempty"`
	BatteryLevel string   `yaml:"battery_level,omitempty"` // percent
	SolarToday   string   `yaml:"solar_today,omitempty"`
	UsedToday    string   `yaml:"used_today,omitempty"`
	Consumers    []string `yaml:"consumers,omitempty"`
	WaterFlow    string   `yaml:"water_flow,omitempty"`
	WaterToday   string   `yaml:"water_today,omitempty"`
	// Version 3: the water card, with the valve.
	Water *WaterMapping `yaml:"water,omitempty"`
	// Version 3: the electricity tariff, for the bill so far and the cost of running something now.
	Tariff *TariffMapping `yaml:"tariff,omitempty"`
}

// TariffMapping is the electricity tariff: tiers of a rate per kWh over the billing period, a fixed charge and a
// tax, and the meter that counts the period's kWh. `net` is a sensor in kWh since the period began (a
// utility_meter on what the grid delivered, less what it received where the utility nets them), reset by its
// cycle; its last_reset attribute, when it has one, says when the period began, else `period_start` steps forward
// by `period_days`. Tiers run in order: each `up_to` is the cumulative kWh the tier ends at; the last has none.
type TariffMapping struct {
	Net         string        `yaml:"net"`
	Delivered   string        `yaml:"delivered,omitempty"`
	Received    string        `yaml:"received,omitempty"`
	PeriodDays  int           `yaml:"period_days,omitempty"`  // default 30
	PeriodStart string        `yaml:"period_start,omitempty"` // YYYY-MM-DD, a day a period began
	Tiers       []TierMapping `yaml:"tiers"`
	Fixed       float64       `yaml:"fixed,omitempty"`    // per period, before tax
	Tax         float64       `yaml:"tax,omitempty"`      // a fraction of the whole: 0.10 for ten percent
	Currency    string        `yaml:"currency,omitempty"` // the sign, default $
	Estimate    bool          `yaml:"estimate,omitempty"` // the meter is not the utility's: the apps say "estimate"
	// Seasons with tiers of their own, by MM-DD dates that may wrap the year; the tiers above serve the dates no
	// season covers (and may be left out when the seasons cover the year).
	Seasons []TariffSeasonMapping `yaml:"seasons,omitempty"`
}

type TariffSeasonMapping struct {
	Name  string        `yaml:"name,omitempty"`
	From  string        `yaml:"from"` // MM-DD
	To    string        `yaml:"to"`   // MM-DD, inclusive
	Tiers []TierMapping `yaml:"tiers"`
}

type TierMapping struct {
	UpTo float64 `yaml:"up_to,omitempty"`
	Rate float64 `yaml:"rate"`
}

// PowerRef is a power sensor in the file: an entity id, or `{entity: sensor.x, invert: true}` for a sensor whose
// sign is the opposite of the convention (a grid sensor positive when exporting, say). The apps negate it.
type PowerRef struct {
	Entity string
	Invert bool
}

func (p *PowerRef) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		return n.Decode(&p.Entity)
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, val := n.Content[i].Value, n.Content[i+1]
			switch key {
			case "entity":
				if err := val.Decode(&p.Entity); err != nil {
					return err
				}
			case "invert":
				if err := val.Decode(&p.Invert); err != nil {
					return fmt.Errorf("invert: %q is not true/false", val.Value)
				}
			default:
				return fmt.Errorf("a power sensor takes entity and invert, not %q", key)
			}
		}
		if p.Entity == "" {
			return errors.New("a power sensor given as {entity: …, invert: …} needs its entity")
		}
		return nil
	default:
		return errors.New("a power sensor is an entity id, or {entity: sensor.x, invert: true}")
	}
}

// MarshalYAML writes the short form when the sign is not inverted (atrium_auto never inverts).
func (p PowerRef) MarshalYAML() (any, error) {
	if !p.Invert {
		return p.Entity, nil
	}
	return map[string]any{"entity": p.Entity, "invert": true}, nil
}

func (p PowerRef) IsZero() bool { return p.Entity == "" }

// powerRefs is the energy section's power sensors, for checks that run over all of them.
func (e *EnergyMapping) powerRefs() []PowerRef {
	if e == nil {
		return nil
	}
	return append([]PowerRef{e.SolarPower, e.GridPower, e.BatteryPower}, e.LoadPower...)
}

type MusicMapping struct {
	Speakers []string `yaml:"speakers,omitempty"`
}

// Empty says whether the file adds nothing: no overrides and no custom screen.
func (m Mapping) Empty() bool {
	return m.Name == "" && len(m.Floors) == 0 && len(m.Rooms) == 0 && len(m.Hide) == 0 && len(m.Scenes) == 0 &&
		!m.Home.set() && m.Security == nil && m.Energy == nil && m.Music == nil && len(m.Names) == 0 && m.Remotes == nil && m.Widgets == nil && !m.v3()
}

// Custom says whether the file has any of the screens that only it can fill.
func (m Mapping) Custom() bool {
	return m.Home.set() || m.Security != nil || m.Energy != nil || m.Music != nil || m.Remotes != nil || m.Widgets != nil || m.v3()
}

// hides says whether a house-wide hide rule covers this entity: its id, a glob over ids, or its integration.
func (m Mapping) hides(id, platform string) bool {
	for _, rule := range m.Hide {
		switch {
		case strings.HasPrefix(rule, "integration:"):
			if platform != "" && strings.TrimPrefix(rule, "integration:") == platform {
				return true
			}
		case rule == id:
			return true
		default:
			if ok, _ := path.Match(rule, id); ok {
				return true
			}
		}
	}
	return false
}

// ParseMapping reads atrium.yaml. Structural errors (bad YAML, a room without an id, an id that is not an
// entity id) are errors: the file is refused as a whole. Entities that do not exist are not errors here; the
// renderer drops and reports them, so one typo never blanks the house.
func ParseMapping(data []byte) (Mapping, error) {
	var m Mapping
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil && !errors.Is(err, io.EOF) { // EOF: an empty file
		return m, err
	}
	if m.Version != 0 && m.Version != 1 && m.Version != 2 && m.Version != 3 {
		return m, fmt.Errorf("version %d is not known (this add-on knows versions 1, 2 and 3)", m.Version)
	}
	if m.Version < 3 && m.v3() {
		return m, fmt.Errorf("the file has version 3 sections (climate, appliances, vehicles, pool, day, panels, seasonal, attention, water, tariff) but says version %d: write version: 3", m.Version)
	}
	seen := map[string]bool{}
	for i, r := range m.Rooms {
		if r.ID == "" {
			return m, fmt.Errorf("rooms[%d] has no id", i)
		}
		if seen[r.ID] {
			return m, fmt.Errorf("room %q appears twice", r.ID)
		}
		seen[r.ID] = true
		for _, id := range concat([]string{r.Thermostat, r.Temperature, r.Humidity}, r.Entities, r.Hide.Entities) {
			if id != "" && id != None && domain(id) == "" {
				return m, fmt.Errorf("room %q: %q is not an entity id", r.ID, id)
			}
		}
		for _, id := range concat(r.Entities, r.Hide.Entities) {
			if id == None {
				return m, fmt.Errorf("room %q: %q is only for thermostat, temperature and humidity", r.ID, id)
			}
		}
		if r.Thermostat != "" && r.Thermostat != None && domain(r.Thermostat) != "climate" {
			return m, fmt.Errorf("room %q: thermostat %q is not a climate entity", r.ID, r.Thermostat)
		}
	}
	for i, f := range m.Floors {
		if f.ID == "" {
			return m, fmt.Errorf("floors[%d] has no id", i)
		}
	}
	for i, rule := range m.Hide {
		if strings.HasPrefix(rule, "integration:") {
			if strings.TrimPrefix(rule, "integration:") == "" {
				return m, fmt.Errorf("hide[%d]: integration: needs a name", i)
			}
			continue
		}
		if _, err := path.Match(rule, ""); err != nil || domain(rule) == "" {
			return m, fmt.Errorf("hide[%d]: %q is not an entity id, a glob over entity ids, or integration:<name>", i, rule)
		}
	}
	for i, s := range m.Scenes {
		if domain(s.Entity) == "" {
			return m, fmt.Errorf("scenes[%d]: %q is not an entity id", i, s.Entity)
		}
	}
	for id := range m.Names {
		if domain(id) == "" {
			return m, fmt.Errorf("names: %q is not an entity id", id)
		}
	}
	if m.Energy != nil {
		for _, ref := range m.Energy.powerRefs() {
			if ref.Entity != "" && domain(ref.Entity) == "" {
				return m, fmt.Errorf("energy: %q is not an entity id", ref.Entity)
			}
		}
		if id := m.Energy.BatteryLevel; id != "" && domain(id) == "" {
			return m, fmt.Errorf("energy: battery_level %q is not an entity id", id)
		}
	}
	return m, nil
}

// LoadMapping reads the file; a missing file is an empty mapping, not an error.
func LoadMapping(path string) (Mapping, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Mapping{}, nil
	}
	if err != nil {
		return Mapping{}, err
	}
	return ParseMapping(data)
}
