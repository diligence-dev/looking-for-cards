# Implementation Plan: `looking-for-cards`

A webapp where users paste a list of MTG cards they're looking for; others can click a card to offer to give/trade it. Card images come straight from Scryfall. No auth — just a name.

This document is self-contained: an implementing agent should not need to ask further questions. A reference app at `/home/pascal/dev/mtg-alternatives` follows the same architecture conventions (Go stdlib + embedded static SPA + SQLite); consult it for idioms where this plan says "mirror the reference."

---

## 1. Locked decisions

| Area | Decision |
|---|---|
| Tech stack | Go stdlib `net/http`, SQLite via `github.com/mattn/go-sqlite3` (CGO), single embedded static `frontend/index.html` (inline CSS+inline vanilla JS, no framework, no build step), Fly.io + persistent volume. Mirror `mtg-alternatives`. |
| Card images | Browser-direct per-name: `<img src="https://api.scryfall.com/cards/named?fuzzy=<name>[&set=<set>]&format=image&version=normal">`. No backend image proxy. Scryfall is CORS-friendly. |
| List format | One card per line: `Name` or `Name\|SET`. Optional leading quantity `2 Name` / `2x Name` expands to N entries. Duplicate lines also create separate entries. Both quantity-prefix and duplicate-line forms supported. |
| Seeker/giver model | **One entry = one unit.** Single seeker, single giver per entry. Same card → multiple independent tiles (2 Lightning Bolts wanted = 2 entries). |
| Giver click | Toggle (click again to un-offer). Self-offer allowed (a seeker may also be the giver of their own entry). |
| Identity | Name only, no auth. Name carried in the URL query param `?user=<name>`, not localStorage. Identity = the typed name (spoofable, accepted). |
| Refresh | "Refresh" button re-fetches via `fetch()` (no page reload). Name survives F5 because it lives in the URL. |
| Fulfillment / removal | Seeker can cancel anytime. Once a giver is matched, seeker *or* giver can remove the entry (fulfilled). Non-parties cannot remove. |
| Live updates | Manual refresh only. The offer endpoint rejects with a message if another user took the slot or the entry was removed in the meantime (409 / 404). |
| Ordering | Server-side sort by color, then card type, then name, then `created_at`. Color order: **W, U, B, R, G, colorless, multicolor**. Type order: **Planeswalker, Creature, Artifact, Enchantment, Instant, Sorcery, Land, Battle**. |
| Pagination | Server returns ALL entries sorted; client filters, then renders 50 at a time with a "Show more" button. (Server-side pagination is incompatible with client-side filtering, so the server returns the full sorted list.) |
| Filters (all client-side) | (a) Card-name text filter; (b) Giver-name text filter; (c) "Hide giver found" checkbox — hides any entry that currently has a giver. Filters compose (AND). |
| Color/type metadata | Browser fetches `https://api.scryfall.com/cards/named?fuzzy=<name>[&set=<set>]&format=json` at add-time, extracts `colors` (array) + `type_line`, sends them with the add request. Backend computes sort keys from these and stores them. The sort-key rules live server-side (authoritative). |
| Unresolved lines | If Scryfall returns 404 for a name (fuzzy miss) or a bad set, **reject that line, report it as an error, and create no entry for it.** Other valid lines still process. |
| Notifications | None. No email/SMTP. Matching is visible in-UI. |
| Deployment | Fly.io + 1GB persistent volume at `/data`, `DATA_DIR=/data`, multi-stage Docker with `CGO_ENABLED=1`. |
| Dev tooling | `air` live-reload; `make test/build/run`. |

---

## 2. Project layout

```
looking-for-cards/
├── main.go                 // go:embed frontend; open DB; start server on :8080
├── go.mod
├── go.sum
├── Makefile                // test / build / run
├── Dockerfile              // multi-stage, CGO_ENABLED=1
├── fly.toml                // app=looking-for-cards, volume at /data
├── .air.toml               // live-reload dev
├── .gitignore
├── frontend/
│   └── index.html          // single SPA (inline CSS+JS)
├── server/
│   ├── server.go           // Server struct + route registration
│   ├── entries.go          // HTTP handlers for /api/entries...
│   ├── db.go               // SQLite open, schema, migrations, queries
│   ├── sortkeys.go         // color/type sort-key computation
│   └── tests/
│       ├── entries_test.go
│       ├── sortkeys_test.go
│       └── helpers_test.go
└── data/
    └── .gitignore          // ignores data.db
```

