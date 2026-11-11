// Package house renders the app's layout (house.json) from the declared mapping (atrium.yaml, see
// docs/mapping.md), what Home Assistant knows about its entities, and the owner's in-app preferences.
//
// The JSON field names are the contract with the apps: the iOS app decodes this with its own House type.
package house

import (
	"sort"
	"strings"
	"time"
)

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
	// Version 3: what "needs attention" counts, beyond the built-in alerts.
	Attention *Attention `json:"attention,omitempty"`
}

// Security is what the Security screen shows.
type Security struct {
	Cameras   []string `json:"cameras"`
	Locks     []string `json:"locks"`
	Entries   []string `json:"entries"`
	Hazards   []string `json:"hazards"`
	Doorbells []string `json:"doorbells"`
	// The ring entity behind each doorbell camera (an event whose state is the last ring's time, or a binary
	// sensor), so the apps can tell a ring from the camera's own state.
	Rings     map[string]string `json:"rings,omitempty"`
	CameraFor map[string]string `json:"cameraFor"`
	// camera id → "hls" (streams through the API, and has stills) or "webrtc" (WebRTC only, no still: Nest).
	// A camera not listed streams the old way: stills only.
	Streams map[string]string `json:"streams"`
	// UniFi Access-style doors: an unlock button with the door's contact. Shown as locks.
	Doors []Door `json:"doors"`
	// Version 3: the water shut-off, with its openOnly flag (the apps may open it, never close it from a tap).
	Valves []Valve `json:"valves,omitempty"`
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
	// The door's access event (UniFi Access: event.*_access), whose attributes say who came in and how; the apps
	// read its history for the day's story, since the logbook drops them.
	Access string `json:"access,omitempty"`
}

