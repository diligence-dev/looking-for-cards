# Occasions Feature — Implementation Plan

## Overview

Add an "occasion" concept: a potentially recurring event where givers/seekers meet. Occasions are tied to **seekers**, not individual entries — an entry inherits its occasions from its seeker. Occasions are filterable, appear in the URL (`?occasion=<name>`), and can be created by anyone.

## Data Model

New tables added in `server/db.go` `InitDB` (in the same `CREATE TABLE` block, before migrations run):

```
occasions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE,
  date_or_recurring TEXT NOT NULL   -- 'recurring' or 'YYYY-MM-DD'
)

seeker_occasions (
  seeker_name TEXT NOT NULL,
  occasion_id INTEGER NOT NULL REFERENCES occasions(id),
  PRIMARY KEY (seeker_name, occasion_id)
)
CREATE INDEX idx_seeker_occasions_occasion ON seeker_occasions(occasion_id);
```

Notes:
- No `created_at` on occasions.
- An entry's occasions are defined by its seeker's `seeker_occasions` rows — no per-entry occasion column.
- A seeker must have at least one occasion (enforced at add-time in the frontend).

### Migrations (idempotent)

1. `migrateCreateOccasions` — the two `CREATE TABLE IF NOT EXISTS` + index. Can just be in the main `InitDB` block (tables are `IF NOT EXISTS`).
2. `migrateSeedHedwig` — idempotent:
   - `INSERT OR IGNORE INTO occasions (name, date_or_recurring) VALUES ('hedwig', 'recurring')`.
   - For every distinct `seeker_name` in `entries` with no row in `seeker_occasions`, insert a link to the `hedwig` occasion's id. Use `INSERT OR IGNORE` against the PK constraint so re-runs don't duplicate.
   - No `schema_meta` marker needed — `INSERT OR IGNORE` makes it idempotent.

## Backend

### New file `server/occasions.go`

Types:
```go
type Occasion struct {
    ID              int    `json:"-"`
    Name            string `json:"name"`
    DateOrRecurring string `json:"date_or_recurring"`
    Past            bool   `json:"past"`   // computed: true if non-recurring and date < today
}
```

Functions:
- `IsPast(dateOrRecurring string) bool` — `false` if `"recurring"`; else parse `YYYY-MM-DD`, return `date < time.Now().Format("2006-01-02")` (server local date).
- `ListOccasions(db, includePast bool) ([]Occasion, error)` — `SELECT id, name, date_or_recurring FROM occasions ORDER BY name`. Compute `Past` per row. Filter out past rows when `includePast=false`.
- `UpsertOccasion(db, name, dateOrRecurring) (Occasion, error)` — validate: name non-empty; `date_or_recurring` is `"recurring"` or matches `^\d{4}-\d{2}-\d{2}$`. Insert. Return the created row. Duplicate name → return a sentinel error `ErrOccasionExists`.
- `GetOccasion(db, name string) (Occasion, error)` — for resolving URL `?occasion=<name>` to ids on the server if needed (frontend resolves names to ids via `GET /api/occasions`).
- `ListSeekerOccasions(db, seeker) ([]Occasion, error)` — join `seeker_occasions` with `occasions`, ordered by name.
- `SetSeekerOccasions(db, seeker, occasionIDs []int) error` — replace-all in a tx: delete all `seeker_occasions` rows for `seeker`, insert the new set. Caller enforces non-empty.
- `SeekerOccasionNames(db, seekers []string) (map[string][]string, error)` — one query `WHERE so.seeker_name IN (...)` returning `seeker_name, occasion.name` pairs; build the map. Used to enrich `ListEntries`.

### New HTTP handlers (in `server/occasions.go`, registered in `server/server.go`)

| Method | Path | Body | Success | Errors |
|---|---|---|---|---|
| `GET`  | `/api/occasions?include_past=true\|false` | — | `200 {"occasions":[...]}` (default `include_past=false`) | — |
| `POST` | `/api/occasions?user=<name>` | `{"name":..., "date_or_recurring":...}` | `201` occasion JSON | `400` bad input / empty user; `409` duplicate name |
| `GET`  | `/api/occasions/mine?user=<name>` | — | `200 {"occasions":[...]}` | `400` empty user |
| `POST` | `/api/occasions/mine?user=<name>` | `{"occasion_ids":[int...]}` | `204` | `400` empty set / empty user / unknown id |