Direct Go dependency: only `github.com/mattn/go-sqlite3`. No `golang.org/x/image`, no `google/uuid` (use crypto/rand for any IDs if needed — but autoincrement PKs suffice here), no SMTP lib.

---

## 3. SQLite schema (`server/db.go`)

```sql
CREATE TABLE IF NOT EXISTS cards (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  name           TEXT NOT NULL,
  set_code       TEXT NOT NULL DEFAULT '',    -- '' = no set specified (default print)
  colors         TEXT NOT NULL DEFAULT '',   -- concatenated color letters, e.g. "R", "WU", "" = colorless
  type_line      TEXT NOT NULL DEFAULT '',
  color_sort_key INTEGER NOT NULL DEFAULT 5, -- colorless default; see sortkeys.go
  type_sort_key  INTEGER NOT NULL DEFAULT 8, -- unknown default; see sortkeys.go
  created_at     DATETIME DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(name, set_code)
);
CREATE INDEX IF NOT EXISTS idx_cards_sort ON cards(color_sort_key, type_sort_key, name);

CREATE TABLE IF NOT EXISTS entries (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  card_id     INTEGER NOT NULL REFERENCES cards(id),
  seeker_name TEXT NOT NULL,
  giver_name  TEXT,                           -- NULL = no giver yet
  created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_entries_seeker ON entries(seeker_name);
CREATE INDEX IF NOT EXISTS idx_entries_giver  ON entries(giver_name);
CREATE INDEX IF NOT EXISTS idx_entries_card   ON entries(card_id);
```

- `cards` is keyed by `(name, set_code)` where `set_code` is `""` when no set was specified. So `("Lightning Bolt", "")` is one row and `("Lightning Bolt", "LEA")` another. This is the desired behavior. (Using `""` rather than NULL makes the plain `UNIQUE(name, set_code)` constraint enforce exactly one default-print row per name — SQLite treats NULLs as distinct under UNIQUE, so NULL would not enforce this.)
- `entries` is one unit-sought row. Multiple rows may reference the same `card_id` (2 Lightning Bolts = 2 rows).

### Go model types (`server/db.go`)

```go
type Card struct {
    ID           int    `json:"-"`
    Name         string `json:"name"`
    SetCode      string `json:"set"`       // "" when no set specified
    Colors       string `json:"colors"`
    TypeLine     string `json:"type_line"`
    ColorSortKey int    `json:"-"`
    TypeSortKey  int    `json:"-"`
}

type Entry struct {
    ID         int    `json:"id"`
    Card       Card   `json:"card"`
    SeekerName string `json:"seeker"`
    GiverName  string `json:"giver"`   // "" when NULL
    CreatedAt  string `json:"created_at"`
}
```

API responses never expose raw `card_id`/sort keys; they expose the nested `card` and the entry's `seeker`/`giver` names.

---

## 4. Sort-key computation (`server/sortkeys.go`)

Two pure functions; unit-test them exhaustively.

### `ColorSortKey(colors string) int`
Map the `colors` string (concatenated single-letter codes, uppercased, already deduped by Scryfall) to a bucket:
- `""` → `5` (colorless)
- length `>= 2` → `6` (multicolor)
- length `1`: `W=0, U=1, B=2, R=3, G=4`; any other letter → `5`

### `TypeSortKey(typeLine string) int`
1. If `typeLine` is empty → `8`.
2. Split on `" — "` (em-dash with spaces); take the **left** part (the supertype/type portion before the subtype). If no em-dash, use the whole string.
3. Split that part on spaces; lowercase-compare each token.
4. Skip supertypes: `legendary, basic, snow, world, elite, ongoing`.
5. Return the index of the **first remaining token** in this order: `planeswalker=0, creature=1, artifact=2, enchantment=3, instant=4, sorcery=5, land=6, battle=7`.
6. If none match → `8`.

Examples:
- `"Lightning Bolt"` → `Instant` → `4`
- `"Lightning Bolt — Instant"` → `Instant` → `4`
- `"Wrenn and Six"` → `Legendary Planeswalker — Wrenn` → skip `Legendary` → `Planeswalker` → `0`
- `"Solenkin Golem"` (typo test) → `Artifact Creature` → `Artifact` → `2` (Artifact beats Creature by first-match)
- `"Mountain"` → `Basic Land — Mountain` → skip `Basic` → `Land` → `6`
- `""` → `8`

