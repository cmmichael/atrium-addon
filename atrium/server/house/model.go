// Package house renders the app's layout (house.json) from the declared mapping (atrium.yaml, see
// docs/mapping.md), what Home Assistant knows about its entities, and the owner's in-app preferences.
//
// The JSON field names are the contract with the apps: the iOS app decodes this with its own House type.
package house

// Floor is one level of the house.
type Floor struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Room is one area that has something to control.
type Room struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Floor       string   `json:"floor"`
	Icon        string   `json:"icon"`
	Entities    []string `json:"entities"`
	Temperature string   `json:"temperature,omitempty"`
	Humidity    string   `json:"humidity,omitempty"`
	Hidden      []string `json:"hidden,omitempty"` // what the configuration hides here, so an editor can offer it back
}

// SceneRef is a scene or a script offered on the home screen.
type SceneRef struct {
	Entity string `json:"entity"`
	Name   string `json:"name"`
	Room   string `json:"room,omitempty"` // the room it belongs to (its area), when it has one: a room scene, not a house scene
}

// Summary is the whole-house readouts.
type Summary struct {
	Weather     string   `json:"weather,omitempty"`
	OutdoorTemp string   `json:"outdoorTemp,omitempty"`
	IndoorTemp  string   `json:"indoorTemp,omitempty"`
	Alarm       string   `json:"alarm,omitempty"`
	People      []string `json:"people"`
}

// Security is what the Security screen shows.
type Security struct {
	Cameras   []string          `json:"cameras"`
	Locks     []string          `json:"locks"`
	Entries   []string          `json:"entries"`
	Hazards   []string          `json:"hazards"`
	Doorbells []string          `json:"doorbells"`
	CameraFor map[string]string `json:"cameraFor"`
	// camera id → "hls" (streams through the API, and has stills) or "webrtc" (WebRTC only, no still: Nest).
	// A camera not listed streams the old way: stills only.
	Streams map[string]string `json:"streams"`
	// UniFi Access-style doors: an unlock button with the door's contact. Shown as locks.
	Doors []Door `json:"doors"`
}

type MusicAssistant struct {
	Entry   string            `json:"entry"`
	Players map[string]string `json:"players"`
}

// Door is a door with a lock that is a button (UniFi Access): pressing unlock opens it for a few seconds.
type Door struct {
	Unlock  string `json:"unlock"`            // button.*
	Contact string `json:"contact,omitempty"` // binary_sensor.* door contact, for open/closed
	Name    string `json:"name"`
}

// Energy is what the Energy screen shows.
type Energy struct {
	SolarPower string   `json:"solarPower,omitempty"`
	LoadPower  []string `json:"loadPower"`
	SolarToday string   `json:"solarToday,omitempty"`
	UsedToday  string   `json:"usedToday,omitempty"`
	Consumers  []string `json:"consumers"`
	WaterFlow  string   `json:"waterFlow,omitempty"`
	WaterToday string   `json:"waterToday,omitempty"`
}

// House is the layout.
type House struct {
	Name     string            `json:"name"`
	Floors   []Floor           `json:"floors"`
	Rooms    []Room            `json:"rooms"`
	Scenes   []SceneRef        `json:"scenes"`
	Summary  Summary           `json:"summary"`
	Security Security          `json:"security"`
	Energy   Energy            `json:"energy"`
	Speakers []string          `json:"speakers"`
	Names    map[string]string `json:"names"`
	BuiltAt  string            `json:"builtAt,omitempty"` // when the add-on built this, RFC 3339
	// "custom": the add-on's defaults with atrium.yaml on top; "default": the defaults alone (no file, or a
	// viewer asked for them with ?layout=default to compare).
	Layout string `json:"layout,omitempty"`
	// Music Assistant, when the house has it: its config entry (its services need it) and, for each speaker
	// shown, its Music Assistant twin (play, search, transfer go to the twin).
	MusicAssistant *MusicAssistant `json:"musicAssistant,omitempty"`
	// Rooms the configuration hides, so an editor can offer them back. Not shown.
	HiddenRooms []Room `json:"hiddenRooms,omitempty"`
}

func newHouse(name string) House {
	return House{
		Name: name, Floors: []Floor{}, Rooms: []Room{}, Scenes: []SceneRef{}, Speakers: []string{}, Names: map[string]string{},
		Summary:  Summary{People: []string{}},
		Security: Security{Cameras: []string{}, Locks: []string{}, Entries: []string{}, Hazards: []string{}, Doorbells: []string{}, CameraFor: map[string]string{}, Streams: map[string]string{}, Doors: []Door{}},
		Energy:   Energy{LoadPower: []string{}, Consumers: []string{}},
	}
}

// State is one entity's state as Home Assistant reports it.
type State struct {
	State      string         `json:"state"`
	Attributes map[string]any `json:"attributes"`
}

func (s *State) str(key string) string {
	if s == nil {
		return ""
	}
	v, _ := s.Attributes[key].(string)
	return v
}

func (s *State) num(key string) (float64, bool) {
	if s == nil {
		return 0, false
	}
	v, ok := s.Attributes[key].(float64)
	return v, ok
}

// Dump is what Home Assistant says about itself, as its WebSocket API returns it.
type Dump struct {
	Name     string
	Areas    []map[string]any // config/area_registry/list
	Floors   []map[string]any // config/floor_registry/list
	Entities []map[string]any // config/entity_registry/list
	Devices  []map[string]any // config/device_registry/list
	States   map[string]*State
	// camera id → how it streams ("hls", "web_rtc"), from camera/capabilities. Missing: not asked (older dump).
	CameraStreams map[string][]string
}
