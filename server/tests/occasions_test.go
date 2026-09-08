package server_test

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/diligence-dev/looking-for-cards/server"
)

func decodeOccasions(t *testing.T, body []byte) []server.Occasion {
	t.Helper()
	var resp struct {
		Occasions []server.Occasion `json:"occasions"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to decode occasions response: %v", err)
	}
	if resp.Occasions == nil {
		return []server.Occasion{}
	}
	return resp.Occasions
}

func postOccasion(t *testing.T, srv *server.Server, user, name, dateOrRecurring string) apiResponse {
	t.Helper()
	return doRequest(t, srv, http.MethodPost, "/api/occasions?user="+user, map[string]any{
		"name": name, "date_or_recurring": dateOrRecurring,
	})
}

func occasionNames(occasions []server.Occasion) map[string]bool {
	m := map[string]bool{}
	for _, o := range occasions {
		m[o.Name] = true
	}
	return m
}

func TestOccasions_CreateAndList(t *testing.T) {
	srv := newTestServer(t)
	if res := postOccasion(t, srv, "alice", "friday club", "recurring"); res.Status != http.StatusCreated {
		t.Fatalf("create 1: expected 201, got %d: %s", res.Status, res.Body)
	}
	if res := postOccasion(t, srv, "alice", "christmas 2026", "2026-12-25"); res.Status != http.StatusCreated {
		t.Fatalf("create 2: expected 201, got %d: %s", res.Status, res.Body)
	}
	res := doRequest(t, srv, http.MethodGet, "/api/occasions?include_past=false", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("list: expected 200, got %d: %s", res.Status, res.Body)
	}
	got := occasionNames(decodeOccasions(t, res.Body))
	// hedwig seed + the two created
	for _, want := range []string{"hedwig", "friday club", "christmas 2026"} {
		if !got[want] {
			t.Errorf("expected occasion %q in list, got %v", want, got)
		}
	}
}

func TestOccasions_ListExcludesPast(t *testing.T) {
	srv := newTestServer(t)
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	postOccasion(t, srv, "alice", "weekly", "recurring")
	postOccasion(t, srv, "alice", "past event", yesterday)
	postOccasion(t, srv, "alice", "future event", tomorrow)

	res := doRequest(t, srv, http.MethodGet, "/api/occasions?include_past=false", nil)
	names := occasionNames(decodeOccasions(t, res.Body))
	if names["past event"] {
		t.Errorf("past event should be excluded with include_past=false")
	}
	if !names["weekly"] || !names["future event"] {
		t.Errorf("weekly + future should be included, got %v", names)
	}

	resAll := doRequest(t, srv, http.MethodGet, "/api/occasions?include_past=true", nil)
	namesAll := occasionNames(decodeOccasions(t, resAll.Body))
	for _, want := range []string{"weekly", "past event", "future event"} {
		if !namesAll[want] {
			t.Errorf("expected %q with include_past=true, got %v", want, namesAll)
		}
	}
}

func TestOccasions_PastFlagComputedCorrectly(t *testing.T) {
	srv := newTestServer(t)
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	postOccasion(t, srv, "alice", "weekly", "recurring")
	postOccasion(t, srv, "alice", "past event", yesterday)
	postOccasion(t, srv, "alice", "future event", tomorrow)

	res := doRequest(t, srv, http.MethodGet, "/api/occasions?include_past=true", nil)
	byName := map[string]server.Occasion{}
	for _, o := range decodeOccasions(t, res.Body) {
		byName[o.Name] = o
	}
	if byName["weekly"].Past {
		t.Errorf("recurring should have past=false")
	}
	if !byName["past event"].Past {
		t.Errorf("past date should have past=true")
	}
	if byName["future event"].Past {
		t.Errorf("future date should have past=false")
	}
}

func TestOccasions_CreateDuplicateNameReturns409(t *testing.T) {
	srv := newTestServer(t)
	if res := postOccasion(t, srv, "alice", "friday club", "recurring"); res.Status != http.StatusCreated {
		t.Fatalf("first create: expected 201, got %d", res.Status)
	}
	res := postOccasion(t, srv, "alice", "friday club", "recurring")
	if res.Status != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", res.Status, res.Body)
	}
}

func TestOccasions_CreateInvalidDateOrRecurringReturns400(t *testing.T) {
	srv := newTestServer(t)
	cases := []map[string]any{
		{"name": "bad date", "date_or_recurring": "someday"},
		{"name": "", "date_or_recurring": "recurring"},
		{"name": "no date", "date_or_recurring": ""},
		{"name": "short date", "date_or_recurring": "2026-1-1"},
	}
	for i, body := range cases {
		res := doRequest(t, srv, http.MethodPost, "/api/occasions?user=alice", body)
		if res.Status != http.StatusBadRequest {
			t.Errorf("case %d: expected 400, got %d: %s", i, res.Status, res.Body)
		}
	}
}

func TestOccasions_MissingUserReturns400(t *testing.T) {
	srv := newTestServer(t)
	res := doRequest(t, srv, http.MethodPost, "/api/occasions", map[string]any{
		"name": "friday club", "date_or_recurring": "recurring",
	})
	if res.Status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", res.Status, res.Body)
	}
}

func setMyOccasions(t *testing.T, srv *server.Server, user string, ids []int) apiResponse {
	t.Helper()
	return doRequest(t, srv, http.MethodPost, "/api/occasions/mine?user="+user, map[string]any{"occasion_ids": ids})
}

func occasionIDsByName(t *testing.T, srv *server.Server) map[string]int {
	t.Helper()
	db := srv.DB()
	rows, err := db.Query(`SELECT id, name FROM occasions`)
	if err != nil {
		t.Fatalf("query occasions: %v", err)
	}
	defer rows.Close()
	m := map[string]int{}
	for rows.Next() {
		var id int
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		m[name] = id
	}
	return m
}

func TestSeekerOccasions_SetAndList(t *testing.T) {
	srv := newTestServer(t)
	postOccasion(t, srv, "alice", "friday club", "recurring")
	postOccasion(t, srv, "alice", "sunday club", "recurring")
	ids := occasionIDsByName(t, srv)
	res := setMyOccasions(t, srv, "alice", []int{ids["friday club"], ids["sunday club"]})
	if res.Status != http.StatusNoContent {
		t.Fatalf("set: expected 204, got %d: %s", res.Status, res.Body)
	}
	got := doRequest(t, srv, http.MethodGet, "/api/occasions/mine?user=alice", nil)
	if got.Status != http.StatusOK {
		t.Fatalf("get mine: expected 200, got %d: %s", got.Status, got.Body)
	}
	names := occasionNames(decodeOccasions(t, got.Body))
	if !names["friday club"] || !names["sunday club"] {
		t.Errorf("expected both occasions, got %v", names)
	}
}

func TestSeekerOccasions_ReplaceAllSemantics(t *testing.T) {
	srv := newTestServer(t)
	postOccasion(t, srv, "alice", "one", "recurring")
	postOccasion(t, srv, "alice", "two", "recurring")
	postOccasion(t, srv, "alice", "three", "recurring")
	ids := occasionIDsByName(t, srv)
	if res := setMyOccasions(t, srv, "alice", []int{ids["one"], ids["two"]}); res.Status != http.StatusNoContent {
		t.Fatalf("set 1: expected 204, got %d", res.Status)
	}
	if res := setMyOccasions(t, srv, "alice", []int{ids["three"]}); res.Status != http.StatusNoContent {
		t.Fatalf("set 2: expected 204, got %d", res.Status)
	}
	got := doRequest(t, srv, http.MethodGet, "/api/occasions/mine?user=alice", nil)
	list := decodeOccasions(t, got.Body)
	if len(list) != 1 || list[0].Name != "three" {
		t.Fatalf("expected only [three], got %v", list)
	}
}

func TestSeekerOccasions_RejectsEmpty(t *testing.T) {
	srv := newTestServer(t)
	res := setMyOccasions(t, srv, "alice", []int{})
	if res.Status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", res.Status, res.Body)
	}
}

func TestSeekerOccasions_UnknownOccasionIDReturns400(t *testing.T) {
	srv := newTestServer(t)
	res := setMyOccasions(t, srv, "alice", []int{999999})
	if res.Status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", res.Status, res.Body)
	}
}

func TestSeekerOccasions_MissingUserReturns400(t *testing.T) {
	srv := newTestServer(t)
	if res := doRequest(t, srv, http.MethodGet, "/api/occasions/mine", nil); res.Status != http.StatusBadRequest {
		t.Fatalf("GET mine without user: expected 400, got %d", res.Status)
	}
	if res := doRequest(t, srv, http.MethodPost, "/api/occasions/mine", map[string]any{"occasion_ids": []int{1}}); res.Status != http.StatusBadRequest {
		t.Fatalf("POST mine without user: expected 400, got %d", res.Status)
	}
}

func TestMigrate_HedwigSeedsExistingSeekers(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := server.InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB #1: %v", err)
	}
	cardID, err := server.UpsertCard(db, "Bolt", "", "", "R", "Instant", 1, "")
	if err != nil {
		t.Fatalf("UpsertCard: %v", err)
	}
	for _, seeker := range []string{"alice", "bob"} {
		if _, err := server.AddEntry(db, cardID, seeker); err != nil {
			t.Fatalf("AddEntry %s: %v", seeker, err)
		}
	}
	// Wipe occasions + links to simulate a pre-occasions database.
	if _, err := db.Exec(`DELETE FROM seeker_occasions`); err != nil {
		t.Fatalf("clear links: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM occasions`); err != nil {
		t.Fatalf("clear occasions: %v", err)
	}
	db.Close()

	db2, err := server.InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB #2: %v", err)
	}
	defer db2.Close()

	var hedwigID int
	if err := db2.QueryRow(`SELECT id FROM occasions WHERE name='hedwig'`).Scan(&hedwigID); err != nil {
		t.Fatalf("hedwig occasion missing: %v", err)
	}
	for _, seeker := range []string{"alice", "bob"} {
		var n int
		if err := db2.QueryRow(`SELECT COUNT(*) FROM seeker_occasions WHERE seeker_name=? AND occasion_id=?`, seeker, hedwigID).Scan(&n); err != nil {
			t.Fatalf("link query %s: %v", seeker, err)
		}
		if n != 1 {
			t.Errorf("expected %s linked to hedwig, got count=%d", seeker, n)
		}
	}
}

