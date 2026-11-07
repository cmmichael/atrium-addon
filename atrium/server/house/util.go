package house

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

func set(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		if s != "" {
			m[s] = true
		}
	}
	return m
}

func domain(id string) string {
	if i := strings.Index(id, "."); i > 0 && i < len(id)-1 {
		return id[:i]
	}
	return ""
}

func firstOf(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// strs reads a string or a list of strings.
func strs(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		var out []string
		for _, i := range x {
			if s, ok := i.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// get walks nested objects: get(m, "water", "leak").
func get(m map[string]any, path ...string) any {
	var cur any = m
	for _, p := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = obj[p]
	}
	return cur
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func containsAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

func concat(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}

func rank(order map[string]int, key string) int {
	if r, ok := order[key]; ok {
		return r
	}
	return 9
}

func parseFloat(s string) (float64, bool) {
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}

func capitalizedFirst(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if size == 0 {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}

// MARK: Names and icons

func pretty(id string) string {
	words := id
	if i := strings.LastIndex(id, "."); i >= 0 {
		words = id[i+1:]
	}
	return capitalizedFirst(strings.NewReplacer("_", " ", "-", " ").Replace(words))
}

// "Family Room Floor Lamp" in Family Room → "Floor lamp".
func short(name, room string) string {
	for _, prefix := range []string{room, strings.ReplaceAll(room, " Room", ""), strings.ReplaceAll(room, "'s", "")} {
		if strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix)+" ") && len(name) > len(prefix)+1 {
			return capitalizedFirst(strings.TrimSpace(name[len(prefix)+1:]))
		}
	}
	return name
}

var icons = [][2]string{
	{"kitchen", "fork.knife"}, {"dining", "fork.knife.circle"}, {"family", "sofa"}, {"living", "sofa"}, {"lounge", "sofa"}, {"garage", "car"},
	{"gym", "dumbbell"}, {"office", "desktopcomputer"}, {"study", "desktopcomputer"}, {"bath", "shower"}, {"powder", "shower"}, {"laundry", "washer"},
	{"pantry", "cabinet"}, {"stair", "stairs"}, {"hall", "door.left.hand.open"}, {"entry", "door.left.hand.open"}, {"play", "gamecontroller"},
	{"game", "gamecontroller"}, {"server", "server.rack"}, {"bed", "bed.double"}, {"yard", "tree"}, {"garden", "tree"}, {"patio", "tree"},
	{"exterior", "tree"}, {"outside", "tree"}, {"porch", "door.left.hand.open"}, {"deck", "tree"}, {"pool", "figure.pool.swim"}, {"room", "bed.double"},
}

func iconFor(room string) string {
	n := strings.ToLower(room)
	for _, ic := range icons {
		if strings.Contains(n, ic[0]) {
			return ic[1]
		}
	}
	return "square.split.bottomrightquarter"
}