- `POST /api/occasions` requires `?user=` for consistency with other mutating endpoints but does **not** store the creator (edit/delete out of scope).
- Duplicate occasion name → 409 with `{"error":"occasion already exists"}`.
- `POST /api/occasions/mine` with empty `occasion_ids` → 400 `{"error":"select at least one occasion"}`. Unknown id → 400.

### Modified `server/entries.go`

- `Entry` struct gets `Occasions []string \`json:"occasions"\`` (no `omitempty` — always present, possibly empty array).
- `ListEntries` — after fetching entries:
  1. Collect distinct seeker names from the result set.
  2. Call `SeekerOccasionNames(db, seekers)` → `map[seeker][]string`.
  3. Attach to each entry before encoding.
- No change to the SQL query itself; enrichment happens in Go after the fact.

### Route registration (`server/server.go`)

Add to `registerRoutes`:
```go
s.mux.HandleFunc("/api/occasions", s.handleOccasions)        // GET, POST
s.mux.HandleFunc("/api/occasions/mine", s.handleMyOccasions)  // GET, POST
```

## Tests (TDD — written first, must fail, then implement)

New file `server/tests/occasions_test.go`:

1. `TestOccasions_CreateAndList` — POST two occasions, GET returns both (with `include_past=false` if none past).
2. `TestOccasions_ListExcludesPast` — one recurring + one past date + one future date; `include_past=false` returns 2; `include_past=true` returns 3.
3. `TestOccasions_PastFlagComputedCorrectly` — recurring → `past:false`; past date → `past:true`; future date → `past:false`.
4. `TestOccasions_CreateDuplicateNameReturns409`.
5. `TestOccasions_CreateInvalidDateOrRecurringReturns400` — bad date string, missing name, empty `date_or_recurring`.
6. `TestOccasions_MissingUserReturns400` on POST.
7. `TestSeekerOccasions_SetAndList` round-trip — set [1,2], GET returns both.
8. `TestSeekerOccasions_ReplaceAllSemantics` — set [1,2] then [3], list returns [3].
9. `TestSeekerOccasions_RejectsEmpty` → 400.
10. `TestSeekerOccasions_UnknownOccasionIDReturns400`.
11. `TestSeekerOccasions_MissingUserReturns400` on both GET and POST.
12. `TestMigrate_HedwigSeedsExistingSeekers` — pre-insert entries with seekers alice/bob, run InitDB, assert `hedwig` occasion exists and both are linked.
13. `TestMigrate_HedwigIdempotent` — second InitDB doesn't duplicate links.
14. `TestListEntries_ExposesOccasionNames` — seeker with two occasions, their entries include both names in `e.occasions`.
15. `TestListEntries_OccasionsEmptyWhenSeekerHasNone` — entry's `occasions` is `[]` (transient state, e.g. a seeker whose occasions were somehow cleared).

Test order: write all, run `go test ./server/tests/` — new tests must fail (compile errors / missing functions). Then implement, run again — all pass.

## Frontend (`frontend/index.html`)

### State

New globals:
```js
let allOccasions = [];          // from GET /api/occasions?include_past=true
let myOccasionIDs = new Set();  // current user's occasion ids
const occasionSelected = new Set(); // URL/filter-selected occasion names
```

### Screen A — name prompt + occasion confirmation

Current behavior: shown when URL has no `?user=`. On submit, sets `?user=`, switches to Screen B.