// Action is one Home Assistant service call: what a remote's key, input or button does.
type Action struct {
	Domain  string         `json:"domain"`
	Service string         `json:"service"`
	Entity  string         `json:"entity,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
}

// Remote is a television, a streamer or a receiver as one set of buttons, every key already resolved to an
// action. The apps draw the keys they are given: a d-pad when there are arrows, transport when there is play,
// a number pad when there are digits. Keys: power_on, power_off (the player's; "power" when only the remote's
// toggle is known), up, down, left, right, select, back, home, menu, info, guide, exit, play, pause, play_pause,
// rewind, fast_forward, previous, next, volume_up, volume_down, mute, channel_up, channel_down, 0–9, and
// any the file adds (art, say). A mute action without is_volume_muted toggles.
type Remote struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Room    string            `json:"room,omitempty"`
	Player  string            `json:"player,omitempty"` // media_player: on or off, and what is playing
	Volume  string            `json:"volume,omitempty"` // media_player with a volume level to show
	Keys    map[string]Action `json:"keys"`
	Inputs  *Inputs           `json:"inputs,omitempty"`
	Buttons []RemoteButton    `json:"buttons,omitempty"`
	// Sensors about the set, shown on the remote: its network throughput (a UniFi client's RX and TX), its power
	// draw (a smart plug). From the file, else sensors on the television's device or on a device sharing its MAC.
	Readouts []string `json:"readouts,omitempty"`
}

// Inputs is the input (or app) picker: a media player's own source list, live from its state (select_source),
// or the file's options, each its own action.
type Inputs struct {
	Entity  string        `json:"entity,omitempty"`
	Options []InputOption `json:"options,omitempty"`
}

type InputOption struct {
	Name   string `json:"name"`
	Action Action `json:"action"`
	Entity string `json:"entity,omitempty"` // lit while this entity…
	Value  string `json:"value,omitempty"`  // …is in this state
}

type RemoteButton struct {
	Name   string `json:"name"`
	Icon   string `json:"icon,omitempty"` // an SF Symbol name
	Action Action `json:"action"`
}

// Energy is what the Energy screen shows. Powers are W or kW by the sensor's unit. gridPower is signed, positive
// when importing; batteryPower signed, positive when discharging (Home Assistant's energy-dashboard convention);
// the apps negate any sensor in inverted. All optional; the apps derive one missing term of
// solar + battery + grid = load, and treat a house without batteryPower and batteryLevel as having no battery.
type Energy struct {
	SolarPower   string   `json:"solarPower,omitempty"`
	LoadPower    []string `json:"loadPower"`
	GridPower    string   `json:"gridPower,omitempty"`
	BatteryPower string   `json:"batteryPower,omitempty"`
	BatteryLevel string   `json:"batteryLevel,omitempty"` // percent
	Inverted     []string `json:"inverted,omitempty"`     // power sensors whose sign is the opposite of the convention
	SolarToday   string   `json:"solarToday,omitempty"`
	UsedToday    string   `json:"usedToday,omitempty"`
	UsedAlso     []string `json:"usedAlso,omitempty"` // further meters whose kWh today add to usedToday's (a pool on its own tap)
	Consumers    []string `json:"consumers"`
	WaterFlow    string   `json:"waterFlow,omitempty"`
	WaterToday   string   `json:"waterToday,omitempty"`
	// The day waterToday's figure is for (a timestamp or date sensor), when the meter's data arrives days late;
	// the apps caption the figure with that day instead of "today". From water.date.
	WaterDate string `json:"waterDate,omitempty"`
	// Version 3: the water card.
	Water *Water `json:"water,omitempty"`
	// Version 3: the electricity tariff; the apps work out the bill so far and the cost now from the net meter.
	Tariff *Tariff `json:"tariff,omitempty"`
}

type Tariff struct {
	Net         string  `json:"net"`
	Delivered   string  `json:"delivered,omitempty"`
	Received    string  `json:"received,omitempty"`
	PeriodDays  int     `json:"periodDays"`
	PeriodStart string  `json:"periodStart,omitempty"`
	Tiers       []Tier  `json:"tiers"`
	Fixed       float64 `json:"fixed,omitempty"`
	Tax         float64 `json:"tax,omitempty"`
	Currency    string  `json:"currency"`
	Estimate    bool    `json:"estimate,omitempty"`
	// Seasons with their own tiers (MM-DD, inclusive, wrapping the year); the apps pick by the day.
	Seasons []TariffSeason `json:"seasons,omitempty"`
}

type TariffSeason struct {
	Name  string `json:"name,omitempty"`
	From  string `json:"from"`
	To    string `json:"to"`
	Tiers []Tier `json:"tiers"`
}

type Tier struct {
	UpTo float64 `json:"upTo,omitempty"` // cumulative kWh the tier ends at; 0 on the last: the rest
	Rate float64 `json:"rate"`
}

// House is the layout.
type House struct {
	Name     string     `json:"name"`
	Floors   []Floor    `json:"floors"`
	Rooms    []Room     `json:"rooms"`
	Scenes   []SceneRef `json:"scenes"`
	Summary  Summary    `json:"summary"`
	Security Security   `json:"security"`
	Energy   Energy     `json:"energy"`
	Speakers []string   `json:"speakers"`
	// The conversation agent the apps ask (home.ask), a conversation.* entity; empty: Home Assistant's default.
	Ask     string            `json:"ask,omitempty"`
	Names   map[string]string `json:"names"`
	BuiltAt string            `json:"builtAt,omitempty"` // when the add-on built this, RFC 3339
	// "custom": the add-on's defaults with atrium.yaml on top; "default": the defaults alone (no file, or a
	// viewer asked for them with ?layout=default to compare).
	Layout string `json:"layout,omitempty"`
	// Music Assistant, when the house has it: its config entry (its services need it) and, for each speaker
	// shown, its Music Assistant twin (play, search, transfer go to the twin).
	MusicAssistant *MusicAssistant `json:"musicAssistant,omitempty"`
	// Rooms the configuration hides, so an editor can offer them back. Not shown.
	HiddenRooms []Room `json:"hiddenRooms,omitempty"`
	// Televisions and the like, each a set of buttons: the file's, or one per television found.
	Remotes []Remote `json:"remotes,omitempty"`
	// Home Screen widgets: the file's, or Scenes, Home and one per room. The phone fits the items to the size.
	Widgets []Widget `json:"widgets,omitempty"`
	// Version 3 of the file: the house beyond rooms. Each absent when the file has no such section.
	Climate    *Climate    `json:"climate,omitempty"`
	Appliances []Appliance `json:"appliances,omitempty"`
	Seasonal   []Seasonal  `json:"seasonal,omitempty"`
	Vehicles   []Vehicle   `json:"vehicles,omitempty"`
	Pool       *Pool       `json:"pool,omitempty"`
	Day        *Day        `json:"day,omitempty"`
	Panels     []Panel     `json:"panels,omitempty"`
	Routines   *Routines   `json:"routines,omitempty"`
}

// Widget is one choice in the phone's widget gallery: a named list of items, every one already resolved. The
// widget draws as many as its size holds, in this order, unless the file says what each size shows.
type Widget struct {
	ID    string       `json:"id"`
	Name  string       `json:"name"`
	Items []WidgetItem `json:"items"`
	// Item ids per size, when the file chose; else the first items that fit.
	Small  []string `json:"small,omitempty"`
	Medium []string `json:"medium,omitempty"`
	Large  []string `json:"large,omitempty"`
	Lock   []string `json:"lock,omitempty"` // the Lock Screen's small shapes
}

// WidgetItem is one tile of a widget. Kinds: scene (tap runs it), toggle (an entity whose state is on or off;
// tap toggles), lights (a room's lights: how many are on; tap turns them off), readout (a number with its unit),
// alarm (the alarm's state; tap opens the app), button (any action).
type WidgetItem struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Name     string   `json:"name"`
	Icon     string   `json:"icon,omitempty"`     // an SF Symbol name
	Entity   string   `json:"entity,omitempty"`   // whose state is shown
	Entities []string `json:"entities,omitempty"` // lights: the room's lights
	Unit     string   `json:"unit,omitempty"`     // readout
	Action   *Action  `json:"action,omitempty"`   // what a tap does; none for alarm and readout
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
	State       string         `json:"state"`
	Attributes  map[string]any `json:"attributes"`
	LastChanged time.Time      `json:"-"` // when the state last changed; zero when not known
}

// restored says whether this is a placeholder Home Assistant wrote at start for a registered entity its
// integration has not provided since (`restored: true`, state unavailable): the device is gone, or its
// integration is still loading.
func (s *State) restored() bool {
	if s == nil {
		return false
	}
	v, _ := s.Attributes["restored"].(bool)
	return v
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
	// config_entries/get: each integration's entry and whether it is loaded. Missing: not asked (older dump).
	ConfigEntries []map[string]any
	// recorder/list_statistic_ids: the entities Home Assistant keeps long-term statistics for. Nil: not asked.
	Statistics map[string]bool
	// Home Assistant's state from get_config ("RUNNING" once started up) and how long it has been running.
	// Integrations load in the first minutes, so what is restored then means nothing yet; see Settled.
	Running    bool
	RunningFor time.Duration
	// Home Assistant's currency from get_config ("USD", "EUR"): the tariff's sign when the layout names none.
	Currency string
}

// SettleAfter is how long after Home Assistant starts its restored entities are taken to mean something: an
// integration that has not provided an entity by then is not about to.
const SettleAfter = 10 * time.Minute

// Settled says whether a restored entity may be read as gone: Home Assistant has been running for SettleAfter.
// Before that, restored entities are treated like any other and nothing is decided from them.
func (d Dump) Settled() bool { return d.Running && d.RunningFor >= SettleAfter }

// loadedEntries is the config entries that are loaded, by id; nil when the dump did not ask (older dump).
func (d Dump) loadedEntries() map[string]bool {
	if d.ConfigEntries == nil {
		return nil
	}
	loaded := map[string]bool{}
	for _, e := range d.ConfigEntries {
		if id := str(e["entry_id"]); id != "" && str(e["state"]) == "loaded" {
			loaded[id] = true
		}
	}
	return loaded
}

// StaleEntity is a registered entity whose integration is loaded and yet has not provided it since Home Assistant
// started: left behind by a device that was removed or renamed. Atrium only lists them; deleting is the owner's.
type StaleEntity struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform,omitempty"`
}

