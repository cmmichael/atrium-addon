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
}

func (h HomeMapping) set() bool {
	return h.Weather != "" || h.OutdoorTemperature != "" || h.IndoorTemperature != "" || h.Alarm != "" || len(h.People) > 0
}

type SecurityMapping struct {
	Cameras   []string          `yaml:"cameras,omitempty"`
	Doorbells []string          `yaml:"doorbells,omitempty"`
	CameraFor map[string]string `yaml:"camera_for,omitempty"`
	Locks     []string          `yaml:"locks,omitempty"`
	Entries   []string          `yaml:"entries,omitempty"`
	Hazards   []string          `yaml:"hazards,omitempty"`
}

type EnergyMapping struct {
	SolarPower string   `yaml:"solar_power,omitempty"`
	LoadPower  []string `yaml:"load_power,omitempty"`
	SolarToday string   `yaml:"solar_today,omitempty"`
	UsedToday  string   `yaml:"used_today,omitempty"`
	Consumers  []string `yaml:"consumers,omitempty"`
	WaterFlow  string   `yaml:"water_flow,omitempty"`
	WaterToday string   `yaml:"water_today,omitempty"`
}

type MusicMapping struct {
	Speakers []string `yaml:"speakers,omitempty"`
}

// Empty says whether the file adds nothing: no overrides and no custom screen.
func (m Mapping) Empty() bool {
	return m.Name == "" && len(m.Floors) == 0 && len(m.Rooms) == 0 && len(m.Hide) == 0 && len(m.Scenes) == 0 &&
		!m.Home.set() && m.Security == nil && m.Energy == nil && m.Music == nil && len(m.Names) == 0
}

// Custom says whether the file has any of the screens that only it can fill.
func (m Mapping) Custom() bool {
	return m.Home.set() || m.Security != nil || m.Energy != nil || m.Music != nil
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
	if m.Version != 0 && m.Version != 1 && m.Version != 2 {
		return m, fmt.Errorf("version %d is not known (this add-on knows versions 1 and 2)", m.Version)
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