func TestMigrate_HedwigIdempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := server.InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB #1: %v", err)
	}
	cardID, _ := server.UpsertCard(db, "Bolt", "", "", "R", "Instant", 1, "")
	server.AddEntry(db, cardID, "alice")
	db.Close()

	db2, err := server.InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB #2: %v", err)
	}
	db2.Close()
	db3, err := server.InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB #3: %v", err)
	}
	defer db3.Close()

	var occasions int
	if err := db3.QueryRow(`SELECT COUNT(*) FROM occasions WHERE name='hedwig'`).Scan(&occasions); err != nil {
		t.Fatalf("count occasions: %v", err)
	}
	if occasions != 1 {
		t.Errorf("expected 1 hedwig occasion, got %d", occasions)
	}
	var links int
	if err := db3.QueryRow(`SELECT COUNT(*) FROM seeker_occasions WHERE seeker_name='alice'`).Scan(&links); err != nil {
		t.Fatalf("count links: %v", err)
	}
	if links != 1 {
		t.Errorf("expected 1 link for alice, got %d", links)
	}
}

func TestListEntries_ExposesOccasionNames(t *testing.T) {
	srv := newTestServer(t)
	postOccasion(t, srv, "alice", "friday club", "recurring")
	postOccasion(t, srv, "alice", "sunday club", "recurring")
	ids := occasionIDsByName(t, srv)
	if res := setMyOccasions(t, srv, "alice", []int{ids["friday club"], ids["sunday club"]}); res.Status != http.StatusNoContent {
		t.Fatalf("set: expected 204, got %d", res.Status)
	}
	seedEntry(t, srv.DB(), "Bolt", "", "", "R", "Instant", "alice")

	res := doRequest(t, srv, http.MethodGet, "/api/entries", nil)
	entries := decodeEntries(t, res.Body)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	got := map[string]bool{}
	for _, o := range entries[0].Occasions {
		got[o] = true
	}
	if !got["friday club"] || !got["sunday club"] {
		t.Errorf("expected both occasion names, got %v", entries[0].Occasions)
	}
}