### Storage
Compute both keys **server-side** from the `colors`+`type_line` the browser sends at add-time. Never trust the client's sort key. Store in `cards.color_sort_key` / `cards.type_sort_key` at upsert time.

---

## 5. DB query functions (`server/db.go`)

```go
// UpsertCard inserts or returns existing card id for (name, set). Computes sort keys.
func UpsertCard(db *sql.DB, name, setCode, colors, typeLine string) (int, error)

// AddEntry inserts one entry row.
func AddEntry(db *sql.DB, cardID int, seeker string) (int64, error)

// ListEntries returns all entries joined with cards, fully sorted.
// ORDER BY cards.color_sort_key, cards.type_sort_key, cards.name, entries.created_at, entries.id
func ListEntries(db *sql.DB) ([]Entry, error)

// SetGiver atomically claims the giver slot. Returns the updated entry and a
// bool "taken by someone else". Only succeeds if giver_name IS NULL or
// giver_name == requester (idempotent re-claim).
func SetGiver(db *sql.DB, entryID int, requester string) (Entry, taken bool, err error)

// ClearGiver removes the requester's own offer. No-op (returns notFound) if
// the entry's giver is someone else.
func ClearGiver(db *sql.DB, entryID int, requester string) (ok bool, err error)

// RemoveEntry deletes the row if requester is the seeker or the giver.
// Returns whether a row was deleted.
func RemoveEntry(db *sql.DB, entryID int, requester string) (ok bool, err error)
```

Notes:
- `SetGiver` uses `UPDATE entries SET giver_name=? WHERE id=? AND (giver_name IS NULL OR giver_name=?)`. After the update, `SELECT` the row back to build the `Entry` response. If `RowsAffected==0`, re-read the row: if it exists with a different `giver_name`, return `taken=true` (→ HTTP 409); if it doesn't exist, return not-found (→ HTTP 404).
- `ClearGiver` uses `UPDATE entries SET giver_name=NULL WHERE id=? AND giver_name=?`. `RowsAffected==0` → either the entry is gone or someone else is the giver; both map to 404/403 as below.
- `RemoveEntry` uses `DELETE FROM entries WHERE id=? AND (seeker_name=? OR giver_name=?)`. Note: a seeker can remove anytime (cancel) — covered by `seeker_name=?`. A giver can remove only when matched — covered by `giver_name=?` (NULL never equals the requester, so a giver slot that's empty blocks this path, which is correct: a non-seeker can't remove an unmatched entry). `RowsAffected==0` → 403/404.

---

## 6. Backend routes (`server/entries.go`)

`Server` struct and route registration mirror the reference (`server/server.go`):

```go
type Server struct {
    db       *sql.DB
    frontend fs.FS
    mux      *http.ServeMux
}

func (s *Server) registerRoutes() {
    s.mux.HandleFunc("/api/entries", s.handleEntries)                 // GET, POST
    s.mux.HandleFunc("/api/entries/", s.handleEntryByID)              // sub-routes below
    if s.frontend != nil {
        s.mux.Handle("/", http.FileServer(http.FS(s.frontend)))
    }
}
```

Because Go 1.22+ `ServeMux` patterns conflict when mixing `{id}` with literal sub-paths, register the explicit paths:

```go
s.mux.HandleFunc("POST   /api/entries/{id}/giver",  s.handleSetGiver)
s.mux.HandleFunc("DELETE /api/entries/{id}/giver",  s.handleClearGiver)
s.mux.HandleFunc("POST   /api/entries/{id}/remove", s.handleRemoveEntry)
```

The actor's name comes from the `?user=<name>` query param on every mutating endpoint (no auth). Empty/missing `user` → 400.

### Endpoints

