package server

import "strings"

func ColorSortKey(colors string) int {
	switch len(colors) {
	case 0:
		return 5
	case 1:
		switch colors {
		case "W":
			return 0
		case "U":
			return 1
		case "B":
			return 2
		case "R":
			return 3
		case "G":
			return 4
		default:
			return 5
		}
	default:
		return 6
	}
}

var typeOrder = map[string]int{
	"planeswalker": 0,
	"creature":     1,
	"artifact":     2,
	"enchantment":  3,
	"instant":      4,
	"sorcery":      5,
	"land":         6,
	"battle":       7,
}

var supertypes = map[string]bool{
	"legendary": true,
	"basic":     true,
	"snow":      true,
	"world":     true,
	"elite":     true,
	"ongoing":   true,
}

func TypeSortKey(typeLine string) int {
	if typeLine == "" {
		return 8
	}
	left := typeLine
	if i := strings.Index(typeLine, " — "); i >= 0 {
		left = typeLine[:i]
	}
	for _, token := range strings.Split(left, " ") {
		key := strings.ToLower(token)
		if supertypes[key] {
			continue
		}
		if idx, ok := typeOrder[key]; ok {
			return idx
		}
	}
	return 8
}
