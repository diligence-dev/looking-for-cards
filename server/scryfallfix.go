package server

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// This file is a one-time database fix: the live DB had wrong mana_value for
// every card. On the next deploy, migrateFixManaValues re-fetches the correct
// cmc from Scryfall for every card and overwrites it, then records a marker
// in schema_meta so it never runs again. Remove this file (and the
// migrateFixManaValues call in db.go) after the fix has run in production.

// ScryfallIdentifier is one lookup key for the /cards/collection endpoint.
type ScryfallIdentifier struct {
	Name            string `json:"name,omitempty"`
	Set             string `json:"set,omitempty"`
	CollectorNumber string `json:"collector_number,omitempty"`
}

// ScryfallCard is the subset of a Scryfall card object we need.
type ScryfallCard struct {
	Name string  `json:"name"`
	Set  string  `json:"set"`
	Cmc  float64 `json:"cmc"`
}

// ScryfallCollectionResponse mirrors Scryfall's /cards/collection reply:
// data holds the found cards (in request order, excluding not_found) and
// not_found echoes the identifiers that did not resolve.
type ScryfallCollectionResponse struct {
	Data     []ScryfallCard       `json:"data"`
	NotFound []ScryfallIdentifier `json:"not_found"`
}

// ScryfallFetchCollection is the indirection point for tests. The default
// points at the real Scryfall endpoint; tests swap it with a stub.
var ScryfallFetchCollection = realScryfallFetchCollection

func realScryfallFetchCollection(identifiers []ScryfallIdentifier) (*ScryfallCollectionResponse, error) {
	body, _ := json.Marshal(map[string]any{"identifiers": identifiers})
	client := &http.Client{Timeout: 30 * time.Second}
	const maxAttempts = 3
	var resp *http.Response
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequest(http.MethodPost, "https://api.scryfall.com/cards/collection", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err = client.Do(req)
		if err != nil {
			if attempt >= maxAttempts-1 {
				return nil, err
			}
			time.Sleep(time.Second)
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxAttempts-1 {
			resp.Body.Close()
			retry := 1
			if n, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && n > 0 {
				retry = n
			}
			time.Sleep(time.Duration(retry) * time.Second)
			continue
		}
		break
	}
	defer resp.Body.Close()
	var out ScryfallCollectionResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// frontFaceName strips the back face of split/transform names ("Status // Statue"
// -> "Status") so /cards/collection, which rejects "//" names, can find them.
func frontFaceName(name string) string {
	return strings.TrimSpace(strings.Split(name, "//")[0])
}

func scryfallIDKey(id ScryfallIdentifier) string {
	return strings.ToLower(id.Name) + "\x00" + strings.ToLower(id.Set) + "\x00" + strings.ToLower(id.CollectorNumber)
}

// migrateFixManaValues re-fetches mana_value for every card from Scryfall and
// overwrites the stored (wrong) value. Idempotent via the schema_meta marker:
// if it already ran, it returns immediately. On any batch failure it returns
// without setting the marker so the next machine start retries the whole fix.
func migrateFixManaValues(db *sql.DB) {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_meta(key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		log.Printf("migrateFixManaValues: create schema_meta: %v", err)
		return
	}
	var done int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_meta WHERE key='mana_value_fix_v1'`).Scan(&done); err != nil {
		log.Printf("migrateFixManaValues: read marker: %v", err)
		return
	}
	if done > 0 {
		return
	}

	rows, err := db.Query(`SELECT id, name, set_code, collector_number FROM cards`)
	if err != nil {
		log.Printf("migrateFixManaValues: query cards: %v", err)
		return
	}
	type cardRef struct {
		id              int
		name            string
		set             string
		collectorNumber string
	}
	var cards []cardRef
	for rows.Next() {
		var c cardRef
		if err := rows.Scan(&c.id, &c.name, &c.set, &c.collectorNumber); err != nil {
			rows.Close()
			log.Printf("migrateFixManaValues: scan: %v", err)
			return
		}
		cards = append(cards, c)
	}
	rows.Close()

	const batchSize = 75
	for start := 0; start < len(cards); start += batchSize {
		end := start + batchSize
		if end > len(cards) {
			end = len(cards)
		}
		batch := cards[start:end]

		identifiers := make([]ScryfallIdentifier, len(batch))
		for i, c := range batch {
			if c.set != "" && c.collectorNumber != "" {
				identifiers[i] = ScryfallIdentifier{Set: c.set, CollectorNumber: c.collectorNumber}
			} else {
				identifiers[i] = ScryfallIdentifier{Name: frontFaceName(c.name), Set: c.set}
			}
		}

		resp, err := ScryfallFetchCollection(identifiers)
		if err != nil {
			log.Printf("migrateFixManaValues: fetch batch starting at %d: %v (will retry on next start)", start, err)
			return
		}

		notFound := map[string]bool{}
		for _, nf := range resp.NotFound {
			notFound[scryfallIDKey(nf)] = true
		}

		tx, err := db.Begin()
		if err != nil {
			log.Printf("migrateFixManaValues: begin tx: %v (will retry on next start)", err)
			return
		}
		cardIdx := 0
		for i, id := range identifiers {
			if notFound[scryfallIDKey(id)] {
				continue
			}
			if cardIdx >= len(resp.Data) {
				break
			}
			card := resp.Data[cardIdx]
			cardIdx++
			if id.Set != "" && card.Set != "" && !strings.EqualFold(id.Set, card.Set) {
				continue
			}
			if _, err := tx.Exec(`UPDATE cards SET mana_value=? WHERE id=?`, card.Cmc, batch[i].id); err != nil {
				tx.Rollback()
				log.Printf("migrateFixManaValues: update id=%d: %v (will retry on next start)", batch[i].id, err)
				return
			}
		}
		if err := tx.Commit(); err != nil {
			log.Printf("migrateFixManaValues: commit: %v (will retry on next start)", err)
			return
		}

		if end < len(cards) {
			time.Sleep(500 * time.Millisecond)
		}
	}

	if _, err := db.Exec(`INSERT INTO schema_meta(key, value) VALUES('mana_value_fix_v1', 'done')`); err != nil {
		log.Printf("migrateFixManaValues: set marker: %v", err)
	}
}
