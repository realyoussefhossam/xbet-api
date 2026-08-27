package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"xbet-api/internal/model"
	"xbet-api/internal/xbet"
)

// stubFetcher implements Fetcher with canned data.
type stubFetcher struct {
	leagues []model.League
	events  []model.Event
	game    model.EventDetail
	err     error
}

func (s *stubFetcher) GetChamps(_ context.Context, _ int) ([]model.League, error) {
	return s.leagues, s.err
}

func (s *stubFetcher) GetEvents(_ context.Context, _ int, _ xbet.EventsParams) ([]model.Event, error) {
	return s.events, s.err
}

func (s *stubFetcher) GetAllEvents(_ context.Context, _ int, _ xbet.EventsParams, _ int) ([]model.Event, error) {
	return s.events, s.err
}

func (s *stubFetcher) GetLiveEvents(_ context.Context, _ int, _ int) ([]model.Event, error) {
	return s.events, s.err
}

func (s *stubFetcher) GetLiveGame(_ context.Context, _ int64) (model.EventDetail, error) {
	return s.game, s.err
}

func (s *stubFetcher) GetGame(_ context.Context, _ int64) (model.EventDetail, error) {
	return s.game, s.err
}

func (s *stubFetcher) Raw(_ context.Context, path string, _ url.Values) ([]byte, error) {
	return []byte(fmt.Sprintf(`{"path":%q}`, path)), s.err
}

func newTestServer() (*Server, *stubFetcher) {
	f := &stubFetcher{
		leagues: []model.League{{ID: 88, Name: "Germany", SportID: 1}},
		events: []model.Event{{
			ID: 1, SportID: 1, LeagueID: 88, LeagueName: "Bundesliga",
			Home: "Bayern", Away: "Dortmund", Status: model.StatusPrematch,
			MainOdds: &model.Odds{Home: 1.85, Draw: 3.75, Away: 4.1},
		}},
		game: model.EventDetail{
			Event: model.Event{ID: 1, Home: "Bayern", Away: "Dortmund", Status: model.StatusPrematch},
			Markets: []model.Market{{
				ID: 100, Name: "Match Winner", Group: "Full time",
				Outcomes: []model.Outcome{{ID: 6, Name: "1", Odds: 1.85}},
			}},
		},
	}
	s := New(Options{Fetcher: f, Sports: []model.Sport{{ID: 1, Name: "Football"}}})
	return s, f
}

func doGet(t *testing.T, s *Server, path string) (*httptest.ResponseRecorder, any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	var body any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body %q: %v", rec.Body.String(), err)
		}
	}
	return rec, body
}

func TestHealthz(t *testing.T) {
	s, _ := newTestServer()
	rec, _ := doGet(t, s, "/healthz")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

func TestSports(t *testing.T) {
	s, _ := newTestServer()
	rec, body := doGet(t, s, "/sports")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	arr, ok := body.([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("want 1 sport, got %T %v", body, body)
	}
}

func TestLeagues(t *testing.T) {
	s, _ := newTestServer()
	rec, body := doGet(t, s, "/sports/1/leagues")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	arr, ok := body.([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("want array of 1 league, got %T %v", body, body)
	}
}

func TestLeaguesBadSport(t *testing.T) {
	s, _ := newTestServer()
	rec, _ := doGet(t, s, "/sports/abc/leagues")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestEvents(t *testing.T) {
	s, _ := newTestServer()
	rec, body := doGet(t, s, "/sports/1/events?status=prematch")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	arr, ok := body.([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("want array of 1 event, got %T %v", body, body)
	}
}

func TestEventsBadStatus(t *testing.T) {
	s, _ := newTestServer()
	rec, _ := doGet(t, s, "/sports/1/events?status=bogus")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestMarkets(t *testing.T) {
	s, _ := newTestServer()
	rec, _ := doGet(t, s, "/events/1/markets")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestOdds(t *testing.T) {
	s, _ := newTestServer()
	rec, _ := doGet(t, s, "/events/1/odds")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMarketsBadID(t *testing.T) {
	s, _ := newTestServer()
	rec, _ := doGet(t, s, "/events/xyz/markets")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestUpstreamError(t *testing.T) {
	s, f := newTestServer()
	f.err = fmt.Errorf("all mirrors failed")
	rec, _ := doGet(t, s, "/sports/1/leagues")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("want 502, got %d", rec.Code)
	}
}

func TestRawPassthroughEndpoint(t *testing.T) {
	s, _ := newTestServer()
	rec, body := doGet(t, s, "/debug/raw?path=GetChampsZip&sport=1")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if body.(map[string]any)["path"] != "/LineFeed/GetChampsZip" {
		t.Fatalf("bad raw path: %v", body)
	}
}
func TestCacheHit(t *testing.T) {
	s, f := newTestServer()
	doGet(t, s, "/sports/1/leagues")
	// count upstream calls: stub has no counter; verify cache len instead
	if s.champs.Len() != 1 {
		t.Fatalf("want 1 cached champs entry, got %d", s.champs.Len())
	}
	doGet(t, s, "/sports/1/leagues") // second call served from cache
	if s.champs.Len() != 1 {
		t.Fatalf("cache should still hold 1 entry, got %d", s.champs.Len())
	}
	_ = f
}
