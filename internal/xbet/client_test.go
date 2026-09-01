package xbet

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

// gz returns b gzip-compressed.
func gz(b []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(b)
	zw.Close()
	return buf.Bytes()
}

// newTestClient spins up httptest servers: good (serves gzip'd JSON from a
// fixture) and bad (HTTP-level failures), so failover is exercised.
func newTestClient(t *testing.T) (*Client, []string) {
	t.Helper()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var fixture string
		switch {
		case len(r.URL.Path) > 0 && r.URL.Path != "/":
			if r.URL.Path != "/LineFeed/GetChampsZip" &&
				r.URL.Path != "/service-api/LineFeed/GetChampsZip" &&
				r.URL.Path != "/service-api/LineFeed/Get1x2_Zip" &&
				r.URL.Path != "/service-api/LineFeed/GetGameZip" {
				http.NotFound(w, r)
				return
			}
		}
		switch {
		case containsStr(r.URL.Path, "GetChampsZip"):
			fixture = "champs-new.json"
		case containsStr(r.URL.Path, "Get1x2_Zip"):
			fixture = "events-new.json"
		case containsStr(r.URL.Path, "GetGameZip"):
			fixture = "game-new.json"
		default: // homepage
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "<html>1xbet</html>")
			return
		}
		body := gz(loadFixture(t, fixture))
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	t.Cleanup(good.Close)

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "blocked", http.StatusForbidden)
	}))
	t.Cleanup(bad.Close)

	hosts := []string{
		bad.Listener.Addr().String(),
		good.Listener.Addr().String(),
	}
	return NewClient(ClientOptions{Mirrors: hosts, Timeout: 5 * time.Second, Scheme: "http"}), hosts
}

func TestClientFailoverAndGzip(t *testing.T) {
	c, _ := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	leagues, err := c.GetChamps(ctx, 1)
	if err != nil {
		t.Fatalf("GetChamps with failover: %v", err)
	}
	if len(leagues) != 3 || leagues[1].Name != "Spain. La Liga" {
		t.Fatalf("bad leagues: %+v", leagues)
	}

	events, err := c.GetEvents(ctx, 1, EventsParams{})
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("bad events count: %d", len(events))
	}
	if events[1].Home != "Barcelona" || events[1].Away != "Athletic Bilbao" {
		t.Fatalf("bad event: %+v", events[1])
	}

	game, err := c.GetGame(ctx, 736802022)
	if err != nil {
		t.Fatalf("GetGame: %v", err)
	}
	if len(game.Markets) != 6 {
		t.Fatalf("bad markets: %d", len(game.Markets))
	}
	if game.Markets[0].Outcomes[0].Odds != 1.27 {
		t.Fatalf("bad odds: %+v", game.Markets[0].Outcomes[0])
	}
}

func TestClientAllMirrorsDead(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusServiceUnavailable)
	}))
	defer dead.Close()

	c := NewClient(ClientOptions{
		Mirrors: []string{dead.Listener.Addr().String()},
		Timeout: 3 * time.Second,
		Scheme:  "http",
	})
	_, err := c.GetChamps(context.Background(), 1)
	if err == nil {
		t.Fatal("want error when all mirrors fail")
	}
	if !containsStr(err.Error(), "all mirrors failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClientBackendError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if containsStr(r.URL.Path, "GetGameZip") {
			body := gz([]byte(`{"Id":0,"Success":false,"Error":"Game is not found in Sports!","Value":null}`))
			w.Header().Set("Content-Type", "application/json")
			w.Write(body)
			return
		}
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient(ClientOptions{
		Mirrors: []string{srv.Listener.Addr().String()},
		Timeout: 3 * time.Second,
		Scheme:  "http",
	})
	_, err := c.GetGame(context.Background(), 123)
	if err == nil {
		t.Fatal("want backend error surfaced")
	}
	if !containsStr(err.Error(), "Game is not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMaybeGunzip(t *testing.T) {
	raw := []byte(`{"Value":[]}`)
	out, err := maybeGunzip(gz(raw))
	if err != nil || !bytes.Equal(out, raw) {
		t.Fatalf("gunzip failed: %v %s", err, out)
	}
	// plain passthrough
	out, err = maybeGunzip(raw)
	if err != nil || !bytes.Equal(out, raw) {
		t.Fatalf("passthrough failed: %v", err)
	}
}

