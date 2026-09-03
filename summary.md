# Looking for Cards — Code Summary

## What It Does

A webapp where users paste a list of Magic: The Gathering cards they're looking for; others click a card tile to offer to give/trade it. Card images load straight from Scryfall. No auth — just a name (carried in the URL `?user=<name>`). Seekers tag themselves with occasions (recurring meetups or dated events, shareable via `?occasion=<name>` URL params) so givers can filter by where they'll meet.

## Architecture

```
Browser <--> Go server <--> SQLite (data.db)
        |
        +---> Scryfall API (directly from browser: card images + metadata at add-time)
```

- Single Go binary with embedded frontend (`go:embed frontend`).
- SQLite for cards + entries. No filesystem uploads.
- Browser calls Scryfall directly (CORS-friendly, no backend proxy).
- The backend never calls Scryfall; the browser resolves card metadata (colors, type_line) at add-time and sends it to the server, which computes the authoritative sort keys.

## Project Layout

```
looking-for-cards/
├── main.go              // embeds frontend/, opens DB, starts server on :8080
├── go.mod / go.sum
├── Makefile             // test / build / run
├── Dockerfile           // multi-stage, CGO_ENABLED=1
├── fly.toml             // Fly.io, volume at /data
├── .air.toml            // live-reload dev
├── .gitignore
├── .github/workflows/fly-deploy.yml  // CI: deploy to Fly on push to main
├── frontend/
│   └── index.html       // single SPA (inline CSS + JS, no framework)
├── server/
│   ├── server.go        // Server struct, route registration, static serving
│   ├── entries.go       // HTTP handlers for /api/entries* + sendJSONError
│   ├── occasions.go     // Occasion types, queries, /api/occasions* handlers
│   ├── db.go            // SQLite init, schema, model types, queries
│   ├── sortkeys.go      // ColorSortKey + TypeSortKey (pure functions)
│   └── tests/
│       ├── helpers_test.go
│       ├── sortkeys_test.go
│       ├── entries_test.go
│       └── occasions_test.go
└── data/                // data.db lives here (gitignored)
```

## Dependencies

Only `github.com/mattn/go-sqlite3` (CGO). No uuid, no image libs, no SMTP.

## Configuration (env vars)

| Variable | Default | Purpose |
|---|---|---|
| `DATA_DIR` | `data` | Directory containing `data.db` |
| `PORT` | `8080` | Listen port |

## Database Schema

`cards` — keyed by `(name, set_code, collector_number)` where `set_code` is `""` when no set was specified and `collector_number` is `""` when none was given (empty strings, not NULL, so `UNIQUE(name, set_code, collector_number)` enforces exactly one row per name+set+printing):

| Column | Type | Notes |
|---|---|---|
| `id` | INTEGER PK | Auto-increment |
| `name` | TEXT | Card name |
| `set_code` | TEXT | `""` = no set (default print) |
| `collector_number` | TEXT | `""` = none specified; Scryfall collector number (string, may contain letters/`★`) |
| `colors` | TEXT | Concatenated color letters, e.g. `"R"`, `"WU"`, `""` = colorless |
| `type_line` | TEXT | Scryfall type line |
| `mana_value` | REAL | Scryfall `cmc`; `NULL` = unknown (legacy card not yet backfilled) |
| `image_url` | TEXT | Direct `cards.scryfall.io` image URL (no rate limit); `""` = not yet resolved |
| `color_sort_key` | INTEGER | Computed by `ColorSortKey` (default 5) |
| `type_sort_key` | INTEGER | Computed by `TypeSortKey` (default 8) |
| `created_at` | DATETIME | Default CURRENT_TIMESTAMP |

Index `idx_cards_sort(color_sort_key, type_sort_key, mana_value, name)`.

`entries` — one unit-sought row. Multiple rows may reference the same `card_id` (2 Lightning Bolts wanted = 2 rows):

| Column | Type | Notes |
|---|---|---|
| `id` | INTEGER PK | Auto-increment |
| `card_id` | INTEGER FK → cards(id) |
| `seeker_name` | TEXT | The user looking for the card |
| `giver_name` | TEXT | NULL = no giver yet |
| `created_at` | DATETIME | Default CURRENT_TIMESTAMP |

Indexes on `seeker_name`, `giver_name`, `card_id`.

