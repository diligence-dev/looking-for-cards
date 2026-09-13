package server_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/diligence-dev/looking-for-cards/server"
)

var sharedDB *sql.DB

func TestMain(m *testing.M) {
	db, err := server.InitTestDB()
	if err != nil {
		os.Exit(1)
	}
	sharedDB = db
	code := m.Run()
	db.Close()
	os.Exit(code)
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	if err := server.ResetTestDB(sharedDB); err != nil {
		t.Fatalf("failed to reset test database: %v", err)
	}
	return sharedDB
}

func newTestServer(t *testing.T) *server.Server {
	t.Helper()
	db := openTestDB(t)
	return server.NewServer(db, nil)
}

type apiResponse struct {
	Status int
	Body   []byte
}

func doRequest(t *testing.T, srv *server.Server, method, target string, body any) apiResponse {
	t.Helper()
	var r *http.Request
	if body != nil {
		buf, _ := json.Marshal(body)
		r = httptest.NewRequest(method, target, bytes.NewReader(buf))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	return apiResponse{Status: w.Code, Body: w.Body.Bytes()}
}

func doRequestRaw(t *testing.T, srv *server.Server, method, target string, body []byte) apiResponse {
	t.Helper()
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, target, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	return apiResponse{Status: w.Code, Body: w.Body.Bytes()}
}

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("countRows failed: %v", err)
	}
	return n
}

func decodeEntries(t *testing.T, body []byte) []server.Entry {
	t.Helper()
	var resp struct {
		Entries []server.Entry `json:"entries"`
		Total   int            `json:"total"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to decode entries response: %v", err)
	}
	return resp.Entries
}

func entryNames(entries []server.Entry) []string {
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Card.Name
	}
	return names
}