New behavior: if URL has `?occasion=<name>` params AND no `?user=`:
- Show the name input (as today).
- Below it, show a confirmation block: "Are you going to these occasions?" followed by a checkbox list of the URL-specified occasions (pre-checked). Render each occasion's name; show its date or "recurring" next to it.
- On submit:
  1. Set `currentUser = name`.
  2. Build the URL: preserve all `?occasion=` params, add `?user=<name>`. `replaceState`.
  3. Call `GET /api/occasions/mine?user=<name>` to check if this seeker already has occasions.
     - **If they already have occasions** (existing user): keep their existing `myOccasionIDs`; ignore the confirmation checkboxes (they already told us what they're going to).
     - **If they have no occasions** (truly new user): save the checked occasion ids via `POST /api/occasions/mine` with the checked ids. Update `myOccasionIDs`.
  4. Switch to Screen B, load entries, apply the occasion filter from the URL.

If URL has no `?occasion=` and no `?user=`: current behavior (name prompt only).

If URL has `?user=`: skip Screen A (current behavior).

### Topbar additions (Screen B)

After the existing "I'm seeking…" button, add:
- `<button id="my-occasions-btn">My occasions</button>`
- Occasion multiselect (same pattern as `seeker-filter`/`giver-filter`):
  - `<div class="multiselect" id="occasion-filter"><button class="ms-trigger" type="button">all occasions</button><div class="ms-panel hidden"></div></div>`
  - Populated from `allOccasions`. The dropdown shows only non-past occasions, **plus** any URL-selected past occasions appended at the bottom (so the user can see and un-check them).
  - Trigger label: `all occasions` or `occasions (N)`.
  - Selection uses occasion **names** (not ids) — matches URL format.

### "My occasions" modal

Mirror the seek modal structure:
- Heading "My occasions".
- Checkbox list of all occasions (non-past + currently selected past, so user can un-select a past one).
- "+ New occasion" button → reveals an inline sub-form:
  - `name` input (required).
  - Radio: "Recurring" (default) / "One-time".
  - Date input (`type="date"`, shown only when "One-time" selected).
  - "Create" button → `POST /api/occasions` with `?user=<currentUser>`. On 201, prepend the new occasion to the list (pre-checked). On 409, toast "That occasion already exists." On 400, toast the error message.
- "Save" button → `POST /api/occasions/mine` with selected ids. Disabled when 0 checked. Toast "Saved." on success. Update `myOccasionIDs`. Then `loadEntries()` (to refresh tile occasion data).

### Add-cards modal — occasion picker

Add at the top of the add-section (above the hint/textarea):
- Refactor the occasion checkbox list + "+ New occasion" sub-form into a shared function: `renderOccasionPicker(container, selected, {allowCreate})` → returns the current selected set. Used by both "My occasions" modal and the add-cards modal.
- Pre-check `myOccasionIDs`.
- "Add cards" button disabled until **both**: ≥ 1 occasion checked AND textarea non-empty.
- On submit:
  1. If the selected set differs from `myOccasionIDs`: call `POST /api/occasions/mine` (await success; on failure toast and abort). Update `myOccasionIDs`.
  2. Resolve selected occasion ids from names (look up in `allOccasions`).
  3. `POST /api/entries` as today — no per-card occasion data sent (occasions come from the seeker).

Note: the entries POST body does **not** change. The server enriches entries with the seeker's occasions at list time.

### Tile info — NO change

Per the user's decision: **do not** add an occasion line to the tile. Tiles continue to show only `seeker:` and `giver:` lines. Occasions are visible via the occasion filter and "My occasions" modal.

### URL handling

- `?occasion=<name>` repeated for multi-select. Preserved alongside `?user=`.
- On load and on `popstate`: parse `occasion` params → `occasionSelected` set → render.
- On occasion-filter change: rebuild URLSearchParams (preserve `user`, set all `occasion` params), `replaceState`.
- On other filter changes (card-name, color, etc.): URL unchanged (matches existing behavior).

### Past-occasion filtering logic (in `applyFiltersAndRender`)

```
if occasionSelected.size > 0:
  keep entry if e.occasions intersects occasionSelected (by name)
else:
  keep entry if NOT every one of e.occasions is past
```

- URL-selected past occasions **are shown** (the shared link works retroactively).
- Non-URL-selected past occasions and their entries are hidden.
- An entry whose seeker has zero occasions (transient) is kept.

## Execution Order

1. Write `server/tests/occasions_test.go` with all 15 tests; run `go test ./server/tests/` — new tests must fail.
2. Implement `server/occasions.go` (types, functions, HTTP handlers) + migrations in `server/db.go` + route registration in `server/server.go` + modify `server/entries.go` for occasion enrichment; run tests — all pass.
3. Update `frontend/index.html`:
   - State globals.
   - Screen A occasion confirmation flow.
   - Topbar: "My occasions" button + occasion multiselect.
   - "My occasions" modal.
   - Shared `renderOccasionPicker` function.
   - Add-cards modal: occasion picker + submit-logic changes.
   - URL handling for `?occasion=`.
   - Past-occasion filter logic in `applyFiltersAndRender`.
4. Update `summary.md`: schema (new tables), API (new endpoints), frontend (occasion filter, my occasions modal, add-cards occasion picker, Screen A confirmation), URL params (`?occasion=`), migrations (hedwig seed).
5. Run `go test ./server/tests/` once more. Visually verify at http://localhost:8080 (assume `air` is running).
6. Say "done".

## Out of Scope

- Editing/deleting occasions.
- Server-side filtering of entries by occasion (frontend filters client-side, matching existing pattern).
- Tile occasion display line.
- Storing occasion creator.
- Changes to pinch-zoom, giver-flow, or other unrelated features.