| Method | Path | Body / Params | Success | Errors |
|---|---|---|---|---|
| `GET` | `/api/entries` | — | `200 {"entries":[…], "total":N}` (all entries, server-sorted) | — |
| `POST` | `/api/entries?user=<seeker>` | `{"cards":[{name,set,colors,type_line}, …]}` (one object **per unit**; duplicates allowed; `set` may be `""`) | `201 {"created":N, "errors":[{"line":i,"name":"…","error":"…"}]}` | `400` bad JSON / empty `user` |
| `POST` | `/api/entries/{id}/giver?user=<name>` | — | `200` updated entry JSON | `409 {"error":"taken","entry":{…}}` if another giver holds it (include current entry so the FE can show who); `404` if entry gone; `400` empty user |
| `DELETE` | `/api/entries/{id}/giver?user=<name>` | — | `204` | `404` if entry gone or requester isn't the giver; `400` empty user |
| `POST` | `/api/entries/{id}/remove?user=<name>` | — | `204` | `403` if requester is neither seeker nor giver; `404` if entry gone; `400` empty user |
| `GET` | `/` | — | Serve embedded `frontend/index.html` | — |

`{id}` read via `r.PathValue("id")`.

### POST `/api/entries` semantics
- Parse `user` from query; if empty → 400.
- Parse JSON body: `{"cards":[{name, set, colors, type_line}, ...]}`.
- For each card object: `UpsertCard` (computes sort keys), then `AddEntry(cardID, user)`.
- On per-card error (e.g. DB failure), record into `errors[]` with the line index and continue (don't abort the whole batch).
- Return `created` = number successfully inserted, plus `errors`.
- The **browser is responsible for resolving Scryfall metadata** and emitting one card object per unit (expanding quantities and duplicate lines) before calling this endpoint. The backend does no Scryfall calls and no quantity parsing.

---

## 7. Scryfall integration (frontend only)

### Image URL (display)
```
https://api.scryfall.com/cards/named?fuzzy=<encodeURIComponent(name)>&format=image&version=normal
```
Append `&set=<encodeURIComponent(set)>` when `set` is non-empty. Use directly as `<img src>`. Set `loading="lazy"`. Add `onerror` to swap to a placeholder ("image not found").

### Metadata fetch (at add-time, one per unique card)
```
fetch(`https://api.scryfall.com/cards/named?fuzzy=${encodeURIComponent(name)}&format=json` + (set ? `&set=${encodeURIComponent(set)}` : ''))
```
Read `data.colors` (array of single-letter codes, e.g. `["R"]`, `[]` for colorless) and `data.type_line` (string). Join `colors` into a single string `"R"`. If `set` was given, also verify `data.set` matches (Scryfall may resolve to a different print if the set code is invalid — if mismatch, treat as an error for that line).

### Rate limiting
Scryfall asks for 50–100ms between requests. Serialize the metadata fetches with `await sleep(100)` between calls. Do not fire them in parallel.

### Error handling per line
- HTTP 404 from Scryfall → line is "not found", no entry created, reported in the results UI.
- HTTP 404 specifically because `set` doesn't exist → same, message "set not found".
- Network error → retry once, then report.
- Other 4xx/5xx → report generic "Scryfall error".

---

## 8. List parsing (textarea → batch)

In the frontend, when the user clicks "Add cards":

1. Read textarea value. Split on `\n`.
2. For each line (index `i` from 0):
   - Trim. Skip empty lines.
   - Match optional leading quantity: `/^\s*(\d+)\s*x?\s+(.*)$/`. If matched, `qty = parseInt($1)`, rest = `$2`; else `qty = 1`, rest = whole line.
   - Split rest on the first `|`: `name = parts[0].trim()`, `set = (parts[1] || '').trim().toUpperCase()`.
   - If `name` is empty → error "empty name", skip.
   - Emit `qty` copies of `{name, set}` into a flat `units` array, each tagged with its source line index for error reporting.
3. Build the set of unique `(name,set)` pairs across all units.
4. For each unique pair, fetch Scryfall metadata (serialized, 100ms apart). Map pair → `{name, set, colors, type_line}` or → error.
5. Build the request body: for each unit (in original order), look up its resolved metadata; if resolved, push `{name, set, colors, type_line}` to `cards[]`; if unresolved, record the line as an error.
6. If `cards` is non-empty, `POST /api/entries?user=<name>` with `{"cards":[...]}`.
7. Display results: "Added N cards." + list of failed lines with reasons. Clear successful lines from the textarea (keep failed lines so the user can fix them). Then refresh the gallery.

---

## 9. Frontend (`frontend/index.html`) — single file

One HTML file with inline `<style>` and inline `<script>`. Dark theme mirroring the reference (body `#1a1a2e`, text `#eee`, accent `#e94560`, system font stack). CSS grid `repeat(auto-fill, minmax(200px, 1fr))` for the tile grid; responsive breakpoint at 768px.

### State (module-scoped `let`s in the script)
```js
let currentUser = null;      // from ?user=
let allEntries = [];         // last fetched, server-sorted
let displayedCount = 0;      // how many rendered after filtering
const PAGE_SIZE = 50;
```

### Screen A — Name prompt
Shown iff URL has no `?user=` (or `?user=` is empty). Layout: centered card with a heading, a text input "Your name", and a "Continue" button. Button disabled while input empty. On submit (button click or Enter):
- `currentUser = input.value.trim()`
- `history.replaceState(null, '', '?user=' + encodeURIComponent(currentUser))`
- Hide Screen A, show Screen B, call `loadEntries()`.

### Screen B — Main gallery
Top bar (sticky):
- `@<currentUser>` with a **"change"** link → sets `history.replaceState(null,'','/')`, `currentUser=null`, show Screen A.
- **Refresh** button → `loadEntries()` (re-`GET /api/entries`, re-render, preserve filter state).
- Card-name **filter** input (`<input type="search">`) → on input, re-run `applyFiltersAndRender()`.
- Giver-name **filter** input (`<input type="search" placeholder="giver name">`) → on input, re-render.
- **"Hide giver found"** checkbox (`<input type="checkbox">`) → on change, re-render. When checked, hide entries where `giver !== ""`.

Grid container + a "Show more" button (shown when `displayedCount < filtered.length`).

### Tile rendering
For each entry (after filtering, paginated to `displayedCount`):
```html
<div class="card-tile" data-id="<id>">
  <img src="<scryfall image url>" loading="lazy" alt="<name>" />
  <div class="name"><name> [<set> if set]</div>
  <div class="seeker">looking for: <seeker></div>
  <div class="giver">giver: <giver | "—"></div>
  <div class="actions">
    <!-- conditional buttons, see below -->
  </div>
</div>
```
Image `src` is the Scryfall named-image URL (§7). `onerror` swaps to a placeholder.

Clicking the **image** toggles the current user as giver:
- If `giver === ""` → `POST /api/entries/{id}/giver?user=<currentUser>`.
- If `giver === currentUser` → `DELETE /api/entries/{id}/giver?user=<currentUser>`.
- If `giver` is someone else → do nothing on click (or show a tooltip "already claimed by <giver>"). (Self-offer is allowed, so the only "can't claim" case is when someone else already has it.)

### Action buttons (conditional)
- **Cancel** — visible iff `seeker === currentUser`. Calls `POST /api/entries/{id}/remove?user=<currentUser>`. (Seeker can cancel anytime.)
- **Fulfilled** — visible iff a giver exists AND (`seeker === currentUser` OR `giver === currentUser`). Calls `POST /api/entries/{id}/remove?user=<currentUser>`. (Either party closes the match.)
- **Give / Ungive** — visible when `seeker !== currentUser` and (`giver === ""` or `giver === currentUser`). Clicking does the same as clicking the image (claim/unclaim). Optional convenience button; can be omitted if the image-click is enough — implementer's choice, but keep image-click always working.

### Concurrency / error UX
- `POST giver` → 409: toast `"Sorry — <otherUser> is already giving this card."` and call `loadEntries()` to refresh the changed entry.
- `POST giver` / `DELETE giver` / `POST remove` → 404: toast `"This entry is no longer available."` and `loadEntries()`.
- `POST remove` → 403: toast `"You can only remove your own entries."` (shouldn't happen if the UI hides the button correctly, but guard anyway).
- Any network error: toast `"Network error — try Refresh."`.

### Bottom: add-cards form
```html
<textarea id="card-list" placeholder="One card per line, e.g.&#10;Lightning Bolt&#10;2 Counterspell&#10;Lightning Bolt|LEA"></textarea>
<button id="add-btn">Add cards</button>
<div id="add-results"></div>
```
On "Add cards" click → run §8 parsing. Show progress ("Resolving N card names…") while metadata fetches are in flight, then final results in `#add-results`. Disable the button while processing.

### `loadEntries()`
```js
fetch('/api/entries').then(r => r.json()).then(data => {
  allEntries = data.entries;
  applyFiltersAndRender();
});
```

### `applyFiltersAndRender()`
1. Start from `allEntries` (already server-sorted).
2. Apply card-name filter: `entry.card.name` lowercased includes the filter text (trimmed). Empty filter = pass all.
3. Apply giver-name filter: `entry.giver` lowercased includes the filter text (trimmed). Empty filter = pass all. (Note: this matches entries whose giver contains the text; entries with no giver are excluded when the filter is non-empty, unless the user types something that should match empty — keep it simple: non-empty filter requires a non-empty giver that contains the text.)
4. Apply "Hide giver found": if checked, drop entries where `giver !== ""`.
5. Slice to `displayedCount` (initialized to `PAGE_SIZE` on each filter change). Render.
6. Toggle "Show more" visibility: show iff `displayedCount < filtered.length`.
7. "Show more" click → `displayedCount += PAGE_SIZE`, re-render (without re-filtering the source, just re-slice).

Reset `displayedCount = PAGE_SIZE` whenever the filters change or `loadEntries()` runs.

### Init on load
```js
const params = new URLSearchParams(location.search);
currentUser = params.get('user') || null;
if (currentUser) { showScreenB(); loadEntries(); } else { showScreenA(); }
```
Also handle `popstate` so the browser back/forward between `?user=` and `/` switches screens.

---

## 10. `main.go`

Mirror the reference. Key points:
- `//go:embed frontend` to embed the directory as `embed.FS`.
- `fs.Sub(frontendFS, "frontend")` to strip the prefix so the SPA serves at `/`.
- Read `DATA_DIR` (default `"data"`); `dbPath = dataDir + "/data.db"`.
- Open/initialize SQLite, run schema (and any migrations).
- `server.NewServer(db, frontendSubFS)`, `ListenAndServe(":8080", s.mux)`.
- Env: `DATA_DIR` (default `data`), `PORT` (default `8080`). No `BASE_URL`, no `SMTP_*`.

---

## 11. Testing (`server/tests/`)

Run with `CGO_ENABLED=1 go test ./server/tests/`. Follow the reference's integration-test style (open a temp SQLite DB, hit handlers via `httptest`).

### `sortkeys_test.go`
- `ColorSortKey`: each of `W,U,B,R,G` → 0–4; `""` → 5; `"WU"`,`"RGB"`,`"UB"` → 6; unknown letter → 5.
- `TypeSortKey`: each type token alone → its index; `"Legendary Creature"` → 1; `"Basic Land"` → 6; `"Artifact Creature"` → 2; `"Snow Instant"` → 4; `""` → 8; `"Creature — Goblin"` → 1; full `type_line` with em-dash parses the left side only.

### `entries_test.go`
- **Add**: POST a batch of 3 cards (2 unique) → 3 entries created; `cards` table has 2 rows; sort keys stored.
- **Add with duplicate lines**: `["Lightning Bolt","Lightning Bolt"]` → 2 entries, 1 card row.
- **List ordering**: insert cards with known color/type; assert the returned order matches `W,U,B,R,G,colorless,multicolor` and within a color the type order `Planeswalker,Creature,Artifact,Enchantment,Instant,Sorcery,Land,Battle`; tie-break by name then `created_at`.
- **SetGiver idempotent**: requester re-claims own slot → 200 (no 409).
- **SetGiver conflict**: entry has giver `"alice"`; `"bob"` POSTs → 409 with the current entry in the body.
- **SetGiver 404**: remove entry, then POST giver → 404.
- **ClearGiver**: only the current giver can clear; a different user → 404 (no rows affected).
- **RemoveEntry seeker**: seeker removes an unmatched entry → 204; second call → 404.
- **RemoveEntry giver**: entry has giver `"alice"`; `"alice"` removes → 204; a non-party user → 403.
- **RemoveEntry non-party**: a user who is neither seeker nor giver → 403.
- **Self-offer allowed**: seeker is `"alice"`; `"alice"` POSTs giver → 200 (no special block).
- **Pagination contract**: server returns all entries (no `limit`/`offset`), sorted. Confirm a 200-entry fixture returns all 200 in order.

### `helpers_test.go`
- A `newTestDB(t)` that opens an in-memory or temp-file SQLite DB and runs the schema.
- A `doJSON(t, method, path, body)` helper that hits the server via `httptest.NewServer`.

---

## 12. Deployment & dev tooling

### `Dockerfile` (mirror reference)
```dockerfile
FROM golang:1.24-alpine AS builder
RUN apk add --no-cache gcc musl-dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -o looking-for-cards .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /app/looking-for-cards .
EXPOSE 8080
CMD ["./looking-for-cards"]
```
(Use the Go version that matches `go.mod`. The reference pins `go1.26.4` via the toolchain; the implementer may pin similarly or use a stable `golang:1.24-alpine` image — just keep `CGO_ENABLED=1`.)

### `fly.toml`
```toml
app = "looking-for-cards"
primary_region = "ams"

[build]
  dockerfile = "Dockerfile"

[env]
  DATA_DIR = "/data"

[mounts]
  source = "lfk_data"
  destination = "/data"

[http_service]
  internal_port = 8080
  force_https = true
  auto_stop_machines = "stop"
  auto_start_machines = true
  min_machines_running = 0
```

### `Makefile`
```makefile
.PHONY: test build run
test:
	CGO_ENABLED=1 go test ./server/tests/
build:
	go build -o looking-for-cards .
run: build
	./looking-for-cards
```

### `.air.toml`
Watch `.go` and `.html` files; rebuild to `./tmp/main`; 1000ms delay. Exclude `*_test.go`, `data/`, `tmp/`.

### `.gitignore`
Ignore the binary, `tmp/`, `data/data.db`. Commit `data/.gitignore` with contents:
```
data.db
```

---

## 13. Implementation order (recommended)

1. `go mod init`, add `github.com/mattn/go-sqlite3`, `main.go` skeleton with embedded `frontend/index.html` placeholder.
2. `server/sortkeys.go` + `sortkeys_test.go` — get the pure logic right first (TDD).
3. `server/db.go` — schema, open, `UpsertCard`, `AddEntry`, `ListEntries`, `SetGiver`, `ClearGiver`, `RemoveEntry`.
4. `server/entries.go` — handlers wiring the DB functions; `server/server.go` — `Server` + routes.
5. `server/tests/entries_test.go` — full integration coverage (§11).
6. `frontend/index.html` — Screen A, then Screen B (top bar, grid, tile, add form), then filters, pagination, concurrency UX.
7. `Dockerfile`, `fly.toml`, `Makefile`, `.air.toml`, `.gitignore`, `data/.gitignore`.
8. Manual smoke test: run locally, open two browser windows with different `?user=` names, paste a list in one, click to give in the other, confirm removal flows, confirm 409 toast on race.

---

## 14. Edge cases & reminders for the implementer

- `UNIQUE(name, set_code)` with `set_code` stored as `""` (empty string) for "no set": the plain `UNIQUE(name, set_code)` constraint then enforces exactly one default-print row per name (NULLs would be treated as distinct under UNIQUE, so we avoid NULL). The Go model uses `SetCode string` where `""` means no set; the frontend sends `""` when no `|SET` was given.
- Scryfall `format=image` redirects (302) to `cards.scryfall.io`; browsers follow automatically for `<img src>`. No special handling.
- Scryfall `format=json` for `/cards/named` returns a Card object; on 404 it returns `{"code":"not_found",...}` with HTTP 404 — check `response.ok` and the `code` field.
- When `set` is provided, Scryfall's `/cards/named` requires `exact=` (not `fuzzy=`) together with `set=`. **Resolution:** when `set` is provided, use `?exact=<name>&set=<set>`; when no set, use `?fuzzy=<name>`. Document this in a code comment in the frontend.
- Self-offer is allowed: do not add a `seeker != requester` check in `SetGiver`.
- The "Fulfilled" button is for closing a matched entry; the seeker can also use "Cancel" before a giver appears. Both hit the same `POST /api/entries/{id}/remove` endpoint; the backend doesn't distinguish — it just checks `seeker_name=? OR giver_name=?`.
- The server returns **all** entries on `GET /api/entries` because client-side filters need the full set. If the gallery ever grows huge, revisit with server-side filtering + pagination — out of scope for MVP.
- Do not add comments to code unless requested (per repo conventions).
- Follow the reference's code style: short, explicit handler functions; model types with JSON tags; `net/http` stdlib routing; no framework.
