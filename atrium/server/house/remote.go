package house

import (
	"fmt"
	"sort"
	"strings"
)

// A remote is a TV, a streamer or a receiver as one set of buttons. Atrium's keys are abstract ("up", "select",
// "play_pause", "volume_up"…); the add-on resolves each to one Home Assistant service call, from the integration's
// own command names, so the apps only press what they are given. What one integration cannot do (switching an
// HDMI matrix, say) goes in a Home Assistant script, which atrium.yaml maps a key, an input or a button to.

// The keys the apps draw, in the order a remote reads them. Anything else in a file's `keys` is kept too.
var remoteKeys = []string{"power", "up", "down", "left", "right", "select", "back", "home", "menu", "info", "guide", "exit",
	"play", "pause", "play_pause", "rewind", "fast_forward", "previous", "next",
	"volume_up", "volume_down", "mute", "channel_up", "channel_down", "0", "1", "2", "3", "4", "5", "6", "7", "8", "9"}

// remoteCommands is each integration's name for each key, sent with remote.send_command to its remote entity.
var remoteCommands = map[string]map[string]string{
	"samsungtv": {"power": "KEY_POWER", "up": "KEY_UP", "down": "KEY_DOWN", "left": "KEY_LEFT", "right": "KEY_RIGHT", "select": "KEY_ENTER",
		"back": "KEY_RETURN", "home": "KEY_HOME", "menu": "KEY_MENU", "info": "KEY_INFO", "guide": "KEY_GUIDE", "exit": "KEY_EXIT",
		"play": "KEY_PLAY", "pause": "KEY_PAUSE", "rewind": "KEY_REWIND", "fast_forward": "KEY_FF",
		"volume_up": "KEY_VOLUP", "volume_down": "KEY_VOLDOWN", "mute": "KEY_MUTE", "channel_up": "KEY_CHUP", "channel_down": "KEY_CHDOWN",
		"0": "KEY_0", "1": "KEY_1", "2": "KEY_2", "3": "KEY_3", "4": "KEY_4", "5": "KEY_5", "6": "KEY_6", "7": "KEY_7", "8": "KEY_8", "9": "KEY_9"},
	"androidtv_remote": {"power": "POWER", "up": "DPAD_UP", "down": "DPAD_DOWN", "left": "DPAD_LEFT", "right": "DPAD_RIGHT", "select": "DPAD_CENTER",
		"back": "BACK", "home": "HOME", "menu": "MENU", "info": "INFO", "guide": "GUIDE",
		"play": "MEDIA_PLAY", "pause": "MEDIA_PAUSE", "play_pause": "MEDIA_PLAY_PAUSE", "rewind": "MEDIA_REWIND", "fast_forward": "MEDIA_FAST_FORWARD",
		"previous": "MEDIA_PREVIOUS", "next": "MEDIA_NEXT",
		"volume_up": "VOLUME_UP", "volume_down": "VOLUME_DOWN", "mute": "MUTE", "channel_up": "CHANNEL_UP", "channel_down": "CHANNEL_DOWN",
		"0": "0", "1": "1", "2": "2", "3": "3", "4": "4", "5": "5", "6": "6", "7": "7", "8": "8", "9": "9"},
	"roku": {"power": "power", "up": "up", "down": "down", "left": "left", "right": "right", "select": "select", "back": "back", "home": "home",
		"info": "info", "play_pause": "play", "rewind": "reverse", "fast_forward": "forward",
		"volume_up": "volume_up", "volume_down": "volume_down", "mute": "volume_mute", "channel_up": "channel_up", "channel_down": "channel_down"},
	"apple_tv": {"up": "up", "down": "down", "left": "left", "right": "right", "select": "select", "back": "menu", "home": "home", "menu": "top_menu",
		"play": "play", "pause": "pause", "play_pause": "play_pause", "rewind": "skip_backward", "fast_forward": "skip_forward",
		"previous": "previous", "next": "next", "volume_up": "volume_up", "volume_down": "volume_down", "channel_up": "channel_up", "channel_down": "channel_down"},
	"braviatv": {"power": "Power", "up": "Up", "down": "Down", "left": "Left", "right": "Right", "select": "Confirm", "back": "Return", "home": "Home",
		"menu": "Options", "info": "Display", "guide": "EPG", "exit": "Exit", "play": "Play", "pause": "Pause", "rewind": "Rewind", "fast_forward": "Forward",
		"previous": "Prev", "next": "Next", "volume_up": "VolumeUp", "volume_down": "VolumeDown", "mute": "Mute", "channel_up": "ChannelUp", "channel_down": "ChannelDown",
		"0": "Num0", "1": "Num1", "2": "Num2", "3": "Num3", "4": "Num4", "5": "Num5", "6": "Num6", "7": "Num7", "8": "Num8", "9": "Num9"},
}

