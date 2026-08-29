package server_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/diligence-dev/looking-for-cards/server"
)

func seedEntry(t *testing.T, db *sql.DB, name, set, collectorNumber, colors, typeLine, seeker string) int64 {
	t.Helper()
	cardID, err := server.UpsertCard(db, name, set, collectorNumber, colors, typeLine, 0, "")
	if err != nil {
		t.Fatalf("UpsertCard failed: %v", err)
	}
	id, err := server.AddEntry(db, cardID, seeker)
	if err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}
	return id
}

func postCards(t *testing.T, srv *server.Server, user string, cards []map[string]any) apiResponse {
	t.Helper()
	return doRequest(t, srv, http.MethodPost, "/api/entries?user="+user, map[string]any{"cards": cards})
}

func decodeAddResponse(t *testing.T, body []byte) (int, []map[string]any) {
	t.Helper()
	var resp struct {
		Created int              `json:"created"`
		Errors  []map[string]any `json:"errors"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to decode add response: %v", err)
	}
	return resp.Created, resp.Errors
}

func TestAdd_BatchCreatesEntriesAndCardRows(t *testing.T) {
	srv := newTestServer(t)
	cards := []map[string]any{
		{"name": "Lightning Bolt", "set": "", "colors": "R", "type_line": "Instant"},
		{"name": "Lightning Bolt", "set": "", "colors": "R", "type_line": "Instant"},
		{"name": "Counterspell", "set": "", "colors": "UU", "type_line": "Instant"},
	}
	res := postCards(t, srv, "alice", cards)
	if res.Status != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", res.Status, res.Body)
	}
	created, errs := decodeAddResponse(t, res.Body)
	if created != 3 {
		t.Fatalf("expected created=3, got %d", created)
	}
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got %d: %v", len(errs), errs)
	}
	if got := countRows(t, srv.DB(), "SELECT COUNT(*) FROM cards"); got != 2 {
		t.Fatalf("expected 2 card rows, got %d", got)
	}
	if got := countRows(t, srv.DB(), "SELECT COUNT(*) FROM entries"); got != 3 {
		t.Fatalf("expected 3 entry rows, got %d", got)
	}

	var colorKey, typeKey int
	if err := srv.DB().QueryRow(`SELECT color_sort_key, type_sort_key FROM cards WHERE name=? AND set_code=?`, "Lightning Bolt", "").Scan(&colorKey, &typeKey); err != nil {
		t.Fatalf("failed to read sort keys: %v", err)
	}
	if colorKey != 3 {
		t.Errorf("expected color_sort_key=3 (R), got %d", colorKey)
	}
	if typeKey != 4 {
		t.Errorf("expected type_sort_key=4 (Instant), got %d", typeKey)
	}
}

func TestAdd_DuplicateLinesCreateSeparateEntriesOneCard(t *testing.T) {
	srv := newTestServer(t)
	cards := []map[string]any{
		{"name": "Lightning Bolt", "colors": "R", "type_line": "Instant"},
		{"name": "Lightning Bolt", "colors": "R", "type_line": "Instant"},
	}
	res := postCards(t, srv, "alice", cards)
	if res.Status != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", res.Status, res.Body)
	}
	created, errs := decodeAddResponse(t, res.Body)
	if created != 2 {
		t.Fatalf("expected created=2, got %d", created)
	}
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got %d", len(errs))
	}
	if got := countRows(t, srv.DB(), "SELECT COUNT(*) FROM cards"); got != 1 {
		t.Fatalf("expected 1 card row, got %d", got)
	}
	if got := countRows(t, srv.DB(), "SELECT COUNT(*) FROM entries"); got != 2 {
		t.Fatalf("expected 2 entry rows, got %d", got)
	}
}

func TestAdd_EmptyNameReportedAsError(t *testing.T) {
	srv := newTestServer(t)
	cards := []map[string]any{
		{"name": "", "colors": "R", "type_line": "Instant"},
	}
	res := postCards(t, srv, "alice", cards)
	if res.Status != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", res.Status, res.Body)
	}
	created, errs := decodeAddResponse(t, res.Body)
	if created != 0 {
		t.Fatalf("expected created=0, got %d", created)
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
	if errs[0]["error"] != "empty name" {
		t.Errorf("unexpected error: %v", errs[0])
	}
}

func TestAdd_MissingUserReturns400(t *testing.T) {
	srv := newTestServer(t)
	res := doRequest(t, srv, http.MethodPost, "/api/entries", map[string]any{"cards": []map[string]any{}})
	if res.Status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", res.Status, res.Body)
	}
}

func TestAdd_InvalidJSONReturns400(t *testing.T) {
	srv := newTestServer(t)
	r := doRequestRaw(t, srv, http.MethodPost, "/api/entries?user=alice", []byte("{not json"))
	if r.Status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", r.Status, r.Body)
	}
}

func TestList_Ordering(t *testing.T) {
	srv := newTestServer(t)
	db := srv.DB()
	seedEntry(t, db, "Angel", "", "", "W", "Creature", "alice")
	seedEntry(t, db, "Drake", "", "", "U", "Creature", "alice")
	seedEntry(t, db, "Zombie", "", "", "B", "Creature", "alice")
	seedEntry(t, db, "Chandra", "", "", "R", "Planeswalker", "alice")
	seedEntry(t, db, "Goblin", "", "", "R", "Creature", "alice")
	seedEntry(t, db, "Bolt", "", "", "R", "Instant", "alice")
	seedEntry(t, db, "Spark", "", "", "R", "Instant", "alice")
	seedEntry(t, db, "Mountain", "", "", "R", "Land", "alice")
	seedEntry(t, db, "Elf", "", "", "G", "Creature", "alice")
	seedEntry(t, db, "Sol Ring", "", "", "", "Artifact", "alice")
	seedEntry(t, db, "Maelstrom", "", "", "RG", "Creature", "alice")

	res := doRequest(t, srv, http.MethodGet, "/api/entries", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Status, res.Body)
	}
	entries := decodeEntries(t, res.Body)

	expected := []string{
		"Angel", "Drake", "Zombie",
		"Chandra", "Goblin", "Bolt", "Spark", "Mountain",
		"Elf", "Sol Ring", "Maelstrom",
	}
	got := entryNames(entries)
	if len(got) != len(expected) {
		t.Fatalf("expected %d entries, got %d (%v)", len(expected), len(got), got)
	}
	for i, want := range expected {
		if got[i] != want {
			t.Fatalf("position %d: expected %q, got %q (full order: %v)", i, want, got[i], got)
		}
	}
}

func TestList_OrderingByManaValue(t *testing.T) {
	srv := newTestServer(t)
	cards := []map[string]any{
		{"name": "Cherry", "colors": "W", "type_line": "Creature", "mana_value": 1.0},
		{"name": "Apple", "colors": "W", "type_line": "Creature", "mana_value": 2.0},
		{"name": "Banana", "colors": "W", "type_line": "Creature", "mana_value": 2.0},
	}
	res := postCards(t, srv, "alice", cards)
	if res.Status != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", res.Status, res.Body)
	}
	listRes := doRequest(t, srv, http.MethodGet, "/api/entries", nil)
	entries := decodeEntries(t, listRes.Body)
	got := entryNames(entries)
	expected := []string{"Cherry", "Apple", "Banana"}
	if len(got) != len(expected) {
		t.Fatalf("expected %d entries, got %d (%v)", len(expected), len(got), got)
	}
	for i, want := range expected {
		if got[i] != want {
			t.Fatalf("position %d: expected %q, got %q (full: %v)", i, want, got[i], got)
		}
	}
}

func TestCardMetadata_BackfillManaValueUpdatesSort(t *testing.T) {
	srv := newTestServer(t)
	db := srv.DB()
	seedEntry(t, db, "Apple", "", "", "W", "Creature", "alice")
	seedEntry(t, db, "Cherry", "", "", "W", "Creature", "alice")

	res := doRequest(t, srv, http.MethodPost, "/api/cards/metadata", map[string]any{
		"cards": []map[string]any{
			{"name": "Apple", "set": "", "collector_number": "", "mana_value": 2.0},
			{"name": "Cherry", "set": "", "collector_number": "", "mana_value": 1.0},
		},
	})
	if res.Status != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", res.Status, res.Body)
	}

	listRes := doRequest(t, srv, http.MethodGet, "/api/entries", nil)
	entries := decodeEntries(t, listRes.Body)
	got := entryNames(entries)
	expected := []string{"Cherry", "Apple"}
	if len(got) != len(expected) {
		t.Fatalf("expected %d entries, got %d (%v)", len(expected), len(got), got)
	}
	for i, want := range expected {
		if got[i] != want {
			t.Fatalf("position %d: expected %q, got %q (full: %v)", i, want, got[i], got)
		}
	}
}

func TestMigrate_ManaValueNullableConvertsLegacyZero(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := server.InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	db.Close()

	db, err = sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := db.Exec(`DROP INDEX IF EXISTS idx_cards_sort`); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	if _, err := db.Exec(`ALTER TABLE cards DROP COLUMN mana_value`); err != nil {
		t.Fatalf("drop mana_value: %v", err)
	}
	if _, err := db.Exec(`ALTER TABLE cards ADD COLUMN mana_value REAL NOT NULL DEFAULT 0`); err != nil {
		t.Fatalf("readd legacy mana_value: %v", err)
	}
	db.Exec(`INSERT INTO cards (name, set_code, collector_number, colors, type_line, mana_value, color_sort_key, type_sort_key) VALUES ('Zero','','','','Land',0,5,6)`)
	db.Exec(`INSERT INTO cards (name, set_code, collector_number, colors, type_line, mana_value, color_sort_key, type_sort_key) VALUES ('Two','','','','Creature',2,0,1)`)
	db.Close()

	db2, err := server.InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB again: %v", err)
	}
	defer db2.Close()

	var notnull int
	if err := db2.QueryRow(`SELECT "notnull" FROM pragma_table_info('cards') WHERE name='mana_value'`).Scan(&notnull); err != nil {
		t.Fatalf("pragma: %v", err)
	}
	if notnull != 0 {
		t.Fatalf("expected mana_value nullable after migration, got notnull=%d", notnull)
	}
	for _, c := range []struct{ name string; valid bool }{
		{"Zero", false},
		{"Two", true},
	} {
		var mv sql.NullFloat64
		if err := db2.QueryRow("SELECT mana_value FROM cards WHERE name=?", c.name).Scan(&mv); err != nil {
			t.Fatalf("read %s: %v", c.name, err)
		}
		if mv.Valid != c.valid {
			t.Errorf("%s: expected valid=%v, got valid=%v (val=%v)", c.name, c.valid, mv.Valid, mv.Float64)
		}
	}
}

func TestMigrate_RecomputeTypeSortKey(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := server.InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB #1: %v", err)
	}
	db.Exec(`INSERT INTO cards (name, set_code, collector_number, colors, type_line, mana_value, color_sort_key, type_sort_key) VALUES ('AlphaGolem','','','','Artifact Creature',2,5,2)`)
	db.Exec(`INSERT INTO cards (name, set_code, collector_number, colors, type_line, mana_value, color_sort_key, type_sort_key) VALUES ('TherosGod','','','','Enchantment Creature',3,5,3)`)
	db.Exec(`INSERT INTO cards (name, set_code, collector_number, colors, type_line, mana_value, color_sort_key, type_sort_key) VALUES ('SolRing','','','','Artifact',0,5,2)`)
	if _, err := db.Exec(`DELETE FROM schema_meta WHERE key='type_sort_key_recompute_v1'`); err != nil {
		t.Fatalf("clear marker: %v", err)
	}
	db.Close()

	db2, err := server.InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB #2: %v", err)
	}
	defer db2.Close()

	for _, c := range []struct {
		name string
		want int
	}{
		{"AlphaGolem", 1},
		{"TherosGod", 1},
		{"SolRing", 2},
	} {
		var got int
		if err := db2.QueryRow(`SELECT type_sort_key FROM cards WHERE name=?`, c.name).Scan(&got); err != nil {
			t.Fatalf("read %s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s: type_sort_key=%d, want %d", c.name, got, c.want)
		}
	}

	var marker int
	if err := db2.QueryRow(`SELECT COUNT(*) FROM schema_meta WHERE key='type_sort_key_recompute_v1'`).Scan(&marker); err != nil {
		t.Fatalf("query marker: %v", err)
	}
	if marker != 1 {
		t.Fatalf("expected type_sort_key_recompute_v1 marker set, got count=%d", marker)
	}
}

func TestList_TotalFieldReflectsCount(t *testing.T) {
	srv := newTestServer(t)
	seedEntry(t, srv.DB(), "Bolt", "", "", "R", "Instant", "alice")
	seedEntry(t, srv.DB(), "Drake", "", "", "U", "Creature", "bob")

	res := doRequest(t, srv, http.MethodGet, "/api/entries", nil)
	var resp struct {
		Entries []server.Entry `json:"entries"`
		Total   int            `json:"total"`
	}
	json.Unmarshal(res.Body, &resp)
	if resp.Total != 2 {
		t.Fatalf("expected total=2, got %d", resp.Total)
	}
	if len(resp.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(resp.Entries))
	}
}

func TestSetGiver_SelfOfferAllowed(t *testing.T) {
	srv := newTestServer(t)
	id := seedEntry(t, srv.DB(), "Bolt", "", "", "R", "Instant", "alice")
	res := doRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/entries/%d/giver?user=alice", id), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("expected 200 for self-offer, got %d: %s", res.Status, res.Body)
	}
}

func TestSetGiver_IdempotentReclaim(t *testing.T) {
	srv := newTestServer(t)
	id := seedEntry(t, srv.DB(), "Bolt", "", "", "R", "Instant", "alice")
	if res := doRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/entries/%d/giver?user=alice", id), nil); res.Status != http.StatusOK {
		t.Fatalf("first claim: expected 200, got %d: %s", res.Status, res.Body)
	}
	if res := doRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/entries/%d/giver?user=alice", id), nil); res.Status != http.StatusOK {
		t.Fatalf("re-claim: expected 200, got %d: %s", res.Status, res.Body)
	}
}

func TestSetGiver_ConflictReturns409WithEntry(t *testing.T) {
	srv := newTestServer(t)
	id := seedEntry(t, srv.DB(), "Bolt", "", "", "R", "Instant", "alice")

	if res := doRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/entries/%d/giver?user=alice", id), nil); res.Status != http.StatusOK {
		t.Fatalf("alice claim: expected 200, got %d", res.Status)
	}
	res := doRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/entries/%d/giver?user=bob", id), nil)
	if res.Status != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", res.Status, res.Body)
	}
	var resp struct {
		Error string      `json:"error"`
		Entry server.Entry `json:"entry"`
	}
	json.Unmarshal(res.Body, &resp)
	if resp.Error != "taken" {
		t.Errorf("expected error='taken', got %q", resp.Error)
	}
	if resp.Entry.GiverName != "alice" {
		t.Errorf("expected entry.giver='alice', got %q", resp.Entry.GiverName)
	}
}

func TestSetGiver_RemovedEntryReturns404(t *testing.T) {
	srv := newTestServer(t)
	id := seedEntry(t, srv.DB(), "Bolt", "", "", "R", "Instant", "alice")
	if res := doRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/entries/%d/remove?user=alice", id), nil); res.Status != http.StatusNoContent {
		t.Fatalf("remove: expected 204, got %d", res.Status)
	}
	res := doRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/entries/%d/giver?user=bob", id), nil)
	if res.Status != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", res.Status, res.Body)
	}
}

func TestSetGiver_MissingUserReturns400(t *testing.T) {
	srv := newTestServer(t)
	id := seedEntry(t, srv.DB(), "Bolt", "", "", "R", "Instant", "alice")
	res := doRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/entries/%d/giver", id), nil)
	if res.Status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", res.Status, res.Body)
	}
}

func TestClearGiver_OnlyCurrentGiverCanClear(t *testing.T) {
	srv := newTestServer(t)
	id := seedEntry(t, srv.DB(), "Bolt", "", "", "R", "Instant", "alice")
	doRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/entries/%d/giver?user=alice", id), nil)

	res := doRequest(t, srv, http.MethodDelete, fmt.Sprintf("/api/entries/%d/giver?user=bob", id), nil)
	if res.Status != http.StatusNotFound {
		t.Fatalf("bob clearing alice's offer: expected 404, got %d: %s", res.Status, res.Body)
	}
	res = doRequest(t, srv, http.MethodDelete, fmt.Sprintf("/api/entries/%d/giver?user=alice", id), nil)
	if res.Status != http.StatusNoContent {
		t.Fatalf("alice clearing own offer: expected 204, got %d: %s", res.Status, res.Body)
	}
}

func TestClearGiver_NoGiverReturns404(t *testing.T) {
	srv := newTestServer(t)
	id := seedEntry(t, srv.DB(), "Bolt", "", "", "R", "Instant", "alice")
	res := doRequest(t, srv, http.MethodDelete, fmt.Sprintf("/api/entries/%d/giver?user=alice", id), nil)
	if res.Status != http.StatusNotFound {
		t.Fatalf("clearing with no giver: expected 404, got %d: %s", res.Status, res.Body)
	}
}

func TestRemoveEntry_SeekerCancels(t *testing.T) {
	srv := newTestServer(t)
	id := seedEntry(t, srv.DB(), "Bolt", "", "", "R", "Instant", "alice")
	res := doRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/entries/%d/remove?user=alice", id), nil)
	if res.Status != http.StatusNoContent {
		t.Fatalf("first remove: expected 204, got %d: %s", res.Status, res.Body)
	}
	res = doRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/entries/%d/remove?user=alice", id), nil)
	if res.Status != http.StatusNotFound {
		t.Fatalf("second remove: expected 404, got %d: %s", res.Status, res.Body)
	}
}

func TestRemoveEntry_GiverCanRemoveMatched(t *testing.T) {
	srv := newTestServer(t)
	id := seedEntry(t, srv.DB(), "Bolt", "", "", "R", "Instant", "alice")
	doRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/entries/%d/giver?user=bob", id), nil)

	res := doRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/entries/%d/remove?user=bob", id), nil)
	if res.Status != http.StatusNoContent {
		t.Fatalf("giver remove: expected 204, got %d: %s", res.Status, res.Body)
	}
}

func TestRemoveEntry_NonPartyMatchedForbidden(t *testing.T) {
	srv := newTestServer(t)
	id := seedEntry(t, srv.DB(), "Bolt", "", "", "R", "Instant", "alice")
	doRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/entries/%d/giver?user=bob", id), nil)

	res := doRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/entries/%d/remove?user=charlie", id), nil)
	if res.Status != http.StatusForbidden {
		t.Fatalf("non-party remove: expected 403, got %d: %s", res.Status, res.Body)
	}
}

func TestRemoveEntry_NonPartyUnmatchedForbidden(t *testing.T) {
	srv := newTestServer(t)
	id := seedEntry(t, srv.DB(), "Bolt", "", "", "R", "Instant", "alice")
	res := doRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/entries/%d/remove?user=charlie", id), nil)
	if res.Status != http.StatusForbidden {
		t.Fatalf("non-party remove (unmatched): expected 403, got %d: %s", res.Status, res.Body)
	}
}

func TestList_ReturnsAllEntries(t *testing.T) {
	srv := newTestServer(t)
	for i := 0; i < 200; i++ {
		seedEntry(t, srv.DB(), fmt.Sprintf("Card %03d", i), "", "", "", "Artifact", "alice")
	}
	res := doRequest(t, srv, http.MethodGet, "/api/entries", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Status, res.Body)
	}
	var resp struct {
		Entries []server.Entry `json:"entries"`
		Total   int            `json:"total"`
	}
	json.Unmarshal(res.Body, &resp)
	if resp.Total != 200 {
		t.Fatalf("expected total=200, got %d", resp.Total)
	}
	if len(resp.Entries) != 200 {
		t.Fatalf("expected 200 entries returned, got %d", len(resp.Entries))
	}
	if resp.Entries[0].Card.Name != "Card 000" {
		t.Errorf("expected first=Card 000, got %q", resp.Entries[0].Card.Name)
	}
	if resp.Entries[199].Card.Name != "Card 199" {
		t.Errorf("expected last=Card 199, got %q", resp.Entries[199].Card.Name)
	}
}

func TestCardImageURLs_BackfillUpdatesAndExposesURL(t *testing.T) {
	srv := newTestServer(t)
	id := seedEntry(t, srv.DB(), "Bolt", "", "", "R", "Instant", "alice")

	res := doRequest(t, srv, http.MethodGet, "/api/entries", nil)
	entries := decodeEntries(t, res.Body)
	if len(entries) != 1 || entries[0].Card.ImageURL != "" {
		t.Fatalf("expected empty image_url before backfill, got %q", entries[0].Card.ImageURL)
	}

	imgURL := "https://cards.scryfall.io/normal/front/xx.jpg"
	backfillRes := doRequest(t, srv, http.MethodPost, "/api/cards/metadata", map[string]any{
		"cards": []map[string]any{
			{"name": "Bolt", "set": "", "image_url": imgURL},
		},
	})
	if backfillRes.Status != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", backfillRes.Status, backfillRes.Body)
	}

	res = doRequest(t, srv, http.MethodGet, "/api/entries", nil)
	entries = decodeEntries(t, res.Body)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Card.ImageURL != imgURL {
		t.Fatalf("expected image_url=%q, got %q", imgURL, entries[0].Card.ImageURL)
	}
	_ = id
}

func TestCardImageURLs_AddWithImageURLStoresIt(t *testing.T) {
	srv := newTestServer(t)
	imgURL := "https://cards.scryfall.io/normal/front/ab.jpg"
	cards := []map[string]any{
		{"name": "Bolt", "set": "", "colors": "R", "type_line": "Instant", "image_url": imgURL},
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
	if entries[0].Card.ImageURL != imgURL {
		t.Fatalf("expected image_url=%q, got %q", imgURL, entries[0].Card.ImageURL)
	}
}

func TestAdd_CollectorNumberDistinguishesPrintings(t *testing.T) {
	srv := newTestServer(t)
	cards := []map[string]any{
		{"name": "Faerie Guidemother", "set": "ELD", "collector_number": "11", "colors": "W", "type_line": "Creature"},
		{"name": "Faerie Guidemother", "set": "ELD", "collector_number": "12", "colors": "W", "type_line": "Creature"},
	}
	res := postCards(t, srv, "alice", cards)
	if res.Status != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", res.Status, res.Body)
	}
	created, errs := decodeAddResponse(t, res.Body)
	if created != 2 {
		t.Fatalf("expected created=2, got %d", created)
	}
	if len(errs) != 0 {
		t.Fatalf("expected 0 errors, got %d: %v", len(errs), errs)
	}
	if got := countRows(t, srv.DB(), "SELECT COUNT(*) FROM cards"); got != 2 {
		t.Fatalf("expected 2 card rows (different collector numbers), got %d", got)
	}
	if got := countRows(t, srv.DB(), "SELECT COUNT(*) FROM entries"); got != 2 {
		t.Fatalf("expected 2 entry rows, got %d", got)
	}
}

func TestAdd_SameNameSetNoCollectorNumberCollapsesToOneCard(t *testing.T) {
	srv := newTestServer(t)
	cards := []map[string]any{
		{"name": "Bolt", "set": "LEA", "collector_number": "", "colors": "R", "type_line": "Instant"},
		{"name": "Bolt", "set": "LEA", "collector_number": "", "colors": "R", "type_line": "Instant"},
	}
	res := postCards(t, srv, "alice", cards)
	if res.Status != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", res.Status, res.Body)
	}
	created, _ := decodeAddResponse(t, res.Body)
	if created != 2 {
		t.Fatalf("expected created=2 entries, got %d", created)
	}
	if got := countRows(t, srv.DB(), "SELECT COUNT(*) FROM cards"); got != 1 {
		t.Fatalf("expected 1 card row (same name+set, no collector number), got %d", got)
	}
}

func TestList_ExposesCollectorNumber(t *testing.T) {
	srv := newTestServer(t)
	cards := []map[string]any{
		{"name": "Faerie Guidemother", "set": "ELD", "collector_number": "11", "colors": "W", "type_line": "Creature", "image_url": "https://example.com/11.jpg"},
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
	if entries[0].Card.CollectorNumber != "11" {
		t.Fatalf("expected collector_number=11, got %q", entries[0].Card.CollectorNumber)
	}
}

func TestCardImageURLs_BackfillByCollectorNumber(t *testing.T) {
	srv := newTestServer(t)
	seedEntry(t, srv.DB(), "Bolt", "LEA", "63", "R", "Instant", "alice")
	seedEntry(t, srv.DB(), "Bolt", "LEA", "64", "R", "Instant", "alice")

	imgURL63 := "https://cards.scryfall.io/normal/front/63.jpg"
	backfillRes := doRequest(t, srv, http.MethodPost, "/api/cards/metadata", map[string]any{
		"cards": []map[string]any{
			{"name": "Bolt", "set": "LEA", "collector_number": "63", "image_url": imgURL63},
		},
	})
	if backfillRes.Status != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", backfillRes.Status, backfillRes.Body)
	}

	listRes := doRequest(t, srv, http.MethodGet, "/api/entries", nil)
	entries := decodeEntries(t, listRes.Body)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.Card.CollectorNumber == "63" && e.Card.ImageURL != imgURL63 {
			t.Fatalf("collector 63 image not updated, got %q", e.Card.ImageURL)
		}
		if e.Card.CollectorNumber == "64" && e.Card.ImageURL != "" {
			t.Fatalf("collector 64 image should be empty, got %q", e.Card.ImageURL)
		}
	}
}