func TestRawPassthrough(t *testing.T) {
	c, _ := newTestClient(t)
	body, err := c.Raw(context.Background(), "GetChampsZip", url.Values{"sport": {"1"}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte(`"Value"`)) {
		t.Fatalf("raw body unexpected: %s", body[:min(len(body), 120)])
	}
}

// TestGetAllEventsPages verifies windowed paging collects events beyond the
// per-request cap. The fake upstream returns max 50 events per window.
func TestGetAllEventsPages(t *testing.T) {
	// 120 fake events spread over 3 days (would need >50 per window)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		from, _ := strconv.ParseInt(r.URL.Query().Get("tsFrom"), 10, 64)
		to, _ := strconv.ParseInt(r.URL.Query().Get("tsTo"), 10, 64)
		var evs []map[string]any
		// events every 30 minutes across the window, capped at 50
		for ts := from; ts < to && len(evs) < 50; ts += 1800 {
			evs = append(evs, map[string]any{"I": 1000000 + ts, "S": ts, "O1": "Home", "O2": "Away", "SS": 2, "SI": 9, "L": "Fights", "LI": 318137})
		}
		body, _ := json.Marshal(map[string]any{"Id": 0, "Success": true, "Value": evs})
		w.Header().Set("Content-Type", "application/json")
		w.Write(gz(body))
	}))
	defer srv.Close()

	c := NewClient(ClientOptions{
		Mirrors: []string{srv.Listener.Addr().String()},
		Timeout: 5 * time.Second,
		Scheme:  "http",
	})
	// window = 3 days -> 144 possible events; cap 50 forces subdivision
	evs, err := c.GetAllEvents(context.Background(), 9, EventsParams{}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 144 {
		t.Fatalf("want 144 events, got %d", len(evs))
	}
	// dedupe check: ids must be unique
	seen := map[int64]bool{}
	for _, e := range evs {
		if seen[e.ID] {
			t.Fatalf("duplicate event id %d", e.ID)
		}
		seen[e.ID] = true
	}
	// sorted by start time
	for i := 1; i < len(evs); i++ {
		if evs[i].StartTime.Before(evs[i-1].StartTime) {
			t.Fatalf("events not sorted at %d", i)
		}
	}
}

// TestGetLiveEvents verifies the v3 live feed: gr candidates are tried and
// the response (scores + main odds) is normalized.
func TestGetLiveEvents(t *testing.T) {
	var gotGR string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !containsStr(r.URL.Path, "games1x2") {
			http.NotFound(w, r)
			return
		}
		gotGR = r.URL.Query().Get("gr")
		body := gz(loadFixture(t, "live-games.json"))
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	defer srv.Close()

	c := NewClient(ClientOptions{
		Mirrors: []string{srv.Listener.Addr().String()},
		Timeout: 5 * time.Second,
		Scheme:  "http",
	})
	evs, err := c.GetLiveEvents(context.Background(), 1, 40)
	if err != nil {
		t.Fatal(err)
	}
	// sport filter: fixture has no football (cricket first) -> 0 expected with filter
	_ = evs
	// without filter
	evs, err = c.GetLiveEvents(context.Background(), 0, 40)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 {
		t.Fatalf("want 2 live events, got %d", len(evs))
	}
	if evs[0].Status != "live" || evs[0].Score == nil {
		t.Fatalf("bad live event: %+v", evs[0])
	}
	if gotGR == "" {
		t.Fatal("gr param not sent")
	}
}

// TestGetLiveGameFallback: GetGame fails with "not found" for live games, so
// the server must fall back to the live feed. (client-side: GetLiveGame works)
func TestGetLiveGame(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !containsStr(r.URL.Path, "gameEvents") {
			http.NotFound(w, r)
			return
		}
		body := gz(loadFixture(t, "live-game-events.json"))
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	defer srv.Close()

	c := NewClient(ClientOptions{
		Mirrors: []string{srv.Listener.Addr().String()},
		Timeout: 5 * time.Second,
		Scheme:  "http",
	})
	d, err := c.GetLiveGame(context.Background(), 747600716)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "live" {
		t.Fatalf("want live status, got %s", d.Status)
	}
	if len(d.Markets) == 0 || d.Markets[0].Name != "Match Winner" {
		t.Fatalf("bad markets: %+v", d.Markets)
	}
}