// webOS TVs have no remote entity: buttons go to the media player through the integration's own service.
var webosButtons = map[string]string{"up": "UP", "down": "DOWN", "left": "LEFT", "right": "RIGHT", "select": "ENTER", "back": "BACK", "home": "HOME",
	"menu": "MENU", "info": "INFO", "guide": "GUIDE", "exit": "EXIT", "play": "PLAY", "pause": "PAUSE", "rewind": "REWIND", "fast_forward": "FASTFORWARD",
	"volume_up": "VOLUMEUP", "volume_down": "VOLUMEDOWN", "mute": "MUTE", "channel_up": "CHANNELUP", "channel_down": "CHANNELDOWN",
	"0": "0", "1": "1", "2": "2", "3": "3", "4": "4", "5": "5", "6": "6", "7": "7", "8": "8", "9": "9"}

func init() {
	remoteCommands["samsungtv_smart"] = remoteCommands["samsungtv"] // the custom integration takes the same key names
}

// Integrations the add-on makes a remote for by itself, best first: when one television is in Home Assistant twice
// (the core integration and a custom one), the first wins.
var remotePlatforms = []string{"samsungtv", "androidtv_remote", "apple_tv", "roku", "braviatv", "webostv", "samsungtv_smart"}

func remotePlatformRank(p string) int {
	for i, q := range remotePlatforms {
		if p == q {
			return i
		}
	}
	return len(remotePlatforms)
}

// playerOnlyRemotes are the integrations whose media player drives the television itself (power, source,
// volume) with no remote entity beside it. Any other player without a remote is not a remote: a Chromecast
// built into the set (cast), a Music Assistant or DLNA player, a Sonos.
var playerOnlyRemotes = map[string]bool{"webostv": true, "vizio": true, "philips_js": true, "panasonic_viera": true, "lg_netcast": true, "androidtv": true}

func sendCommand(remote, command string) Action {
	return Action{Domain: "remote", Service: "send_command", Entity: remote, Data: map[string]any{"command": command}}
}

func playerCall(player, service string) Action {
	return Action{Domain: "media_player", Service: service, Entity: player}
}