`occasions` — named events (recurring or one-time) tied to **seekers**, not entries:

| Column | Type | Notes |
|---|---|---|
| `id` | INTEGER PK | Auto-increment (exposed as `id` in JSON so the frontend can save selections) |
| `name` | TEXT | UNIQUE |
| `date_or_recurring` | TEXT | `'recurring'` or `YYYY-MM-DD` |

`seeker_occasions` — which occasions a seeker attends (`PRIMARY KEY (seeker_name, occasion_id)` + index on `occasion_id`). An entry inherits its occasions from its seeker at list time; there is no per-entry occasion column. A fresh database seeds one recurring occasion `hedwig` and links every pre-existing seeker to it (idempotent `INSERT OR IGNORE` migration `migrateSeedHedwig`).

### Go model types

`Card` exposes `name`, `set`, `collector_number`, `colors`, `type_line`, `mana_value` (JSON `null` when unknown), `image_url` (JSON); `id` and sort keys are `json:"-"`. `Entry` exposes `id`, nested `card`, `seeker`, `giver` (`""` when NULL), `occasions` (array of occasion names inherited from the seeker, always present — `[]` when the seeker has none), `created_at`. `Occasion` exposes `id`, `name`, `date_or_recurring`, `past` (computed server-side: true when non-recurring and date < today, server local date).

## Sort-key computation (`server/sortkeys.go`)

`ColorSortKey(colors)`: `""`→5 (colorless); length≥2→6 (multicolor); single letter `W=0, U=1, B=2, R=3, G=4`; any other→5.

`TypeSortKey(typeLine)`: empty→8. Split on `" — "`, take the left (type) part; split on spaces; skip supertypes (`legendary, basic, snow, world, elite, ongoing`); **creature wins** — if `creature` appears among the remaining tokens, return the creature index (1) regardless of order, so `Artifact Creature` and `Enchantment Creature` sort as creatures; otherwise return the first remaining token's index in `planeswalker=0, creature=1, artifact=2, enchantment=3, instant=4, sorcery=5, land=6, battle=7`; none→8 (e.g. "Artifact Land"→2, "Enchantment Land"→3).

Keys are computed server-side at upsert time and stored — never trusted from the client.

## API Endpoints

All mutating endpoints take the actor's name from `?user=<name>` (no auth; empty/missing → 400). All errors are JSON `{"error":"..."}`.

