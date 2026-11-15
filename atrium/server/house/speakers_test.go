package house

import (
	"fmt"
	"testing"
)

// Three real speakers in rooms; twenty Music Assistant players in no room that call themselves speakers (Macs,
// televisions, renderers); and one Music Assistant twin of a real speaker, in its room, by the same name with
// "MA" after it. Exactly the three are speakers.
func TestOnlyPlacedSpeakersThatAreNotMirrors(t *testing.T) {
	d := Dump{States: map[string]*State{}, Running: true, Areas: list(`[{"area_id":"kitchen","name":"Kitchen"},{"area_id":"den","name":"Den"}]`)}
	add := func(id, platform, device, deviceName, area, name string) {
		e := map[string]any{"entity_id": id, "platform": platform, "device_id": device}
		if area != "" {
			e["area_id"] = area
		}
		d.Entities = append(d.Entities, e)
		d.Devices = append(d.Devices, map[string]any{"id": device, "name": deviceName})
		d.States[id] = &State{State: "idle", Attributes: map[string]any{"friendly_name": name, "device_class": "speaker", "supported_features": 2048 | 512 | 4}}
	}
	add("media_player.kitchen", "cast", "d1", "Kitchen speaker", "kitchen", "Kitchen speaker")
	add("media_player.den", "sonos", "d2", "Den", "den", "Den")
	add("media_player.den_sub", "sonos", "d3", "Den Sub", "den", "Den Sub")
	for i := 0; i < 20; i++ {
		add(fmt.Sprint("media_player.ma_", i), "music_assistant", fmt.Sprint("ma", i), fmt.Sprint("Mac ", i), "", fmt.Sprint("Mac ", i))
	}
	add("media_player.kitchen_speaker_ma", "music_assistant", "ma-k", "Kitchen speaker", "kitchen", "Kitchen speaker MA")
	h, _ := Render(Mapping{}, d, Config{})
	eq(t, "speakers", h.Speakers, []string{"media_player.den", "media_player.den_sub", "media_player.kitchen"})
}
