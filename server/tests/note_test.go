package server_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diligence-dev/looking-for-cards/server"
)

func TestAdd_NoteStoredAndExposed(t *testing.T) {
	srv := newTestServer(t)
	cards := []map[string]any{
		{"name": "Lightning Bolt", "set": "", "colors": "R", "type_line": "Instant", "note": "English, M15 frame"},
	}
	res := postCards(t, srv, "alice", cards)
	if res.Status != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", res.Status, res.Body)
	}
	listRes := doRequest(t, srv, http.MethodGet, "/api/entries", nil)
	entries := decodeEntries(t, listRes.Body)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Note != "English, M15 frame" {
		t.Fatalf("expected note %q, got %q", "English, M15 frame", entries[0].Note)
	}
}

func TestAdd_NoteTruncatesTo50(t *testing.T) {
	srv := newTestServer(t)
	long := strings.Repeat("a", 60)
	cards := []map[string]any{
		{"name": "Lightning Bolt", "set": "", "colors": "R", "type_line": "Instant", "note": long},
	}
	res := postCards(t, srv, "alice", cards)
	if res.Status != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", res.Status, res.Body)
	}
	created, errs := decodeAddResponse(t, res.Body)
	if created != 1 {
		t.Fatalf("expected created=1, got %d", created)
	}
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors for truncated note, got %v", errs)
	}
	listRes := doRequest(t, srv, http.MethodGet, "/api/entries", nil)
	entries := decodeEntries(t, listRes.Body)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if got := len([]rune(entries[0].Note)); got != 50 {
		t.Fatalf("expected note truncated to 50 runes, got %d (%q)", got, entries[0].Note)
	}
	if entries[0].Note != strings.Repeat("a", 50) {
		t.Fatalf("expected %q, got %q", strings.Repeat("a", 50), entries[0].Note)
	}
}

func TestAdd_NoteTrimmed(t *testing.T) {
	srv := newTestServer(t)
	cards := []map[string]any{
		{"name": "Bolt", "colors": "R", "type_line": "Instant", "note": "  foil  "},
	}
	res := postCards(t, srv, "alice", cards)
	if res.Status != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", res.Status, res.Body)
	}
	listRes := doRequest(t, srv, http.MethodGet, "/api/entries", nil)
	entries := decodeEntries(t, listRes.Body)
	if entries[0].Note != "foil" {
		t.Fatalf("expected trimmed note %q, got %q", "foil", entries[0].Note)
	}
}

func TestAdd_SameCardDifferentNotesSeparateEntries(t *testing.T) {
	srv := newTestServer(t)
	cards1 := []map[string]any{
		{"name": "Bolt", "set": "", "colors": "R", "type_line": "Instant", "note": "English"},
	}
	cards2 := []map[string]any{
		{"name": "Bolt", "set": "", "colors": "R", "type_line": "Instant", "note": "German"},
	}
	res1 := postCards(t, srv, "alice", cards1)
	if res1.Status != http.StatusCreated {
		t.Fatalf("first add: expected 201, got %d: %s", res1.Status, res1.Body)
	}
	res2 := postCards(t, srv, "bob", cards2)
	if res2.Status != http.StatusCreated {
		t.Fatalf("second add: expected 201, got %d: %s", res2.Status, res2.Body)
	}
	if got := countRows(t, srv.DB(), "SELECT COUNT(*) FROM cards"); got != 1 {
		t.Fatalf("expected 1 card row, got %d", got)
	}
	if got := countRows(t, srv.DB(), "SELECT COUNT(*) FROM entries"); got != 2 {
		t.Fatalf("expected 2 entries, got %d", got)
	}
	listRes := doRequest(t, srv, http.MethodGet, "/api/entries", nil)
	entries := decodeEntries(t, listRes.Body)
	notes := map[string]bool{}
	for _, e := range entries {
		notes[e.Note] = true
	}
	if !notes["English"] || !notes["German"] {
		t.Fatalf("expected notes English and German, got %v", notes)
	}
}

