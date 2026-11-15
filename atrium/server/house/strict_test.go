package house

import (
	"strings"
	"testing"
)

// One key under home: replaces only that key; "none" leaves one out; the rest stay guessed.
func TestTheHomeSectionMergesOverTheGuess(t *testing.T) {
	guess, _ := Render(Mapping{}, smallHome(), Config{})
	if guess.Summary.Weather == "" || guess.Summary.Alarm == "" {
		t.Fatalf("the small home should guess a weather and an alarm: %+v", guess.Summary)
	}
	h, _ := Render(mapped(t, "version: 2\nhome:\n  alarm: none\n"), smallHome(), Config{})
	if h.Summary.Weather != guess.Summary.Weather || h.Summary.Alarm != "" {
		t.Errorf("weather %q (want the guess %q), alarm %q (want none)", h.Summary.Weather, guess.Summary.Weather, h.Summary.Alarm)
	}
}

// A misspelt key deep in the file is refused, as one at the top is, not quietly dropped.
func TestUnknownKeysAreRefusedEverywhere(t *testing.T) {
	for _, text := range []string{
		"version: 3\nwidgets:\n  - name: W\n    items:\n      - {scene: scene.movie, colour: red}\n",
		"version: 3\nremotes:\n  - id: remote.x\n    inputs:\n      - {name: TV, action: {entity: media_player.x}, wen: {}}\n",
	} {
		if _, err := ParseMapping([]byte(text)); err == nil {
			t.Errorf("accepted:\n%s", text)
		}
	}
}

// A tariff without its net meter, and a light listed as a camera, are said, not silently skipped.
func TestMistakesInTheFileGetAProblemLine(t *testing.T) {
	_, problems := Render(mapped(t, "version: 3\nenergy:\n  tariff:\n    period_days: 30\n    tiers: [{rate: 0.3}]\nsecurity:\n  cameras: [light.kitchen]\n"), smallHome(), Config{})
	all := strings.Join(problems, "\n")
	if !strings.Contains(all, "tariff: no net meter") || !strings.Contains(all, "light.kitchen is listed as a camera") {
		t.Errorf("problems:\n%s", all)
	}
}
