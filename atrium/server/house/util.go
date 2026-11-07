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

func firstID(list []ent) string {
	if len(list) == 0 {
		return ""
	}
	return list[0].id
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
