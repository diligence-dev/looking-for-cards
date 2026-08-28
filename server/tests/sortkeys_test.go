package server_test

import (
	"testing"

	"github.com/diligence-dev/looking-for-cards/server"
)

func TestColorSortKey_SingleLetters(t *testing.T) {
	cases := map[string]int{
		"W": 0,
		"U": 1,
		"B": 2,
		"R": 3,
		"G": 4,
	}
	for colors, want := range cases {
		if got := server.ColorSortKey(colors); got != want {
			t.Errorf("ColorSortKey(%q) = %d, want %d", colors, got, want)
		}
	}
}

func TestColorSortKey_EmptyIsColorless(t *testing.T) {
	if got := server.ColorSortKey(""); got != 5 {
		t.Errorf("ColorSortKey(\"\") = %d, want 5", got)
	}
}

func TestColorSortKey_Multicolor(t *testing.T) {
	cases := []string{"WU", "UB", "RGB", "WR", "WUBRG"}
	for _, colors := range cases {
		if got := server.ColorSortKey(colors); got != 6 {
			t.Errorf("ColorSortKey(%q) = %d, want 6", colors, got)
		}
	}
}

func TestColorSortKey_UnknownLetterIsColorless(t *testing.T) {
	if got := server.ColorSortKey("X"); got != 5 {
		t.Errorf("ColorSortKey(\"X\") = %d, want 5", got)
	}
}

func TestTypeSortKey_SingleTokens(t *testing.T) {
	cases := map[string]int{
		"Planeswalker": 0,
		"Creature":    1,
		"Artifact":    2,
		"Enchantment": 3,
		"Instant":     4,
		"Sorcery":     5,
		"Land":        6,
		"Battle":      7,
	}
	for typeLine, want := range cases {
		if got := server.TypeSortKey(typeLine); got != want {
			t.Errorf("TypeSortKey(%q) = %d, want %d", typeLine, got, want)
		}
	}
}

func TestTypeSortKey_Empty(t *testing.T) {
	if got := server.TypeSortKey(""); got != 8 {
		t.Errorf("TypeSortKey(\"\") = %d, want 8", got)
	}
}

func TestTypeSortKey_SkipSupertypes(t *testing.T) {
	cases := map[string]int{
		"Legendary Creature": 1,
		"Basic Land":         6,
		"Snow Instant":       4,
		"World Enchantment":  3,
		"Elite Creature":     1,
		"Ongoing Sorcery":    5,
	}
	for typeLine, want := range cases {
		if got := server.TypeSortKey(typeLine); got != want {
			t.Errorf("TypeSortKey(%q) = %d, want %d", typeLine, got, want)
		}
	}
}

func TestTypeSortKey_FirstMatchWins(t *testing.T) {
	if got := server.TypeSortKey("Artifact Creature"); got != 2 {
		t.Errorf("TypeSortKey(\"Artifact Creature\") = %d, want 2 (Artifact beats Creature)", got)
	}
}

func TestTypeSortKey_EmDashParsesLeftSideOnly(t *testing.T) {
	cases := map[string]int{
		"Creature — Goblin":          1,
		"Legendary Planeswalker — Wrenn": 0,
		"Basic Land — Mountain":      6,
		"Instant — Subtype":          4,
	}
	for typeLine, want := range cases {
		if got := server.TypeSortKey(typeLine); got != want {
			t.Errorf("TypeSortKey(%q) = %d, want %d", typeLine, got, want)
		}
	}
}

func TestTypeSortKey_NoMatchIsEight(t *testing.T) {
	if got := server.TypeSortKey("Something Weird"); got != 8 {
		t.Errorf("TypeSortKey(\"Something Weird\") = %d, want 8", got)
	}
}
