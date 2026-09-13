package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
)

type errorResponse struct {
	Error string `json:"error"`
}

func sendJSONError(w http.ResponseWriter, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(errorResponse{Error: message})
}

type addCard struct {
	Name            string  `json:"name"`
	Set             string  `json:"set"`
	CollectorNumber string  `json:"collector_number"`
	Colors          string  `json:"colors"`
	TypeLine        string  `json:"type_line"`
	ManaValue       float64 `json:"mana_value"`
	ImageURL        string  `json:"image_url"`
	Note            string  `json:"note"`
}

type addRequest struct {
	Cards []addCard `json:"cards"`
}

type addError struct {
	Line  int    `json:"line"`
	Name  string `json:"name"`
	Error string `json:"error"`
}

type addResponse struct {
	Created int        `json:"created"`
	Errors  []addError `json:"errors"`
}

type takenResponse struct {
	Error string `json:"error"`
	Entry Entry   `json:"entry"`
}

func (s *Server) handleEntries(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listEntries(w, r)
	case http.MethodPost:
		s.addEntries(w, r)
	default:
		sendJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) listEntries(w http.ResponseWriter, r *http.Request) {
	entries, err := ListEntries(s.db)
	if err != nil {
		log.Printf("ListEntries failed: %v", err)
		sendJSONError(w, "failed to fetch entries", http.StatusInternalServerError)
		return
	}
	viewer := r.URL.Query().Get("user")
	if viewer != "" {
		viewerOccs, err := ListSeekerOccasions(s.db, viewer)
		if err != nil {
			log.Printf("ListSeekerOccasions failed for %q: %v", viewer, err)
		} else if len(viewerOccs) > 0 {
			viewerSet := map[string]bool{}
			for _, o := range viewerOccs {
				viewerSet[o.Name] = true
			}
			filtered := []Entry{}
			for _, e := range entries {
				if len(e.Occasions) == 0 {
					filtered = append(filtered, e)
					continue
				}
				intersects := false
				for _, n := range e.Occasions {
					if viewerSet[n] {
						intersects = true
						break
					}
				}
				if intersects {
					filtered = append(filtered, e)
				}
			}
			entries = filtered
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"entries": entries,
		"total":   len(entries),
	})
}

func (s *Server) addEntries(w http.ResponseWriter, r *http.Request) {
	user := r.URL.Query().Get("user")
	if user == "" {
		sendJSONError(w, "user is required", http.StatusBadRequest)
		return
	}

	var req addRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	errs := []addError{}
	created := 0
	for i, c := range req.Cards {
		if c.Name == "" {
			errs = append(errs, addError{Line: i, Name: "", Error: "empty name"})
			continue
		}
		cardID, err := UpsertCard(s.db, c.Name, c.Set, c.CollectorNumber, c.Colors, c.TypeLine, c.ManaValue, c.ImageURL)
		if err != nil {
			log.Printf("UpsertCard failed for %q: %v", c.Name, err)
			errs = append(errs, addError{Line: i, Name: c.Name, Error: "database error"})
			continue
		}
		if _, err := AddEntryWithNote(s.db, cardID, user, c.Note); err != nil {
			log.Printf("AddEntry failed for %q: %v", c.Name, err)
			errs = append(errs, addError{Line: i, Name: c.Name, Error: "database error"})
			continue
		}
		created++
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(addResponse{Created: created, Errors: errs})
}

func (s *Server) handleSetGiver(w http.ResponseWriter, r *http.Request) {
	user := r.URL.Query().Get("user")
	if user == "" {
		sendJSONError(w, "user is required", http.StatusBadRequest)
		return
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		sendJSONError(w, "invalid id", http.StatusNotFound)
		return
	}

	entry, taken, err := SetGiver(s.db, id, user)
	if err != nil {
		if errors.Is(err, ErrEntryNotFound) {
			sendJSONError(w, "entry not found", http.StatusNotFound)
			return
		}
		log.Printf("SetGiver failed for id=%d: %v", id, err)
		sendJSONError(w, "failed to set giver", http.StatusInternalServerError)
		return
	}
	if taken {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(takenResponse{Error: "taken", Entry: entry})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entry)
}

func (s *Server) handleClearGiver(w http.ResponseWriter, r *http.Request) {
	user := r.URL.Query().Get("user")
	if user == "" {
		sendJSONError(w, "user is required", http.StatusBadRequest)
		return
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		sendJSONError(w, "invalid id", http.StatusNotFound)
		return
	}

	ok, err := ClearGiver(s.db, id, user)
	if err != nil {
		log.Printf("ClearGiver failed for id=%d: %v", id, err)
		sendJSONError(w, "failed to clear giver", http.StatusInternalServerError)
		return
	}
	if !ok {
		sendJSONError(w, "entry not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRemoveEntry(w http.ResponseWriter, r *http.Request) {
	user := r.URL.Query().Get("user")
	if user == "" {
		sendJSONError(w, "user is required", http.StatusBadRequest)
		return
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		sendJSONError(w, "invalid id", http.StatusNotFound)
		return
	}

	ok, err := RemoveEntry(s.db, id, user)
	if err != nil {
		log.Printf("RemoveEntry failed for id=%d: %v", id, err)
		sendJSONError(w, "failed to remove entry", http.StatusInternalServerError)
		return
	}
	if ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	exists, err := EntryExists(s.db, id)
	if err != nil {
		log.Printf("EntryExists failed for id=%d: %v", id, err)
		sendJSONError(w, "failed to remove entry", http.StatusInternalServerError)
		return
	}
	if exists {
		sendJSONError(w, "you can only remove your own entries", http.StatusForbidden)
		return
	}
	sendJSONError(w, "entry not found", http.StatusNotFound)
}

func (s *Server) handleCardMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Cards []Card `json:"cards"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if err := UpdateCardMetadata(s.db, req.Cards); err != nil {
		log.Printf("UpdateCardMetadata failed: %v", err)
		sendJSONError(w, "failed to update card metadata", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