func TestListEntries_OccasionsEmptyWhenSeekerHasNone(t *testing.T) {
	srv := newTestServer(t)
	seedEntry(t, srv.DB(), "Bolt", "", "", "R", "Instant", "newcomer")
	res := doRequest(t, srv, http.MethodGet, "/api/entries", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Status)
	}
	// occasions must encode as [] not null.
	var raw struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal(res.Body, &raw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	if len(raw.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(raw.Entries))
	}
	occ, ok := raw.Entries[0]["occasions"]
	if !ok {
		t.Fatalf("entry missing occasions field: %v", raw.Entries[0])
	}
	arr, ok := occ.([]any)
	if !ok {
		t.Fatalf("expected occasions to be an array, got %T (%v)", occ, occ)
	}
	if len(arr) != 0 {
		t.Fatalf("expected empty occasions, got %v", arr)
	}
}

func TestOccasionIsolation_BDoesNotSeeAWhenDisjoint(t *testing.T) {
	srv := newTestServer(t)

	// Create two occasions X and Y
	if res := postOccasion(t, srv, "admin", "occasionX", "recurring"); res.Status != http.StatusCreated {
		t.Fatalf("create X: expected 201, got %d: %s", res.Status, res.Body)
	}
	if res := postOccasion(t, srv, "admin", "occasionY", "recurring"); res.Status != http.StatusCreated {
		t.Fatalf("create Y: expected 201, got %d: %s", res.Status, res.Body)
	}
	ids := occasionIDsByName(t, srv)
	idX := ids["occasionX"]
	idY := ids["occasionY"]

	// A goes to X, B goes to Y
	if res := setMyOccasions(t, srv, "A", []int{idX}); res.Status != http.StatusNoContent {
		t.Fatalf("set A->X: expected 204, got %d: %s", res.Status, res.Body)
	}
	if res := setMyOccasions(t, srv, "B", []int{idY}); res.Status != http.StatusNoContent {
		t.Fatalf("set B->Y: expected 204, got %d: %s", res.Status, res.Body)
	}

	// A adds a card
	cards := []map[string]any{
		{"name": "Lightning Bolt", "set": "", "colors": "R", "type_line": "Instant"},
	}
	res := postCards(t, srv, "A", cards)
	if res.Status != http.StatusCreated {
		t.Fatalf("A add card: expected 201, got %d: %s", res.Status, res.Body)
	}

	// B fetches entries - should see 0 because A and B share no occasion
	bRes := doRequest(t, srv, http.MethodGet, "/api/entries?user=B", nil)
	if bRes.Status != http.StatusOK {
		t.Fatalf("B list: expected 200, got %d: %s", bRes.Status, bRes.Body)
	}
	var bResp struct {
		Entries []map[string]any `json:"entries"`
		Total   int              `json:"total"`
	}
	if err := json.Unmarshal(bRes.Body, &bResp); err != nil {
		t.Fatalf("decode B: %v", err)
	}
	if bResp.Total != 0 {
		t.Fatalf("BUG: B (occasionY) sees %d entries from A (occasionX) - expected 0, got %v", bResp.Total, bResp.Entries)
	}

	// A should still see own card (shares occasion with self)
	aRes := doRequest(t, srv, http.MethodGet, "/api/entries?user=A", nil)
	if aRes.Status != http.StatusOK {
		t.Fatalf("A list: expected 200, got %d: %s", aRes.Status, aRes.Body)
	}
	var aResp struct {
		Entries []map[string]any `json:"entries"`
		Total   int              `json:"total"`
	}
	if err := json.Unmarshal(aRes.Body, &aResp); err != nil {
		t.Fatalf("decode A: %v", err)
	}
	if aResp.Total != 1 {
		t.Fatalf("A should see own card, expected 1 got %d", aResp.Total)
	}

	// User with overlapping occasion should see it - C goes to X too
	if res := setMyOccasions(t, srv, "C", []int{idX}); res.Status != http.StatusNoContent {
		t.Fatalf("set C->X: %d %s", res.Status, res.Body)
	}
	cRes := doRequest(t, srv, http.MethodGet, "/api/entries?user=C", nil)
	if cRes.Status != http.StatusOK {
		t.Fatalf("C list: %d %s", cRes.Status, cRes.Body)
	}
	var cResp struct {
		Entries []map[string]any `json:"entries"`
		Total   int              `json:"total"`
	}
	json.Unmarshal(cRes.Body, &cResp)
	if cResp.Total != 1 {
		t.Fatalf("C (same occasion X as A) should see 1 entry, got %d", cResp.Total)
	}

	// Anonymous (no user param) should still see all? Or at least not filtered empty - we expect 1 for backward compat
	anonRes := doRequest(t, srv, http.MethodGet, "/api/entries", nil)
	var anonResp struct {
		Entries []map[string]any `json:"entries"`
		Total   int              `json:"total"`
	}
	json.Unmarshal(anonRes.Body, &anonResp)
	if anonResp.Total != 1 {
		t.Fatalf("anonymous should see 1 (no filtering), got %d", anonResp.Total)
	}
}