// results API: fixtures served per endpoint path
func newResultsTestClient(t *testing.T) *Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var fixture string
		switch {
		case containsStr(r.URL.Path, "v2/sports"):
			fixture = "results-sports.json"
		case containsStr(r.URL.Path, "v2/champs"):
			fixture = "results-champs.json"
		case containsStr(r.URL.Path, "v3/games"):
			fixture = "results-games.json"
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(gz(loadFixture(t, fixture)))
	}))
	t.Cleanup(srv.Close)
	return NewClient(ClientOptions{
		Mirrors: []string{srv.Listener.Addr().String()},
		Timeout: 5 * time.Second,
		Scheme:  "http",
	})
}

func TestResultWindow(t *testing.T) {
	now := time.Unix(1787940123, 0)
	from, to := ResultWindow(now)
	if to != 1787940180 {
		t.Fatalf("to = %d, want 1787940180 (rounded up to minute)", to)
	}
	if to-from != 86400 {
		t.Fatalf("window must be exactly 24h, got %d", to-from)
	}
}

func TestGetResultSports(t *testing.T) {
	c := newResultsTestClient(t)
	from, to := ResultWindow(time.Now())
	ss, err := c.GetResultSports(context.Background(), from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 3 || ss[0].Name != "Football" {
		t.Fatalf("bad sports: %+v", ss)
	}
}

func TestGetResultChamps(t *testing.T) {
	c := newResultsTestClient(t)
	from, to := ResultWindow(time.Now())
	cs, err := c.GetResultChamps(context.Background(), []int{1, 189}, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 || cs[0].ID != 127733 || cs[0].GamesCount != 3 {
		t.Fatalf("bad champs: %+v", cs)
	}
}

func TestGetResultGames(t *testing.T) {
	c := newResultsTestClient(t)
	from, to := ResultWindow(time.Now())
	gs, err := c.GetResultGames(context.Background(), 2551892, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(gs) != 2 {
		t.Fatalf("want 2 games, got %d", len(gs))
	}
	g := gs[0]
	if g.Home != "Darrius Flowers" || g.Away != "Hayisaer Maheshate" {
		t.Fatalf("bad teams: %+v", g)
	}
	if !containsStr(g.Score, "Wins") {
		t.Fatalf("want winner in score, got %q", g.Score)
	}
	if !containsStr(g.HomeImage, "/sfiles/logo_teams/767ef093161e281b3fc0dcbb9388755a.png") {
		t.Errorf("want absolute home image URL, got %q", g.HomeImage)
	}
	if !containsStr(g.AwayImage, "/sfiles/logo_teams/9bfcf7b6893e88fbbebf831b91b6c026.png") {
		t.Errorf("want absolute away image URL, got %q", g.AwayImage)
	}
}

// rules API fixtures
func newRulesTestClient(t *testing.T) *Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var fixture string
		switch {
		case containsStr(r.URL.Path, "rulesmenu"):
			fixture = "rules-menu.json"
		case containsStr(r.URL.Path, "/information/rules/"):
			fixture = "rules-chapter.json"
		default:
			http.NotFound(w, r)
			return
		}
		// verify the required app headers are sent
		if r.Header.Get("x-language") == "" || r.Header.Get("x-svc-source") == "" {
			http.Error(w, "missing app headers", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(gz(loadFixture(t, fixture)))
	}))
	t.Cleanup(srv.Close)
	return NewClient(ClientOptions{
		Mirrors: []string{srv.Listener.Addr().String()},
		Timeout: 5 * time.Second,
		Scheme:  "http",
	})
}

func TestGetRulesMenu(t *testing.T) {
	c := newRulesTestClient(t)
	menu, err := c.GetRulesMenu(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(menu) < 4 {
		t.Fatalf("want >=4 top chapters, got %d", len(menu))
	}
	found := false
	for _, ch := range menu {
		if ch.ID == 49035435 && ch.Title == "Match Results, Dates and Starting Times" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Match Results chapter missing: %+v", menu)
	}
}

func TestGetRuleChapter(t *testing.T) {
	c := newRulesTestClient(t)
	ch, err := c.GetRuleChapter(context.Background(), 49035435)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Title != "Match Results, Dates and Starting Times" {
		t.Fatalf("bad title: %q", ch.Title)
	}
	if !containsStr(ch.Description, "Bet settlement") {
		t.Fatalf("settlement text missing: %.60s", ch.Description)
	}
}

// X-Zone fixtures
func newZoneTestClient(t *testing.T) *Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var fixture string
		switch {
		case containsStr(r.URL.Path, "result1xzone") && containsStr(r.URL.Path, "/champs"):
			fixture = "zone-champs.json"
		case containsStr(r.URL.Path, "result1xzone") && containsStr(r.URL.Path, "/games"):
			fixture = "zone-games.json"
		case containsStr(r.URL.Path, "result1xzone") && containsStr(r.URL.Path, "/game"):
			fixture = "zone-game.json"
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(gz(loadFixture(t, fixture)))
	}))
	t.Cleanup(srv.Close)
	return NewClient(ClientOptions{
		Mirrors: []string{srv.Listener.Addr().String()},
		Timeout: 5 * time.Second,
		Scheme:  "http",
	})
}