// autoRemotes: one remote per device that has a remote entity or a television, paired on the device: the remote
// for the keys, the media player for power, what is playing, its inputs and its volume.
func (x *index) autoRemotes() []RemoteMapping {
	type cand struct {
		remote, player ent
	}
	byDev := map[string]*cand{}
	var order []string
	for _, e := range x.ents {
		if e.device == "" || kiosks[e.platform] || vehicles[e.platform] {
			continue
		}
		isTV := e.domain == "media_player" && (e.class == "tv" || remoteCommands[e.platform] != nil || e.platform == "webostv")
		if e.domain != "remote" && !isTV {
			continue
		}
		c := byDev[e.device]
		if c == nil {
			c = &cand{}
			byDev[e.device] = c
			order = append(order, e.device)
		}
		switch {
		case e.domain == "remote" && c.remote.id == "":
			c.remote = e
		case e.domain == "media_player" && (c.player.id == "" || e.class == "tv"):
			c.player = e
		}
	}
	var out []RemoteMapping
	var areas []string // out[i]'s area, so that a television known twice is one remote, while two sets of the same
	// default name in different rooms (two "Samsung TV"s) stay two. The area counts only when both are known.
	var macs []string // out[i]'s MAC address, the surer sign of one television known twice
	for _, dev := range order {
		c := byDev[dev]
		platform := firstOf(c.remote.platform, c.player.platform)
		if c.player.id == "" && remoteCommands[platform] == nil {
			continue // a bare remote of an integration whose keys the add-on does not know: the file can map them
		}
		if c.remote.id == "" && remoteCommands[platform] == nil && !playerOnlyRemotes[platform] {
			// A media player alone is a remote only on an integration that speaks to the television itself. The
			// Frame's Chromecast (cast) or its Music Assistant player is a way to play to the set, not to drive it.
			continue
		}
		rm := RemoteMapping{Remote: c.remote.id, Player: c.player.id, Name: remoteName(firstOf(x.deviceName[dev], c.player.name, c.remote.name))}
		area := firstOf(c.player.area, c.remote.area)
		mac := x.deviceMAC[dev]
		twin := -1
		for i, o := range out {
			sameName := strings.EqualFold(o.Name, rm.Name) && (area == "" || areas[i] == "" || area == areas[i])
			if sameName || (mac != "" && mac == macs[i]) { // two integrations' devices for one set share its MAC
				twin = i
			}
		}
		if twin >= 0 {
			if remotePlatformRank(platform) < remotePlatformRank(firstOf(x.byID[out[twin].Remote].platform, x.byID[out[twin].Player].platform)) {
				out[twin], areas[twin] = rm, firstOf(area, areas[twin])
			}
			macs[twin] = firstOf(macs[twin], mac)
			continue
		}
		out, areas, macs = append(out, rm), append(areas, area), append(macs, mac)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// remoteName takes the device's name without the integration's suffixes.
func remoteName(n string) string {
	for _, suffix := range []string{" Remote", " remote", " TV Remote"} {
		n = strings.TrimSuffix(n, suffix)
	}
	return strings.TrimSpace(n)
}

// renderRemote resolves one remote, the file's or the add-on's own, to actions. ok is false when there is nothing
// to press; problems name what in the file could not be used.
func (x *index) renderRemote(rm RemoteMapping, names map[string]string) (r Remote, problems []string, ok bool) {
	problem := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	exists := func(id string) string {
		if id == "" || x.d.States[id] != nil {
			return id
		}
		problem("remote %s: Home Assistant has no %s", firstOf(rm.ID, rm.Name, rm.Remote, rm.Player), id)
		return ""
	}
	remote, player, volume := exists(rm.Remote), exists(rm.Player), exists(rm.Volume)
	if remote != "" && domain(remote) != "remote" {
		problem("remote %s: %s is not a remote entity", firstOf(rm.ID, rm.Name), remote)
		remote = ""
	}
	platform := firstOf(x.byID[remote].platform, x.byID[player].platform)
	keys := map[string]Action{}
	if remote != "" {
		for key, cmd := range remoteCommands[platform] {
			keys[key] = sendCommand(remote, cmd)
		}
	} else if platform == "webostv" && player != "" {
		for key, b := range webosButtons {
			keys[key] = Action{Domain: "webostv", Service: "button", Entity: player, Data: map[string]any{"button": b}}
		}
	}
	// What the remote has no key for, the media player does.
	if player != "" {
		for key, svc := range map[string]string{"power_on": "turn_on", "power_off": "turn_off", "play": "media_play", "pause": "media_pause",
			"play_pause": "media_play_pause", "previous": "media_previous_track", "next": "media_next_track"} {
			if _, has := keys[key]; !has {
				keys[key] = playerCall(player, svc)
			}
		}
		delete(keys, "power") // on and off are the player's: the remote's power key would toggle art mode on a Frame
	}
	// The volume: an explicit entity (a receiver) takes the volume keys; else the remote's, else the player's.
	if volume != "" {
		keys["volume_up"], keys["volume_down"] = playerCall(volume, "volume_up"), playerCall(volume, "volume_down")
		keys["mute"] = playerCall(volume, "volume_mute") // without is_volume_muted: the app toggles
	} else {
		volume = player
		for key, svc := range map[string]string{"volume_up": "volume_up", "volume_down": "volume_down", "mute": "volume_mute"} {
			if _, has := keys[key]; !has && player != "" {
				keys[key] = playerCall(player, svc)
			}
		}
	}
	for _, key := range sortedKeys(rm.Keys) {
		am := rm.Keys[key]
		a, err := am.action()
		if err != nil {
			problem("remote %s, key %s: %v", firstOf(rm.ID, rm.Name), key, err)
			continue
		}
		if a.Entity != "" && x.d.States[a.Entity] == nil {
			problem("remote %s, key %s: Home Assistant has no %s", firstOf(rm.ID, rm.Name), key, a.Entity)
			continue
		}
		if a.Domain == "" {
			delete(keys, key) // `none`: not drawn
			continue
		}
		keys[key] = a
	}
	// Inputs: a media player's source list as Home Assistant has it, or the file's own options.
	var inputs *Inputs
	switch {
	case rm.Inputs == nil:
		if st := x.d.States[player]; st != nil {
			if list, _ := st.Attributes["source_list"].([]any); len(list) > 0 {
				inputs = &Inputs{Entity: player}
			}
		}
	case rm.Inputs.Entity != "":
		if e := exists(rm.Inputs.Entity); e != "" {
			inputs = &Inputs{Entity: e}
		}
	case len(rm.Inputs.Options) > 0:
		inputs = &Inputs{}
		for _, o := range rm.Inputs.Options {
			a, err := o.Action.action()
			if err != nil {
				problem("remote %s, input %s: %v", firstOf(rm.ID, rm.Name), o.Name, err)
				continue
			}
			if a.Entity != "" && x.d.States[a.Entity] == nil {
				problem("remote %s, input %s: Home Assistant has no %s", firstOf(rm.ID, rm.Name), o.Name, a.Entity)
				continue
			}
			io := InputOption{Name: o.Name, Action: a}
			for ent, val := range o.When {
				io.Entity, io.Value = exists(ent), fmt.Sprint(val)
			}
			inputs.Options = append(inputs.Options, io)
		}
		if len(inputs.Options) == 0 {
			inputs = nil
		}
	}
	var buttons []RemoteButton
	for _, b := range rm.Buttons {
		a, err := b.Action.action()
		if err != nil {
			problem("remote %s, button %s: %v", firstOf(rm.ID, rm.Name), b.Name, err)
			continue
		}
		if a.Entity != "" && x.d.States[a.Entity] == nil {
			problem("remote %s, button %s: Home Assistant has no %s", firstOf(rm.ID, rm.Name), b.Name, a.Entity)
			continue
		}
		buttons = append(buttons, RemoteButton{Name: b.Name, Icon: b.Icon, Action: a})
	}
	if len(keys) == 0 && inputs == nil && len(buttons) == 0 {
		return r, problems, false
	}
	id := firstOf(rm.ID, remote, player)
	dev := firstOf(x.byID[remote].device, x.byID[player].device)
	var readouts []string
	if rm.Readouts != nil {
		for _, s := range rm.Readouts { // the file's list, as given; [] turns the readouts off
			if e := exists(s); e != "" {
				readouts = append(readouts, e)
			}
		}
	} else {
		readouts = x.remoteReadouts(x.byID[remote].device, x.byID[player].device)
	}
	r = Remote{
		ID:     id,
		Name:   firstOf(rm.Name, names[id], names[player], remoteName(firstOf(x.deviceName[dev], x.byID[player].name, x.byID[remote].name)), pretty(id)),
		Room:   firstOf(rm.Room, x.byID[remote].area, x.byID[player].area),
		Player: player, Volume: volume, Keys: keys, Inputs: inputs, Buttons: buttons, Readouts: readouts,
	}
	if st := x.d.States[r.Volume]; st == nil || st.Attributes["volume_level"] == nil {
		r.Volume = "" // no level to show: the keys still work
	}
	return r, problems, true
}

// remoteReadouts: the sensors worth a line on a remote, found on the set's own devices and on any device that shares
// a MAC address with them (the UniFi Network integration's client device for the television, say): its throughput
// (data_rate sensors, received before sent) and its power draw. Nothing else: a television's device may carry a
// dozen diagnostic sensors nobody wants on a remote.
func (x *index) remoteReadouts(devices ...string) []string {
	macs := map[string]bool{}
	on := map[string]bool{}
	for _, d := range devices {
		if d == "" {
			continue
		}
		on[d] = true
		if m := x.deviceMAC[d]; m != "" {
			macs[m] = true
		}
	}
	for d, m := range x.deviceMAC {
		if macs[m] {
			on[d] = true
		}
	}
	var rate, power []string
	for _, e := range x.ents {
		if e.domain != "sensor" || e.restored || !on[e.device] {
			continue
		}
		switch e.class {
		case "data_rate":
			rate = append(rate, e.id)
		case "power":
			power = append(power, e.id)
		}
	}
	// Received before sent, then by name, so two sets read the same way.
	sort.SliceStable(rate, func(i, j int) bool { return rateRank(x.byID[rate[i]]) < rateRank(x.byID[rate[j]]) })
	sort.Strings(power)
	out := append(rate, power...)
	if len(out) > 4 {
		out = out[:4]
	}
	return out
}

func rateRank(e ent) string {
	n := strings.ToLower(e.name + " " + e.id)
	switch {
	case strings.Contains(n, "rx") || strings.Contains(n, "receiv") || strings.Contains(n, "down"):
		return "0 " + n
	case strings.Contains(n, "tx") || strings.Contains(n, "transmit") || strings.Contains(n, "up"):
		return "1 " + n
	}
	return "2 " + n
}
