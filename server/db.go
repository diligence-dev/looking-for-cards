package server

import (
	"database/sql"
	"errors"

	_ "github.com/mattn/go-sqlite3"
)

type Card struct {
	ID              int      `json:"-"`
	Name            string   `json:"name"`
	SetCode         string   `json:"set"`
	CollectorNumber string   `json:"collector_number"`
	Colors          string   `json:"colors"`
	TypeLine        string   `json:"type_line"`
	ManaValue       *float64 `json:"mana_value"`
	ImageURL        string   `json:"image_url"`
	ColorSortKey    int      `json:"-"`
	TypeSortKey     int      `json:"-"`
}

type Entry struct {
	ID         int    `json:"id"`
	Card       Card   `json:"card"`
	SeekerName string `json:"seeker"`
	GiverName  string `json:"giver"`
	CreatedAt  string `json:"created_at"`
}

var ErrEntryNotFound = errors.New("entry not found")

func InitDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", path+"?_busy_timeout=5000")
	if err != nil {
		return nil, err
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS cards (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			name            TEXT NOT NULL,
			set_code        TEXT NOT NULL DEFAULT '',
			collector_number TEXT NOT NULL DEFAULT '',
			colors          TEXT NOT NULL DEFAULT '',
			type_line       TEXT NOT NULL DEFAULT '',
			mana_value      REAL,
			image_url       TEXT NOT NULL DEFAULT '',
			color_sort_key  INTEGER NOT NULL DEFAULT 5,
			type_sort_key   INTEGER NOT NULL DEFAULT 8,
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(name, set_code, collector_number)
		);
		CREATE INDEX IF NOT EXISTS idx_cards_sort ON cards(color_sort_key, type_sort_key, mana_value, name);

		CREATE TABLE IF NOT EXISTS entries (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			card_id     INTEGER NOT NULL REFERENCES cards(id),
			seeker_name TEXT NOT NULL,
			giver_name  TEXT,
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_entries_seeker ON entries(seeker_name);
		CREATE INDEX IF NOT EXISTS idx_entries_giver  ON entries(giver_name);
		CREATE INDEX IF NOT EXISTS idx_entries_card   ON entries(card_id);
	`)
	if err != nil {
		return nil, err
	}

	migrateAddImageURL(db)
	migrateAddCollectorNumber(db)
	migrateAddManaValue(db)
	migrateManaValueNullable(db)

	return db, nil
}

func migrateAddImageURL(db *sql.DB) {
	row := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('cards') WHERE name='image_url'")
	var count int
	if err := row.Scan(&count); err != nil || count > 0 {
		return
	}
	db.Exec(`ALTER TABLE cards ADD COLUMN image_url TEXT NOT NULL DEFAULT ''`)
}

func migrateAddCollectorNumber(db *sql.DB) {
	row := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('cards') WHERE name='collector_number'")
	var count int
	if err := row.Scan(&count); err != nil || count > 0 {
		return
	}
	db.Exec(`
		BEGIN;
		CREATE TABLE cards_new (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			name            TEXT NOT NULL,
			set_code        TEXT NOT NULL DEFAULT '',
			collector_number TEXT NOT NULL DEFAULT '',
			colors          TEXT NOT NULL DEFAULT '',
			type_line       TEXT NOT NULL DEFAULT '',
			mana_value      REAL,
			image_url       TEXT NOT NULL DEFAULT '',
			color_sort_key  INTEGER NOT NULL DEFAULT 5,
			type_sort_key   INTEGER NOT NULL DEFAULT 8,
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(name, set_code, collector_number)
		);
		INSERT INTO cards_new (id, name, set_code, collector_number, colors, type_line, mana_value, image_url, color_sort_key, type_sort_key, created_at)
		SELECT id, name, set_code, '', colors, type_line, NULL, image_url, color_sort_key, type_sort_key, created_at FROM cards;
		DROP TABLE cards;
		ALTER TABLE cards_new RENAME TO cards;
		CREATE INDEX idx_cards_sort ON cards(color_sort_key, type_sort_key, mana_value, name);
		COMMIT;
	`)
}

func migrateAddManaValue(db *sql.DB) {
	row := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('cards') WHERE name='mana_value'")
	var count int
	if err := row.Scan(&count); err != nil || count > 0 {
		return
	}
	db.Exec(`ALTER TABLE cards ADD COLUMN mana_value REAL`)
}

// migrateManaValueNullable converts a legacy NOT NULL DEFAULT 0 mana_value
// column (from the first iteration of this feature) into a nullable one, and
// rewrites existing 0s to NULL so the frontend backfills the real value from
// Scryfall. Real 0-cmc cards added through the add flow are untouched (they
// carry a non-zero path only if they had a real value; a true 0 re-backfills
// harmlessly to 0).
func migrateManaValueNullable(db *sql.DB) {
	row := db.QueryRow(`SELECT "notnull" FROM pragma_table_info('cards') WHERE name='mana_value'`)
	var notnull int
	if err := row.Scan(&notnull); err != nil || notnull == 0 {
		return
	}
	db.Exec(`
		BEGIN;
		CREATE TABLE cards_mv (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			name            TEXT NOT NULL,
			set_code        TEXT NOT NULL DEFAULT '',
			collector_number TEXT NOT NULL DEFAULT '',
			colors          TEXT NOT NULL DEFAULT '',
			type_line       TEXT NOT NULL DEFAULT '',
			mana_value      REAL,
			image_url       TEXT NOT NULL DEFAULT '',
			color_sort_key  INTEGER NOT NULL DEFAULT 5,
			type_sort_key   INTEGER NOT NULL DEFAULT 8,
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(name, set_code, collector_number)
		);
		INSERT INTO cards_mv (id, name, set_code, collector_number, colors, type_line, mana_value, image_url, color_sort_key, type_sort_key, created_at)
		SELECT id, name, set_code, collector_number, colors, type_line, NULLIF(mana_value, 0), image_url, color_sort_key, type_sort_key, created_at FROM cards;
		DROP TABLE cards;
		ALTER TABLE cards_mv RENAME TO cards;
		CREATE INDEX idx_cards_sort ON cards(color_sort_key, type_sort_key, mana_value, name);
		COMMIT;
	`)
}

func UpsertCard(db *sql.DB, name, setCode, collectorNumber, colors, typeLine string, manaValue float64, imageURL string) (int, error) {
	colorKey := ColorSortKey(colors)
	typeKey := TypeSortKey(typeLine)
	_, err := db.Exec(`
		INSERT INTO cards (name, set_code, collector_number, colors, type_line, mana_value, image_url, color_sort_key, type_sort_key)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name, set_code, collector_number) DO NOTHING
	`, name, setCode, collectorNumber, colors, typeLine, manaValue, imageURL, colorKey, typeKey)
	if err != nil {
		return 0, err
	}
	var id int
	err = db.QueryRow(`SELECT id FROM cards WHERE name=? AND set_code=? AND collector_number=?`, name, setCode, collectorNumber).Scan(&id)
	return id, err
}

func UpdateCardMetadata(db *sql.DB, cards []Card) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	for _, c := range cards {
		if _, err := tx.Exec(`UPDATE cards SET image_url=?, mana_value=? WHERE name=? AND set_code=? AND collector_number=?`, c.ImageURL, c.ManaValue, c.Name, c.SetCode, c.CollectorNumber); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func AddEntry(db *sql.DB, cardID int, seeker string) (int64, error) {
	res, err := db.Exec(`INSERT INTO entries (card_id, seeker_name) VALUES (?, ?)`, cardID, seeker)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

const entrySelectCols = `
	entries.id, entries.seeker_name, entries.giver_name, entries.created_at,
	cards.id, cards.name, cards.set_code, cards.collector_number, cards.colors, cards.type_line,
	cards.mana_value, cards.image_url, cards.color_sort_key, cards.type_sort_key
`

func scanEntry(scanner interface{ Scan(...interface{}) error }, e *Entry) error {
	var giver sql.NullString
	var mv sql.NullFloat64
	err := scanner.Scan(
		&e.ID, &e.SeekerName, &giver, &e.CreatedAt,
		&e.Card.ID, &e.Card.Name, &e.Card.SetCode, &e.Card.CollectorNumber, &e.Card.Colors, &e.Card.TypeLine,
		&mv, &e.Card.ImageURL, &e.Card.ColorSortKey, &e.Card.TypeSortKey,
	)
	if err != nil {
		return err
	}
	e.GiverName = giver.String
	if mv.Valid {
		v := mv.Float64
		e.Card.ManaValue = &v
	}
	return nil
}

func getEntry(db *sql.DB, entryID int) (Entry, error) {
	row := db.QueryRow(`
		SELECT `+entrySelectCols+`
		FROM entries JOIN cards ON entries.card_id = cards.id
		WHERE entries.id = ?
	`, entryID)
	var e Entry
	if err := scanEntry(row, &e); err != nil {
		return Entry{}, err
	}
	return e, nil
}

func ListEntries(db *sql.DB) ([]Entry, error) {
	rows, err := db.Query(`
		SELECT `+entrySelectCols+`
		FROM entries JOIN cards ON entries.card_id = cards.id
		ORDER BY cards.color_sort_key, cards.type_sort_key, cards.mana_value, cards.name, entries.created_at, entries.id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := []Entry{}
	for rows.Next() {
		var e Entry
		if err := scanEntry(rows, &e); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func SetGiver(db *sql.DB, entryID int, requester string) (Entry, bool, error) {
	res, err := db.Exec(`
		UPDATE entries SET giver_name=?
		WHERE id=? AND (giver_name IS NULL OR giver_name=?)
	`, requester, entryID, requester)
	if err != nil {
		return Entry{}, false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return Entry{}, false, err
	}

	entry, err := getEntry(db, entryID)
	if err == sql.ErrNoRows {
		return Entry{}, false, ErrEntryNotFound
	}
	if err != nil {
		return Entry{}, false, err
	}

	if affected == 0 {
		return entry, true, nil
	}
	return entry, false, nil
}

func ClearGiver(db *sql.DB, entryID int, requester string) (bool, error) {
	res, err := db.Exec(`UPDATE entries SET giver_name=NULL WHERE id=? AND giver_name=?`, entryID, requester)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	return affected > 0, err
}

func RemoveEntry(db *sql.DB, entryID int, requester string) (bool, error) {
	res, err := db.Exec(`DELETE FROM entries WHERE id=? AND (seeker_name=? OR giver_name=?)`, entryID, requester, requester)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	return affected > 0, err
}

func EntryExists(db *sql.DB, entryID int) (bool, error) {
	var one int
	err := db.QueryRow(`SELECT 1 FROM entries WHERE id=?`, entryID).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