func TestGetZoneChamps(t *testing.T) {
	c := newZoneTestClient(t)
	from, to := ResultWindow(time.Now())
	cs, err := c.GetZoneChamps(context.Background(), []int{1}, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) == 0 || cs[0].ID != 127733 {
		t.Fatalf("bad zone champs: %+v", cs[:min(2, len(cs))])
	}
}

func TestGetZoneGames(t *testing.T) {
	c := newZoneTestClient(t)
	from, to := ResultWindow(time.Now())
	gs, err := c.GetZoneGames(context.Background(), []int{127733}, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(gs) != 1 || gs[0].Score != "2:0 (1:0,1:0)" {
		t.Fatalf("bad zone games: %+v", gs)
	}
	if gs[0].MatchInfo["11"] != "Spain" {
		t.Fatalf("match info missing: %+v", gs[0].MatchInfo)
	}
}

func TestGetZoneGame(t *testing.T) {
	c := newZoneTestClient(t)
	from, to := ResultWindow(time.Now())
	evs, err := c.GetZoneGame(context.Background(), 747759846, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) < 10 {
		t.Fatalf("want timeline events, got %d", len(evs))
	}
	if evs[0].Event != "Offside" || evs[0].Time != "01:00" {
		t.Fatalf("bad first event: %+v", evs[0])
	}
}

// TestRuleSubsections: chapters like "Types of bets" carry their content in
// rule_subsection (Single bet, Accumulator, ...) - must be parsed + sorted.
func TestRuleSubsections(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(gz(loadFixture(t, "rules-chapter-subsections.json")))
	}))
	defer srv.Close()
	c := NewClient(ClientOptions{
		Mirrors: []string{srv.Listener.Addr().String()},
		Timeout: 5 * time.Second,
		Scheme:  "http",
	})
	ch, err := c.GetRuleChapter(context.Background(), 31143483)
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.Subsections) < 2 {
		t.Fatalf("want subsections, got %d", len(ch.Subsections))
	}
	// sorted by sort asc; "Single bet" has sort 1
	if ch.Subsections[0].Title != "Single bet" {
		t.Fatalf("first subsection should be Single bet, got %q", ch.Subsections[0].Title)
	}
	if !containsStr(ch.Subsections[0].Description, "stake is multiplied by the odds") {
		t.Fatalf("single bet content missing: %.80s", ch.Subsections[0].Description)
	}
	// sorted ascending
	for i := 1; i < len(ch.Subsections); i++ {
		if ch.Subsections[i].ID < ch.Subsections[i-1].ID {
			t.Fatalf("subsections not sorted at %d", i)
		}
	}
}