func TestAdd_SameSeekerDifferentNotesSeparateEntries(t *testing.T) {
	srv := newTestServer(t)
	res1 := postCards(t, srv, "alice", []map[string]any{
		{"name": "Bolt", "set": "", "colors": "R", "type_line": "Instant", "note": "English"},
	})
	if res1.Status != http.StatusCreated {
		t.Fatalf("first add: expected 201, got %d: %s", res1.Status, res1.Body)
	}
	res2 := postCards(t, srv, "alice", []map[string]any{
		{"name": "Bolt", "set": "", "colors": "R", "type_line": "Instant", "note": "German"},
	})
	if res2.Status != http.StatusCreated {
		t.Fatalf("second add: expected 201, got %d: %s", res2.Status, res2.Body)
	}
	if got := countRows(t, srv.DB(), "SELECT COUNT(*) FROM cards"); got != 1 {
		t.Fatalf("expected 1 card row, got %d", got)
	}
	if got := countRows(t, srv.DB(), "SELECT COUNT(*) FROM entries WHERE seeker_name='alice'"); got != 2 {
		t.Fatalf("expected 2 entries for alice, got %d", got)
	}
	listRes := doRequest(t, srv, http.MethodGet, "/api/entries", nil)
	entries := decodeEntries(t, listRes.Body)
	notes := map[string]bool{}
	for _, e := range entries {
		if e.SeekerName == "alice" {
			notes[e.Note] = true
		}
	}
	if !notes["English"] || !notes["German"] {
		t.Fatalf("expected alice notes English and German, got %v", notes)
	}
}

func TestAdd_NoteEmptyOmitted(t *testing.T) {
	srv := newTestServer(t)
	cards := []map[string]any{
		{"name": "Bolt", "colors": "R", "type_line": "Instant"},
	}
	res := postCards(t, srv, "alice", cards)
	if res.Status != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", res.Status, res.Body)
	}
	listRes := doRequest(t, srv, http.MethodGet, "/api/entries", nil)
	entries := decodeEntries(t, listRes.Body)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Note != "" {
		t.Fatalf("expected empty note, got %q", entries[0].Note)
	}
	var raw struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal(listRes.Body, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if _, ok := raw.Entries[0]["note"]; !ok {
		t.Fatalf("expected note field present in JSON even when empty")
	}
}

func TestList_NotePreservedAcrossGiverOps(t *testing.T) {
	srv := newTestServer(t)
	cards := []map[string]any{
		{"name": "Bolt", "colors": "R", "type_line": "Instant", "note": "keep me"},
	}
	postCards(t, srv, "alice", cards)
	listRes := doRequest(t, srv, http.MethodGet, "/api/entries", nil)
	entries := decodeEntries(t, listRes.Body)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	id := entries[0].ID
	res := doRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/entries/%d/giver?user=bob", id), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("set giver: expected 200, got %d: %s", res.Status, res.Body)
	}
	listRes = doRequest(t, srv, http.MethodGet, "/api/entries", nil)
	entries = decodeEntries(t, listRes.Body)
	if entries[0].Note != "keep me" {
		t.Fatalf("note lost after set giver, got %q", entries[0].Note)
	}
	res = doRequest(t, srv, http.MethodDelete, fmt.Sprintf("/api/entries/%d/giver?user=bob", id), nil)
	if res.Status != http.StatusNoContent {
		t.Fatalf("clear giver: expected 204, got %d: %s", res.Status, res.Body)
	}
	listRes = doRequest(t, srv, http.MethodGet, "/api/entries", nil)
	entries = decodeEntries(t, listRes.Body)
	if entries[0].Note != "keep me" {
		t.Fatalf("note lost after clear giver, got %q", entries[0].Note)
	}
}

func TestMigrate_EntryNoteColumnExists(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := server.InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('entries') WHERE name='note'").Scan(&count); err != nil {
		t.Fatalf("pragma query: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected note column to exist after InitDB")
	}
	var note sql.NullString
	if err := db.QueryRow("SELECT note FROM entries LIMIT 1").Scan(&note); err != nil && err != sql.ErrNoRows {
		t.Fatalf("select note: %v", err)
	}

	// Simulate a legacy DB without the column, with a legacy row.
	if _, err := db.Exec(`ALTER TABLE entries DROP COLUMN note`); err != nil {
		t.Fatalf("drop note column: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('entries') WHERE name='note'").Scan(&count); err != nil {
		t.Fatalf("pragma query after drop: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected note column missing after DROP, got count=%d", count)
	}
	cardID, err := server.UpsertCard(db, "Legacy Bolt", "", "", "R", "Instant", 1, "")
	if err != nil {
		t.Fatalf("UpsertCard legacy: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO entries (card_id, seeker_name) VALUES (?, ?)`, cardID, "alice"); err != nil {
		t.Fatalf("insert legacy entry: %v", err)
	}
	db.Close()

	db2, err := server.InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB again: %v", err)
	}
	defer db2.Close()
	if err := db2.QueryRow("SELECT COUNT(*) FROM pragma_table_info('entries') WHERE name='note'").Scan(&count); err != nil {
		t.Fatalf("pragma query 2: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected note column still exists after second InitDB")
	}
	var got string
	if err := db2.QueryRow(`SELECT note FROM entries WHERE seeker_name='alice'`).Scan(&got); err != nil {
		t.Fatalf("read migrated legacy entry note: %v", err)
	}
	if got != "" {
		t.Fatalf("expected legacy entry note=%q, got %q", "", got)
	}
}
