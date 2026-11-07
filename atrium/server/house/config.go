package house

import (
	"encoding/json"
	"errors"
	"os"
	"sort"
)

// Config is the owner's overrides: deliberately few. Everything is keyed by Home Assistant's own ids, and the
// layout is always built fresh from the live registries with these applied on top, so a new entity in a room
// appears by itself, a removed one disappears, and one that comes back finds its old overrides waiting.
type Config struct {
	Name     string                  `json:"name,omitempty"`     // what to call the house
	Tabs     map[string]bool         `json:"tabs,omitempty"`     // "security", "energy", "music": false hides the tab
	Floors   map[string]FloorConfig  `json:"floors,omitempty"`   // by floor id; ids not in the registry are the owner's own floors ("outside")
	Rooms    map[string]RoomConfig   `json:"rooms,omitempty"`    // by area id
	Entities map[string]EntityConfig `json:"entities,omitempty"` // by entity id
}

type FloorConfig struct {
	Name  string `json:"name,omitempty"`
	Order *int   `json:"order,omitempty"` // lower first; unset floors keep the registry's level order after the ordered ones
}

type RoomConfig struct {
	Hidden     bool     `json:"hidden,omitempty"`
	Name       string   `json:"name,omitempty"`
	Floor      string   `json:"floor,omitempty"`      // a floor id, from the registry or Floors
	Order      *int     `json:"order,omitempty"`      // within the floor; unordered rooms follow by name
	Thermostat string   `json:"thermostat,omitempty"` // a climate entity that belongs with this room, wherever Home Assistant put it
	Entities   []string `json:"entities,omitempty"`   // the order; entities not listed follow in the default order
}

type EntityConfig struct {
	Hidden bool   `json:"hidden,omitempty"`
	Name   string `json:"name,omitempty"`
	Room   string `json:"room,omitempty"` // an area id: show it in this room instead
	Show   bool   `json:"show,omitempty"` // show it in its room even though the defaults would not (a switch, a sensor)
}

// Load reads a config file; a missing file is an empty configuration.
func Load(path string) (Config, error) {
	var c Config
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(data, &c)
}

// Save writes a config file atomically.
func Save(path string, c Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Validate rejects what the builder could not apply.
func (c Config) Validate() error {
	for id, e := range c.Entities {
		if domain(id) == "" {
			return errors.New("entities: " + id + " is not an entity id")
		}
		if e.Room != "" && e.Hidden {
			return errors.New("entities: " + id + " is both hidden and moved")
		}
	}
	for id, r := range c.Rooms {
		if id == "" {
			return errors.New("rooms: empty area id")
		}
		if r.Thermostat != "" && domain(r.Thermostat) != "climate" {
			return errors.New("rooms: " + id + ": thermostat must be a climate entity")
		}
	}
	return nil
}

// ordered applies an optional explicit order to ids: listed ones first, in that order, then the rest as they were.
func ordered(ids []string, first []string) []string {
	if len(first) == 0 {
		return ids
	}
	have := map[string]bool{}
	for _, id := range ids {
		have[id] = true
	}
	out := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range first {
		if have[id] && !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	for _, id := range ids {
		if !seen[id] {
			out = append(out, id)
		}
	}
	return out
}

// sortedKeys is the keys of a map, sorted, so output never depends on map order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
