package house

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Mapping is the declared layout, atrium.yaml: which entities play which part on which screen. Its contract
// is docs/mapping.md in the repository. The add-on renders exactly this; it guesses nothing.
type Mapping struct {
	Version int            `yaml:"version"`
	Name    string         `yaml:"name"`
	Floors  []FloorMapping `yaml:"floors"`
	Rooms   []RoomMapping  `yaml:"rooms"`
	Scenes  []SceneMapping `yaml:"scenes"`
	Home    HomeMapping    `yaml:"home"`
	// Pointers: an absent section means no tab; an empty one means a tab with nothing in it yet.
	Security *SecurityMapping  `yaml:"security"`
	Energy   *EnergyMapping    `yaml:"energy"`
	Music    *MusicMapping     `yaml:"music"`
	Names    map[string]string `yaml:"names"`
}

type FloorMapping struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
}

type RoomMapping struct {
	ID          string   `yaml:"id"`
	Name        string   `yaml:"name"`
	Floor       string   `yaml:"floor"`
	Icon        string   `yaml:"icon"`
	Thermostat  string   `yaml:"thermostat"`
	Temperature string   `yaml:"temperature"`
	Humidity    string   `yaml:"humidity"`
	Entities    []string `yaml:"entities"`
}

type SceneMapping struct {
	Entity string `yaml:"entity"`
	Name   string `yaml:"name"`
}

type HomeMapping struct {
	Weather            string   `yaml:"weather"`
	OutdoorTemperature string   `yaml:"outdoor_temperature"`
	IndoorTemperature  string   `yaml:"indoor_temperature"`
	Alarm              string   `yaml:"alarm"`
	People             []string `yaml:"people"`
}

type SecurityMapping struct {
	Cameras   []string          `yaml:"cameras"`
	Doorbells []string          `yaml:"doorbells"`
	CameraFor map[string]string `yaml:"camera_for"`
	Locks     []string          `yaml:"locks"`
	Entries   []string          `yaml:"entries"`
	Hazards   []string          `yaml:"hazards"`
}

type EnergyMapping struct {
	SolarPower string   `yaml:"solar_power"`
	LoadPower  []string `yaml:"load_power"`
	SolarToday string   `yaml:"solar_today"`
	UsedToday  string   `yaml:"used_today"`
	Consumers  []string `yaml:"consumers"`
	WaterFlow  string   `yaml:"water_flow"`
	WaterToday string   `yaml:"water_today"`
}

type MusicMapping struct {
	Speakers []string `yaml:"speakers"`
}

// Empty says whether there is no layout at all: no file, or one that names no rooms.
func (m Mapping) Empty() bool { return len(m.Rooms) == 0 }

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
	if m.Version != 0 && m.Version != 1 {
		return m, fmt.Errorf("version %d is not known (this add-on knows version 1)", m.Version)
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
		for _, id := range append([]string{r.Thermostat, r.Temperature, r.Humidity}, r.Entities...) {
			if id != "" && domain(id) == "" {
				return m, fmt.Errorf("room %q: %q is not an entity id", r.ID, id)
			}
		}
		if r.Thermostat != "" && domain(r.Thermostat) != "climate" {
			return m, fmt.Errorf("room %q: thermostat %q is not a climate entity", r.ID, r.Thermostat)
		}
	}
	for i, f := range m.Floors {
		if f.ID == "" {
			return m, fmt.Errorf("floors[%d] has no id", i)
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
