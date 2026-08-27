# xbet-api

REST API that wraps 1xbet's internal LineFeed JSON endpoints and exposes
clean, normalized sports data: **events**, **markets**, and **odds**.

Built with the Go standard library only. No external dependencies.

## How it works

1xbet has no public API. This service reverse-engineers the internal JSON
endpoints their own website uses and normalizes the cryptic payloads into a
clean model. All endpoints were verified against the live gateway.

**Upstream endpoints used:**

| Endpoint | Purpose |
|---|---|
| `GetChampsZip?sport=N&lng=en&partner=159` | leagues for a sport |
| `Get1x2_Zip?sports=N&count=K&lng=en&partner=159&getEmpty=true&gr=412&mode=3` | events (line/prematch) with team names, times |
| `GetGameZip?id=EVENT&lng=en&partner=159` | full event: all markets + odds |

**Reverse-engineering findings (things that will save you hours):**

- The new gateway lives at `/service-api/LineFeed/` on the regional domains
  (`1xbet.ng`, `1xbet.co.ke`, `1xbet.ci`, `1xbet.ug`). The old `/LineFeed/`
  path also exists on some hosts.
- Responses use the envelope `{"Id":0,"Success":true,"Error":"","Value":...}`.
- **`partner=159` is required** — with `partner=51` (the old value) champs
  return empty. `getEmpty=true&gr=412&mode=3` unlocks events.
- The events endpoint **ignores `sport` (singular) and requires `sports`
  (plural)** to filter by sport id.
- The edge gateway checks **query-string order**: `GetChampsZip` must start
  with `sport=N&lng=...` — any other order returns HTTP 406. The client
  therefore builds ordered query strings (never sorted).
- `Get1x2_VZip`, `GetGameZip?gameId=...` and `sportId=...` return 406 from
  this backend; the working variants are `Get1x2_Zip`, `GetGameZip?id=...`
  and `sport=...`.
- Events carry no decimal odds in the feed; odds come from `GetGameZip`,
  which returns a **flat outcome list** `{T,C,P,G}` where `T` is the outcome
  type, `C` the decimal odds, `P` the line (handicap/total) and `G` the
  market id. Outcome types decoded: 1/2/3 = 1/X/2, 4/5/6 = 1X/12/X2,
  7/8 = handicap sides, 9/10/11/12 = over/under, 2951–2954 = scaled
  total lines (P encoding `1.015→0.5, 16.03→1.5, ...`), 15–23 = correct
  score lines.

## Endpoints

| Method | Path | Description |
|---|---|---|
| GET | `/healthz` | liveness |
| GET | `/sports` | sport list (id → name; ids verified against live gateway) |
| GET | `/sports/{sport}/leagues` | leagues/championships for a sport |
| GET | `/sports/{sport}/events` | events for a sport (`status=live` = in-play) |
| GET | `/live/events?count=40` | **all in-play events across every sport** (scores + main odds) |
| GET | `/events/{id}/markets` | full event: all markets + all odds (line **or live**) |
| GET | `/events/{id}/odds` | main 1X2 odds snapshot (from markets) |
| GET | `/debug/raw?path=GetGameZip&id=...` | raw 1xbet JSON passthrough (for verifying mappings) |

### Query params

- `/sports/{sport}/events?count=50&league=ID&from=UNIX&to=UNIX&all=true`
  - `count` — max events (default 50)
  - `league` — comma-separated league ids (passed as `champs`)
  - `from` / `to` — unix timestamps filtering event start time (`tsFrom`/`tsTo`)
  - `all=true` — return **all** events for the sport (next 180 days), paging
    through time windows internally; windows hitting the ~50-event per-request
    cap are subdivided recursively until complete, then deduplicated and
    sorted. Matches the count shown on 1xbet's site (e.g. 88 boxing events,
    78 UFC events).

### Sport ids (live-verified)

`1` Football · `2` Ice Hockey · `3` Basketball · `4` Tennis · `5` Baseball ·
`6` Volleyball · `7` Rugby · `8` Handball · `9` Boxing · `10` Table Tennis ·
`11` Chess · `12` Billiards · `13` American Football · `14` Futsal ·
`16` Badminton · `18` Motorsport · `20` TV Games · `21` Darts ·
`26` Formula 1 · `27` Field Hockey · `28` Australian Rules · `30` Snooker ·
`36` Bicycle Racing · `40` Esports · `41` Golf · `44` Horse Racing ·
`48` Lacrosse · `53` Wrestling · `56` Martial Arts (MMA orgs: ONE, KSW, ...) ·
`80` Gaelic Football · **`189` UFC** (Fight Night cards, UFC 331/332/333, Road to UFC,
Prospective fights)

### Example responses

`GET /sports/56/events` → array of:

```json
{
  "id": 746855673,
  "sport_id": 56,
  "league_id": 2900783,
  "league_name": "Combatsport. AMC Fight Nights",
  "home": "Dmitry Vasenev",
  "away": "Vadim Litvin",
  "start_time": "2026-08-27T16:00:00Z",
  "status": "prematch",
  "raw_status": 2
}
```

`GET /events/746855673/markets` → array of:

```json
{
  "id": 1,
  "name": "Match Winner",
  "outcomes": [
    {"id": 1, "name": "1", "odds": 1.07},
    {"id": 2, "name": "X", "odds": 75},
    {"id": 3, "name": "2", "odds": 7.5}
  ]
}
```

## Live (in-play)

Live data comes from the frontend's dedicated v3 feed:

| Endpoint | Purpose |
|---|---|
| `main-live-feed/v3/games1x2` | in-play games with scores, period, main 1X2 odds |
| `main-live-feed/v3/gameEvents` | all markets for an in-play game |

Notes from reverse-engineering:

- `gr` (project id) is **domain-specific** (1557 = lite, 412 = ng); the client
  tries both per host.
- The v3 gateway is **query-order sensitive**: `cfView` leads, `gr` must sit
  between `fcountry` and `grMode` — any other arrangement returns 400.
- The v3 endpoints are absolute paths (not under `LineFeed/`).
- The v3 feed is rate-limited aggressively; keep the mirror list small (the
  client only demotes mirrors on transport failures, never on HTTP rejections).

```bash
# every live event right now:
curl 'localhost:8080/live/events?count=40'

# live events for one sport (football = 1):
curl 'localhost:8080/sports/1/events?status=live'

# markets of a live game (auto-falls back from line GetGameZip to live feed):
curl 'localhost:8080/events/747600716/markets'
```

### Locks (betting suspension)

Live markets **lock** when a goal/point/wicket happens or odds update. 1xbet
sends the state as optional fields that appear in payloads **only while
locked**: `blocked: true` on live outcomes (v3 gameEvents), `blocked` on
live game objects, and `Block: 1` on line (GetGameZip) outcomes. The API
surfaces them as `locked` on events, markets, and outcomes:

```json
{"name": "(69) Points Or More - Yes", "odds": 1.01, "locked": true}
```

REST snapshots catch locks active at request time only; real-time
lock/unlock transitions flow through 1xbet's websocket push feed, which is
not consumed by this API.

## Run

```bash
go build -o bin/xbet-api ./cmd/server
./bin/xbet-api -addr :8080
```

### Configuration

| Setting | Mechanism |
|---|---|
| Mirror list | `-mirrors` flag or env `XBET_MIRRORS` (comma-separated). Defaults: `1xbet.ng, 1xbet.co.ke, 1xbet.ci, 1xbet.ug` first (reachable without Cloudflare challenges), then the regional `.com` mirrors |
| Proxy | `HTTPS_PROXY`/`HTTP_PROXY` env vars (used automatically) |
| Port | `-addr` flag (default `:8080`) |
| API base path | `ClientOptions.BasePath` (default `/service-api/LineFeed/`; legacy `/LineFeed/` for old hosts) |

### Live verification

```bash
# raw payloads, for verifying/updating field mappings:
curl 'localhost:8080/debug/raw?path=GetChampsZip&sport=56'
curl 'localhost:8080/debug/raw?path=Get1x2_Zip&sports=56&count=10'
curl 'localhost:8080/debug/raw?path=GetGameZip&id=746855673'
```

## Caching

In-memory TTL cache: 60s for leagues, 15s for events and game details.

## Status codes

The feed's numeric status (`SS`) is preserved as `raw_status`. Observed
values 1–2 = prematch; live/finished mapping needs verification against
live data (the current LINE feed only returns upcoming events).

## Market dictionary

The flat odds format has no inline market names, but 1xbet publishes the
**official market-group template dictionary** at
`/genfiles/cms/betstemplates/bets_model_{short,full}_en_{chunk}.json`
(discoverable via `bets_model_map_{short,full}_en.json`).

This project embeds the merged dictionary (`internal/xbet/data/markets_en.json.gz`,
4890 groups, ~15k outcome templates, gzip'd to 150KB) and renders official
names: `Win In Round`, `Method Of Victory`, `Fight To Go The Distance`,
`When Will Bout End`, `Bout Duration, Minutes`, etc. Outcome templates are
rendered with the encoded parameter (e.g. `W1 In Round (1)`, `W1 In Round
(1-3)`, `Team 1: (1-15) Minutes`).

Base groups (1/2/8/15/17: Match Winner, Asian Handicap, Double Chance, ...)
are app-builtins with no template; they use the hardcoded map in
`internal/xbet/normalize.go`. Markets 1xbet itself doesn't template (e.g.
group 8389) fall back to `Market N` / `Outcome T`.

Regenerate the dictionary:

```bash
# fetch the chunk map + all chunks, merge short+full, gzip:
python3 tools/build-dict.py
```

## Tests

```bash
go test ./...        # unit tests against gzip'd fixtures + httptest failover
go test -race ./...  # concurrency check
```

Fixtures in `testdata/` are captured from real gateway responses (new
envelope format) plus legacy-shape fixtures for the old API.

## Project layout

```
cmd/server/        main: flags, mirror list, probe loop
internal/model/    normalized data model (Sport, League, Event, Market, Outcome)
internal/xbet/     raw structs, normalizer, market dictionary, mirror pool, client
internal/cache/    in-memory TTL cache
internal/api/      REST handlers (stdlib net/http)
testdata/          fixture responses for tests
```

## Legal note

1xbet's ToS prohibit automated access; this project is for personal/educational
use. Rate-limit politely — the cache throttles repeat hits and the client
does not retry aggressively.