func TestOccasionIsolation_SharedOccasionVisible(t *testing.T) {
	srv := newTestServer(t)
	postOccasion(t, srv, "admin", "occasionX", "recurring")
	postOccasion(t, srv, "admin", "occasionY", "recurring")
	ids := occasionIDsByName(t, srv)
	// A goes to X+Y, B goes to Y -> overlap on Y so B should see A's card
	setMyOccasions(t, srv, "A", []int{ids["occasionX"], ids["occasionY"]})
	setMyOccasions(t, srv, "B", []int{ids["occasionY"]})
	postCards(t, srv, "A", []map[string]any{{"name": "Bolt", "colors": "R", "type_line": "Instant"}})
	bRes := doRequest(t, srv, http.MethodGet, "/api/entries?user=B", nil)
	var resp struct {
		Total int `json:"total"`
	}
	json.Unmarshal(bRes.Body, &resp)
	if resp.Total != 1 {
		t.Fatalf("B shares Y with A (X+Y), expected 1 got %d", resp.Total)
	}
}

func TestOccasionIsolation_ViewerWithNoOccasionsSeesAll(t *testing.T) {
	srv := newTestServer(t)
	postOccasion(t, srv, "admin", "occasionX", "recurring")
	ids := occasionIDsByName(t, srv)
	setMyOccasions(t, srv, "A", []int{ids["occasionX"]})
	postCards(t, srv, "A", []map[string]any{{"name": "Bolt", "colors": "R", "type_line": "Instant"}})
	// B has no occasions set - should see all (or at least not be filtered away)
	bRes := doRequest(t, srv, http.MethodGet, "/api/entries?user=B", nil)
	var resp struct {
		Total int `json:"total"`
	}
	json.Unmarshal(bRes.Body, &resp)
	if resp.Total != 1 {
		t.Fatalf("viewer with no occasions should see all, got %d", resp.Total)
	}
}

func TestOccasionIsolation_EntryWithNoOccasionsAlwaysVisible(t *testing.T) {
	srv := newTestServer(t)
	postOccasion(t, srv, "admin", "occasionY", "recurring")
	ids := occasionIDsByName(t, srv)
	setMyOccasions(t, srv, "B", []int{ids["occasionY"]})
	// A has no occasions (create entry directly via DB helper without setting)
	seedEntry(t, srv.DB(), "Bolt", "", "", "R", "Instant", "A")
	bRes := doRequest(t, srv, http.MethodGet, "/api/entries?user=B", nil)
	var resp struct {
		Total int `json:"total"`
	}
	json.Unmarshal(bRes.Body, &resp)
	if resp.Total != 1 {
		t.Fatalf("entry with no occasions should be visible to anyone, got %d", resp.Total)
	}
}
