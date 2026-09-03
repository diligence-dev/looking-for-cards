package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"time"
)

type Occasion struct {
	ID              int    `json:"id"`
	Name            string `json:"name"`
	DateOrRecurring string `json:"date_or_recurring"`
	Past            bool   `json:"past"`
}

var ErrOccasionExists = errors.New("occasion already exists")
var ErrUnknownOccasion = errors.New("unknown occasion")

var datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func IsPast(dateOrRecurring string) bool {
	if dateOrRecurring == "recurring" {
		return false
	}
	return dateOrRecurring < time.Now().Format("2006-01-02")
}

func validDateOrRecurring(s string) bool {
	if s == "recurring" {
		return true
	}
	if !datePattern.MatchString(s) {
		return false
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

func ListOccasions(db *sql.DB, includePast bool) ([]Occasion, error) {
	rows, err := db.Query(`SELECT id, name, date_or_recurring FROM occasions ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	occasions := []Occasion{}
	for rows.Next() {
		var o Occasion
		if err := rows.Scan(&o.ID, &o.Name, &o.DateOrRecurring); err != nil {
			return nil, err
		}
		o.Past = IsPast(o.DateOrRecurring)
		if o.Past && !includePast {
			continue
		}
		occasions = append(occasions, o)
	}
	return occasions, rows.Err()
}

func UpsertOccasion(db *sql.DB, name, dateOrRecurring string) (Occasion, error) {
	if name == "" || !validDateOrRecurring(dateOrRecurring) {
		return Occasion{}, errors.New("invalid occasion")
	}
	_, err := db.Exec(`INSERT INTO occasions (name, date_or_recurring) VALUES (?, ?)`, name, dateOrRecurring)
	if err != nil {
		var existing int
		if qerr := db.QueryRow(`SELECT COUNT(*) FROM occasions WHERE name=?`, name).Scan(&existing); qerr == nil && existing > 0 {
			return Occasion{}, ErrOccasionExists
		}
		return Occasion{}, err
	}
	return GetOccasion(db, name)
}

func GetOccasion(db *sql.DB, name string) (Occasion, error) {
	var o Occasion
	err := db.QueryRow(`SELECT id, name, date_or_recurring FROM occasions WHERE name=?`, name).Scan(&o.ID, &o.Name, &o.DateOrRecurring)
	if err != nil {
		return Occasion{}, err
	}
	o.Past = IsPast(o.DateOrRecurring)
	return o, nil
}

func ListSeekerOccasions(db *sql.DB, seeker string) ([]Occasion, error) {
	rows, err := db.Query(`
		SELECT o.id, o.name, o.date_or_recurring
		FROM seeker_occasions so JOIN occasions o ON so.occasion_id = o.id
		WHERE so.seeker_name = ?
		ORDER BY o.name
	`, seeker)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	occasions := []Occasion{}
	for rows.Next() {
		var o Occasion
		if err := rows.Scan(&o.ID, &o.Name, &o.DateOrRecurring); err != nil {
			return nil, err
		}
		o.Past = IsPast(o.DateOrRecurring)
		occasions = append(occasions, o)
	}
	return occasions, rows.Err()
}

func SetSeekerOccasions(db *sql.DB, seeker string, occasionIDs []int) error {
	seen := map[int]bool{}
	unique := []int{}
	for _, id := range occasionIDs {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	for _, id := range unique {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM occasions WHERE id=?`, id).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrUnknownOccasion
		}
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM seeker_occasions WHERE seeker_name=?`, seeker); err != nil {
		tx.Rollback()
		return err
	}
	for _, id := range unique {
		if _, err := tx.Exec(`INSERT INTO seeker_occasions (seeker_name, occasion_id) VALUES (?, ?)`, seeker, id); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func SeekerOccasionNames(db *sql.DB, seekers []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(seekers) == 0 {
		return out, nil
	}
	args := make([]any, len(seekers))
	placeholders := ""
	for i, s := range seekers {
		args[i] = s
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
	}
	rows, err := db.Query(`
		SELECT so.seeker_name, o.name
		FROM seeker_occasions so JOIN occasions o ON so.occasion_id = o.id
		WHERE so.seeker_name IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var seeker, name string
		if err := rows.Scan(&seeker, &name); err != nil {
			return nil, err
		}
		out[seeker] = append(out[seeker], name)
	}
	return out, rows.Err()
}

func enrichEntries(db *sql.DB, entries []Entry) error {
	seen := map[string]bool{}
	seekers := []string{}
	for _, e := range entries {
		if !seen[e.SeekerName] {
			seen[e.SeekerName] = true
			seekers = append(seekers, e.SeekerName)
		}
	}
	names, err := SeekerOccasionNames(db, seekers)
	if err != nil {
		return err
	}
	for i := range entries {
		entries[i].Occasions = names[entries[i].SeekerName]
		if entries[i].Occasions == nil {
			entries[i].Occasions = []string{}
		}
	}
	return nil
}

func (s *Server) handleOccasions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		includePast := r.URL.Query().Get("include_past") == "true"
		occasions, err := ListOccasions(s.db, includePast)
		if err != nil {
			log.Printf("ListOccasions failed: %v", err)
			sendJSONError(w, "failed to fetch occasions", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"occasions": occasions})
	case http.MethodPost:
		user := r.URL.Query().Get("user")
		if user == "" {
			sendJSONError(w, "user is required", http.StatusBadRequest)
			return
		}
		var req struct {
			Name            string `json:"name"`
			DateOrRecurring string `json:"date_or_recurring"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			sendJSONError(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		if req.Name == "" || !validDateOrRecurring(req.DateOrRecurring) {
			sendJSONError(w, "name and date_or_recurring (recurring or YYYY-MM-DD) are required", http.StatusBadRequest)
			return
		}
		occasion, err := UpsertOccasion(s.db, req.Name, req.DateOrRecurring)
		if err != nil {
			if errors.Is(err, ErrOccasionExists) {
				sendJSONError(w, "occasion already exists", http.StatusConflict)
				return
			}
			log.Printf("UpsertOccasion failed: %v", err)
			sendJSONError(w, "failed to create occasion", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(occasion)
	default:
		sendJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleMyOccasions(w http.ResponseWriter, r *http.Request) {
	user := r.URL.Query().Get("user")
	if user == "" {
		sendJSONError(w, "user is required", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		occasions, err := ListSeekerOccasions(s.db, user)
		if err != nil {
			log.Printf("ListSeekerOccasions failed: %v", err)
			sendJSONError(w, "failed to fetch occasions", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"occasions": occasions})
	case http.MethodPost:
		var req struct {
			OccasionIDs []int `json:"occasion_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			sendJSONError(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		if len(req.OccasionIDs) == 0 {
			sendJSONError(w, "select at least one occasion", http.StatusBadRequest)
			return
		}
		if err := SetSeekerOccasions(s.db, user, req.OccasionIDs); err != nil {
			if errors.Is(err, ErrUnknownOccasion) {
				sendJSONError(w, "unknown occasion", http.StatusBadRequest)
				return
			}
			log.Printf("SetSeekerOccasions failed: %v", err)
			sendJSONError(w, "failed to save occasions", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		sendJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