| Method | Path | Success | Errors |
|---|---|---|---|
| `GET` | `/api/entries` | `200 {"entries":[…], "total":N}` (all entries, server-sorted) | — |
| `POST` | `/api/entries?user=<seeker>` | `201 {"created":N, "errors":[{"line",...}]}` (one card object per unit; duplicates allowed; per-card errors recorded, batch continues) | `400` bad JSON / empty user |
| `POST` | `/api/entries/{id}/giver?user=<name>` | `200` updated entry JSON | `409 {"error":"taken","entry":{…}}` if another giver holds it; `404` if gone; `400` |
| `DELETE` | `/api/entries/{id}/giver?user=<name>` | `204` | `404` if gone or requester isn't the giver; `400` |
| `POST` | `/api/entries/{id}/remove?user=<name>` | `204` | `403` if requester is neither seeker nor giver; `404` if gone; `400` |
| `POST` | `/api/cards/metadata` | `204` (bulk-updates `cards.image_url` + `cards.mana_value` from body `{"cards":[{name,set,collector_number,image_url,mana_value}]}`) | `400` bad JSON |
| `GET` | `/api/occasions?include_past=true\|false` | `200 {"occasions":[...]}` (default `include_past=false`; past = non-recurring date before today) | — |
| `POST` | `/api/occasions?user=<name>` | `201` occasion JSON (body `{"name":..., "date_or_recurring":...}`; creator not stored) | `400` bad input / empty user; `409` duplicate name |
| `GET` | `/api/occasions/mine?user=<name>` | `200 {"occasions":[...]}` (the seeker's occasions) | `400` empty user |
| `POST` | `/api/occasions/mine?user=<name>` | `204` (replace-all body `{"occasion_ids":[...]}`) | `400` empty set / empty user / unknown id |
| `GET` | `/` | Embedded SPA | — |

- `SetGiver` is idempotent (re-claiming your own slot → 200) and allows self-offer (seeker may be the giver). It uses `UPDATE ... WHERE id=? AND (giver_name IS NULL OR giver_name=?)`; on zero rows affected, it re-reads to distinguish "taken by another" (409, current entry returned) from "not found" (404).
- `RemoveEntry` deletes where `seeker_name=? OR giver_name=?` (seeker can cancel anytime; giver can remove a matched entry). On zero rows, the handler checks existence to return 403 vs 404.

## Ordering

`ListEntries` sorts by `cards.color_sort_key, cards.type_sort_key, cards.mana_value, cards.name, entries.created_at, entries.id`. SQL `NULL` mana values sort first (treated as 0), so legacy cards without a resolved `cmc` appear before higher-cost same-color-and-type cards until backfilled. Color order: W, U, B, R, G, colorless, multicolor. Type order: Planeswalker, Creature, Artifact, Enchantment, Instant, Sorcery, Land, Battle. Artifact Creatures and Enchantment Creatures sort as Creatures (creature wins). Within the same color and type, cards rise by mana value (ascending), then name (A–Z). The server returns **all** entries (no pagination) because client-side filters need the full set.

## Frontend (`frontend/index.html`)

Single file, inline CSS + JS, dark theme (`#1a1a2e` / `#eee` / accent `#e94560`), system font stack, CSS grid `repeat(auto-fill, minmax(var(--card-min), 1fr))` (default `--card-min: 200px`, minimal `gap: 2px`), 768px responsive breakpoint.

- **Screen A** (name prompt): shown when URL has no `?user=`. On submit, sets `?user=<name>` via `replaceState`, switches to Screen B, loads entries. When the URL also carries `?occasion=<name>` params, a confirmation block ("Are you going to these occasions?", pre-checked) appears below the name input; on submit a truly new user (no saved occasions) joins the checked occasions via `POST /api/occasions/mine`, while an existing user keeps their saved occasions. Occasion URL params are preserved when `?user=` is added.
- **Screen B** (gallery): sticky top bar with "I'm seeking..." button (opens the add-cards modal), "My occasions" button (opens the My-occasions modal), card-name filter, color filter, card-type filter, occasion multiselect, seeker-name multiselect, giver-name multiselect, "Hide giver found" checkbox. Grid of all filtered tiles (no pagination; off-screen images stay deferred via `loading="lazy"`).
- **Tile**: Scryfall image displayed via plain `<img src=card.image_url loading="lazy">`. The `image_url` stored in the DB is a direct `cards.scryfall.io` URL (no rate limit), so images load instantly with no queue or 429 risk. If `image_url` is empty (legacy card not yet backfilled), it falls back to the `/cards/named` redirect URL. `onerror` swaps to a "no image" placeholder. Below the image, an info box holds each line on its own row: `seeker: <name>`, `giver: <name>` (only rendered when a giver exists), then conditional action buttons (only rendered when non-empty). The `.card-tile` is a flex column and `.info` is `flex:1`, so within a grid row all info boxes stretch to the tallest tile's height. `.info` is itself a flex row: an `.info-text` column (seeker/giver/actions, `justify-content:center`) on the left and the trash button on the right, with `align-items:center` on `.info` so the button is vertically centered against the whole text block even when a giver row is present. When a giver exists the info box gets a thick bright-green border (`#22c55e`) and the giver line text turns bright-green bold so already-given cards are easy to spot.
  - Clicking the image toggles the current user as giver (claim if empty, unclaim if it's yours; no-op if someone else claimed).
  - **Cancel** (seeker only) is a square icon button (garbage can SVG) rendered as a sibling of `.info-text` inside `.info`, so it stays vertically centered regardless of how many text rows the tile has. It hits `POST .../remove` (the only "remove this entry" action — there's no separate "fulfilled" state). **Give/Ungive** mirrors the image click and stays in the actions row.
- **Filters** compose (AND), client-side; any filter change or refresh re-renders the whole filtered grid. Color and type filters are single-select dropdowns whose groups mirror the server sort keys: color groups are White (W), Blue (U), Black (B), Red (R), Green (G), Colorless (`""`), Multicolor (2+ letters); type groups are Planeswalker, Creature, Artifact, Enchantment, Instant, Sorcery, Land, Battle, Other (computed client-side via the same supertype-skipping logic as `TypeSortKey`). An empty value means "all". Seeker, giver, and occasion filters are multiselect dropdowns rendered as checkbox lists — one checkbox per name gathered from the loaded entries (sorted A–Z); an empty selection means "all" (no filter applied), and the trigger button label shows `all seekers`/`all givers`/`all occasions` or `seekers (N)`/`givers (N)`/`occasions (N)` when N are checked. The occasion dropdown lists non-past occasions plus any URL-selected past occasions appended at the bottom. Panels close on outside click or when another opens, and selections that disappear from the dataset (after a refresh) are pruned automatically. Occasion filtering: with occasions selected, an entry is kept when any of its occasions is selected; with none selected, entries whose occasions are all past are hidden (entries with no occasions are always kept). Tiles show no occasion line — only `seeker:` and `giver:`.
- **My-occasions modal**: checkbox list of all occasions (non-past + currently selected past so a past one can be un-selected) plus a "+ New occasion" sub-form (name, Recurring/One-time radio, date input for one-time). "Create" posts to `/api/occasions` (409 → "That occasion already exists.") and pre-checks the new occasion. "Save" posts the selected ids to `/api/occasions/mine` (disabled when none checked), toasts "Saved.", then reloads entries so tiles pick up the new occasion data.
- **Add-cards modal**: the "I'm seeking..." button in the top bar opens a centered modal overlay (backdrop click or Escape closes) containing an occasion picker (shared `renderOccasionPicker` with the My-occasions modal, pre-checked with the user's occasions) above the textarea + "Add cards" button + progress + results. The "Add cards" button stays disabled until at least one occasion is checked and the textarea is non-empty. On submit, changed occasion selections are saved via `POST /api/occasions/mine` first, then entries post as before (no per-card occasion data — the server enriches entries at list time). A hint line below the heading tells CubeCobra users how to produce a paste-ready list: filter 'status:"not owned"', Export, Card Versions (.txt), copy-paste. Parsing: split on newlines; each line is `[qty] Name [(SET) [collector_number]]` — quantity, set code, and collector number are all optional; collector number requires a set code. Quantity prefix: `2 Name` / `2x Name`. Examples: `Faerie Guidemother`, `1 Faerie Guidemother (ELD)`, `Faerie Guidemother (ELD) 11`. Duplicates and quantity-prefix both expand to separate units. Metadata is resolved per unique `(name,set,collector_number)` from Scryfall via the batched `POST /cards/collection` endpoint (up to 75 identifiers per request, 500ms between batches); when a collector number is present the identifier uses `{set, collector_number}` (exact printing), otherwise `{name}` or `{name, set}`. A 270-card list is ~4 requests instead of 270. Unresolved lines are reported as errors and kept in the textarea; successful lines are cleared. Results show "Added N cards." plus failed lines. Each resolved card's `colors`, `type_line`, `cmc` (stored as `mana_value`), and `image_uris.normal` are captured from Scryfall and sent to the server.
- **Pinch-to-zoom** (mobile): a two-finger pinch on the grid resizes cards instead of page-zooming — pinch out → larger cards (zoom in), pinch in → smaller cards (zoom out, more cards per row). The card min width (`--card-min`) is clamped to `[80, 400]` px and persisted in `localStorage` (`cardMin`). `touch-action: pan-y` on `.card-grid` lets the browser keep vertical scrolling while the app handles the pinch gesture; single-tap tile clicks are unaffected.
- **Metadata backfill**: on `loadEntries`, if any cards have empty `image_url` or null `mana_value` (e.g. added before the metadata feature, or legacy rows from the first iteration whose `mana_value` was silently `0`), the frontend batch-fetches their metadata via `/cards/collection` (paced by the rate-limited queue, ~4 requests for 268 cards), extracts `image_uris.normal` and `cmc`, and persists both via `POST /api/cards/metadata`. This runs once per page load until all cards have both fields.
- **Concurrency UX**: 409 → toast "Sorry — <user> is already giving this card." + refresh; 404 → toast "This entry is no longer available." + refresh; 403 → toast "You can only remove your own entries."; network errors → "Network error — try Refresh."
- `popstate` re-syncs the screen from the URL so back/forward between `?user=` and `/` switches screens. Occasion selection syncs to the URL as repeated `?occasion=<name>` params (preserved alongside `?user=`); other filter changes leave the URL untouched.

### Scryfall integration notes

- Image/metadata URL uses `?exact=<name>&set=<set>` when a set is specified (Scryfall requires `exact=` with `set=`), otherwise `?fuzzy=<name>`. `format=image&version=normal` for images, `format=json` for metadata.
- `/cards/collection` rejects split/transform names with ` // ` (e.g. "Status // Statue") and only resolves the **front face** ("Status"); `/cards/named` accepts the full name. Identifiers sent to `/cards/collection` therefore use `frontFaceName(name)` (split on `//`, take the trimmed left part) when searching by name, while the full name is kept for the result-map key, the stored card name, and the `/cards/named` image fallback. When a collector number is present, the identifier uses `{set, collector_number}` (no name) per Scryfall's exact-printing schema.
- Metadata is fetched in batches with `POST /cards/collection` (up to 75 `name`/`name,set`/`set+collector_number` identifiers per request, paced 500ms apart by the rate-limited queue). The response is a List with `data` (found cards, in request order) and `not_found` (identifiers echoed as submitted); results are mapped back to requests by a positional walk that consumes `not_found` entries by a normalized key (triples are globally unique, so this is unambiguous). Each card's `image_uris.normal` (direct `cards.scryfall.io` URL, no rate limit), `colors`, `type_line`, and `cmc` (stored as `mana_value`) are captured and stored in the DB. DFC cards fall back to `card_faces[0].image_uris.normal` (top-level `cmc` is still used). A 429 response is retried after `Retry-After` (bounded to 2 retries). Unresolved lines → "card not found" (or "card or set not found"); a set mismatch on a resolved card → "set not found"; network error → retry once then report.
- Image display uses the stored `cards.scryfall.io` URL directly (`<img src>`), which has no rate limit. The `/cards/named` redirect endpoint (rate-limited) is only used as a fallback for cards without a stored URL.

## Deployment (Fly.io)

- `Dockerfile`: multi-stage `golang:1.26-alpine` (gcc/musl for CGO) → minimal `alpine:3.21` runtime.
- `fly.toml`: app `looking-for-cards`, region `ams`, persistent volume `lfk_data` at `/data`, `DATA_DIR=/data`, auto-stop/start machines.
- `.github/workflows/fly-deploy.yml`: on every push to `main`, runs `flyctl deploy --remote-only` via `superfly/flyctl-actions`. Requires the `FLY_API_TOKEN` repo secret (a Fly API token with deploy scope on the `looking-for-cards` app).

## Testing

```sh
CGO_ENABLED=1 go test ./server/tests/   # 55 tests
```

Integration tests use a temp-file SQLite DB + `httptest` recorder (mirroring the reference). Coverage includes: sort-key unit tests (including creature-wins-over-artifact/enchantment), batch add (entry/card counts, stored sort keys), duplicate lines, collector-number distinguishing printings, collector-number collapse without printing spec, collector-number in list response, image-URL backfill by collector number, list ordering across colors/types with mana-value then name tie-break, metadata backfill updating `mana_value` and re-sorting, `mana_value` migration converting a legacy NOT NULL DEFAULT 0 column to nullable with `0 → NULL`, the one-time `type_sort_key` recompute migration (creature-wins recomputation from stored `type_line`, idempotent via the `schema_meta` marker), occasions create/list/past-flag/duplicate/invalid-date/missing-user, seeker occasions set/replace-all/reject-empty/unknown-id/missing-user, the `hedwig` seed migration (links pre-existing seekers, idempotent), entries exposing occasion names (and `[]` when the seeker has none), self-offer, idempotent re-claim, 409-with-entry conflict, 404 after removal, clear-giver ownership, seeker/giver/non-party removal (204/403/404), the full-list pagination contract (200 entries returned in order).

## Workspace Conventions

- Never write scratch/temp files under `/tmp`. If a temporary file is needed, create a local folder inside the workspace (e.g. `tmp/`, gitignored) so artifacts stay with the repo.

## Known Limitations / Future Work

- Manual refresh only (no live updates / websockets).
- No auth; identity is the typed name (spoofable, by design).
- Server returns all entries on every fetch (fine for MVP; revisit server-side filtering if the gallery grows large).