// Stale lists the stale entities, by id, once Home Assistant has settled; nothing before.
func Stale(d Dump) []StaleEntity {
	out := []StaleEntity{}
	if !d.Settled() || d.ConfigEntries == nil {
		return out
	}
	loaded := d.loadedEntries()
	for _, e := range d.Entities {
		id := str(e["entity_id"])
		st := d.States[id]
		if id == "" || e["disabled_by"] != nil || !st.restored() || !loaded[str(e["config_entry_id"])] {
			continue
		}
		name := firstOf(st.str("friendly_name"), str(e["name"]), str(e["original_name"]), pretty(id))
		out = append(out, StaleEntity{ID: id, Name: name, Platform: str(e["platform"])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// CurrencySign is how the apps write an ISO 4217 code: the usual sign where there is one, else the code and a space.
func CurrencySign(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	switch code {
	case "":
		return "$"
	case "USD", "CAD", "AUD", "NZD", "SGD", "HKD", "MXN":
		return "$"
	case "EUR":
		return "€"
	case "GBP":
		return "£"
	case "JPY", "CNY":
		return "¥"
	case "INR":
		return "₹"
	case "KRW":
		return "₩"
	case "ILS":
		return "₪"
	case "BRL":
		return "R$"
	}
	return code + " "
}
