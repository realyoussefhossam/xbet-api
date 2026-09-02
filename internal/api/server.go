// Package api exposes the normalized 1xbet feed as a REST API.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"xbet-api/internal/cache"
	"xbet-api/internal/locks"
	"xbet-api/internal/model"
	"xbet-api/internal/xbet"
)

// errInvalid marks a request-parameter error: the response is already
// written, callers must return without double-writing.
var errInvalid = errors.New("invalid parameter")

// Fetcher abstracts the 1xbet client so handlers are testable.
type Fetcher interface {
	GetChamps(ctx context.Context, sportID int) ([]model.League, error)
	GetEvents(ctx context.Context, sportID int, p xbet.EventsParams) ([]model.Event, error)
	GetAllEvents(ctx context.Context, sportID int, p xbet.EventsParams, maxDays int) ([]model.Event, error)
	GetLiveEvents(ctx context.Context, sportID, count int) ([]model.Event, error)
	GetGame(ctx context.Context, eventID int64) (model.EventDetail, error)
	GetLiveGame(ctx context.Context, gameID int64) (model.EventDetail, error)
	GetResultSports(ctx context.Context, from, to int64) ([]model.ResultSport, error)
	GetResultChamps(ctx context.Context, sportIDs []int, from, to int64) ([]model.ResultChamp, error)
	GetResultGames(ctx context.Context, champID int, from, to int64) ([]model.ResultGame, error)
	GetRulesMenu(ctx context.Context) ([]model.RuleChapter, error)
	GetRuleChapter(ctx context.Context, chapterID int) (model.RuleChapter, error)
	GetZoneChamps(ctx context.Context, sportIDs []int, from, to int64) ([]model.ResultChamp, error)
	GetZoneGames(ctx context.Context, champIDs []int, from, to int64) ([]model.ZoneGame, error)
	GetZoneGame(ctx context.Context, gameID int64, from, to int64) ([]model.ZoneEvent, error)
	Raw(ctx context.Context, path string, q url.Values) ([]byte, error)
}

// Server is the REST API server.
type Server struct {
	fetcher   Fetcher
	champsTTL time.Duration
	eventsTTL time.Duration
	champs    *cache.Cache
	events    *cache.Cache
	games     *cache.Cache
	rules     *cache.Cache
	sports    []model.Sport
	watcher   *locks.Watcher
}

// Options configures the API server.
type Options struct {
	Fetcher   Fetcher
	Sports    []model.Sport
	Watcher   *locks.Watcher // optional lock-event watcher
	ChampsTTL time.Duration
	EventsTTL time.Duration
	GamesTTL  time.Duration
}

// New creates a Server.
func New(opts Options) *Server {
	if opts.ChampsTTL <= 0 {
		opts.ChampsTTL = 60 * time.Second
	}
	if opts.EventsTTL <= 0 {
		opts.EventsTTL = 15 * time.Second
	}
	if opts.GamesTTL <= 0 {
		opts.GamesTTL = 15 * time.Second
	}
	s := &Server{
		fetcher:   opts.Fetcher,
		champsTTL: opts.ChampsTTL,
		eventsTTL: opts.EventsTTL,
		champs:    cache.New(opts.ChampsTTL),
		events:    cache.New(opts.EventsTTL),
		games:     cache.New(opts.GamesTTL),
		rules:     cache.New(24 * time.Hour), // rules are static
		sports:    opts.Sports,
		watcher:   opts.Watcher,
	}
	return s
}

// Handler returns the root http.Handler with all routes mounted.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /sports", s.handleSports)
	mux.HandleFunc("GET /sports/{sport}/leagues", s.handleLeagues)
	mux.HandleFunc("GET /sports/{sport}/events", s.handleEvents)
	mux.HandleFunc("GET /live/events", s.handleLiveAll)
	mux.HandleFunc("GET /live/lock-events", s.handleLockEvents)
	mux.HandleFunc("GET /live/lock-events/recent", s.handleLockRecent)
	mux.HandleFunc("GET /events/{id}/markets", s.handleMarkets)
	mux.HandleFunc("GET /events/{id}/subgames", s.handleSubGames)
	mux.HandleFunc("GET /events/{id}/odds", s.handleOdds)
	mux.HandleFunc("GET /results/sports", s.handleResultSports)
	mux.HandleFunc("GET /results/champs", s.handleResultChamps)
	mux.HandleFunc("GET /results/games", s.handleResultGames)
	mux.HandleFunc("GET /results/live", s.handleResultLive)
	mux.HandleFunc("GET /results/zone/champs", s.handleZoneChamps)
	mux.HandleFunc("GET /results/zone/games", s.handleZoneGames)
	mux.HandleFunc("GET /results/zone/game", s.handleZoneGame)
	mux.HandleFunc("GET /rules", s.handleRulesMenu)
	mux.HandleFunc("GET /rules/{id}", s.handleRuleChapter)
	mux.HandleFunc("GET /debug/raw", s.handleRaw)
	return logMiddleware(mux)
}

// ---- handlers ----

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleSports(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.sports)
}

func (s *Server) handleLeagues(w http.ResponseWriter, r *http.Request) {
	sportID, err := pathInt(r, "sport")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	v, err := s.champs.GetOrLoad(fmt.Sprintf("champs:%d", sportID), func() (any, error) {
		return s.fetcher.GetChamps(r.Context(), sportID)
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Errorf("upstream: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	sportID, err := pathInt(r, "sport")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	q := r.URL.Query()
	params := xbet.EventsParams{
		Champs:        q.Get("league"),
		ExcludeChamps: q.Get("exclude_league"),
	}
	if c := q.Get("count"); c != "" {
		n, err := strconv.Atoi(c)
		if err != nil || n <= 0 {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid count: %q", c))
			return
		}
		params.Count = n
	}
	if f := q.Get("from"); f != "" {
		n, err := strconv.ParseInt(f, 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid from: %q (unix seconds)", f))
			return
		}
		params.From = n
	}
	if t := q.Get("to"); t != "" {
		n, err := strconv.ParseInt(t, 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid to: %q (unix seconds)", t))
			return
		}
		params.To = n
	}
	switch q.Get("status") {
	case "", "prematch":
	case "live":
		params.Live = true
	case "all":
		// no tf filter
	default:
		writeErr(w, http.StatusBadRequest, fmt.Errorf("status must be prematch|live|all"))
		return
	}
	all := q.Get("all") == "true"
	live := params.Live

	key := fmt.Sprintf("events:%d:%s:%s:%d:%t:%t:%t:%d:%d", sportID, params.Champs, params.ExcludeChamps, params.Count, live, all, params.Live, params.From, params.To)
	v, err := s.events.GetOrLoad(key, func() (any, error) {
		if live {
			return s.fetcher.GetLiveEvents(r.Context(), sportID, params.Count)
		}
		if all {
			return s.fetcher.GetAllEvents(r.Context(), sportID, params, 180)
		}
		return s.fetcher.GetEvents(r.Context(), sportID, params)
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Errorf("upstream: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleResultSports lists sports with results in the (default 24h) window.
func (s *Server) handleResultSports(w http.ResponseWriter, r *http.Request) {
	from, to := resultWindow(r)
	v, err := s.events.GetOrLoad(fmt.Sprintf("results:sports:%d:%d", from, to), func() (any, error) {
		return s.fetcher.GetResultSports(r.Context(), from, to)
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Errorf("upstream: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleResultChamps lists champs with finished games.
func (s *Server) handleResultChamps(w http.ResponseWriter, r *http.Request) {
	from, to := resultWindow(r)
	sportIDs, err := parseIDList(r.URL.Query().Get("sport"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	key := fmt.Sprintf("results:champs:%v:%d:%d", sportIDs, from, to)
	v, err := s.events.GetOrLoad(key, func() (any, error) {
		return s.fetcher.GetResultChamps(r.Context(), sportIDs, from, to)
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Errorf("upstream: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleResultGames returns finished games with final results for a champ.
func (s *Server) handleResultGames(w http.ResponseWriter, r *http.Request) {
	from, to := resultWindow(r)
	champID, err := strconv.Atoi(r.URL.Query().Get("champ"))
	if err != nil || champID <= 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("champ is required (from /results/champs)"))
		return
	}
	key := fmt.Sprintf("results:games:%d:%d:%d", champID, from, to)
	v, err := s.events.GetOrLoad(key, func() (any, error) {
		return s.fetcher.GetResultGames(r.Context(), champID, from, to)
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Errorf("upstream: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// resultWindow parses ?from/?to or returns the standard 24h window.
func resultWindow(r *http.Request) (int64, int64) {
	f := r.URL.Query().Get("from")
	if f != "" {
		if n, err := strconv.ParseInt(f, 10, 64); err == nil && n > 0 {
			to, _ := strconv.ParseInt(r.URL.Query().Get("to"), 10, 64)
			if to > n {
				return n, to
			}
		}
	}
	return xbet.ResultWindow(time.Now())
}

// parseIDList parses "1,9,189" into ints.
func parseIDList(s string) ([]int, error) {
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return nil, fmt.Errorf("invalid id %q", p)
		}
		out = append(out, n)
	}
	return out, nil
}

// handleResultLive returns in-play games with scores (the results Live tab).
func (s *Server) handleResultLive(w http.ResponseWriter, r *http.Request) {
	v, err := s.liveEvents(w, r)
	if err != nil {
		if errors.Is(err, errInvalid) {
			return
		}
		writeErr(w, http.StatusBadGateway, fmt.Errorf("upstream: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// liveEvents serves /live/events and /results/live: the live feed, with
// optional ?sport=N filtering and ?count=N sizing. Cache is per sport.
func (s *Server) liveEvents(w http.ResponseWriter, r *http.Request) (any, error) {
	sportID := 0
	if v := r.URL.Query().Get("sport"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid sport: %q", v))
			return nil, errInvalid
		}
		sportID = n
	}
	count := 40
	if c := r.URL.Query().Get("count"); c != "" {
		n, err := strconv.Atoi(c)
		if err != nil || n <= 0 {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid count: %q", c))
			return nil, errInvalid
		}
		count = n
	}
	return s.events.GetOrLoad(fmt.Sprintf("live:%d:%d", sportID, count), func() (any, error) {
		return s.fetcher.GetLiveEvents(r.Context(), sportID, count)
	})
}

// handleZoneChamps lists champs with zone (detailed stats) games.
func (s *Server) handleZoneChamps(w http.ResponseWriter, r *http.Request) {
	from, to := resultWindow(r)
	sportIDs, err := parseIDList(r.URL.Query().Get("sport"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	key := fmt.Sprintf("zone:champs:%v:%d:%d", sportIDs, from, to)
	v, err := s.events.GetOrLoad(key, func() (any, error) {
		return s.fetcher.GetZoneChamps(r.Context(), sportIDs, from, to)
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Errorf("upstream: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleZoneGames lists finished games with zone stats.
func (s *Server) handleZoneGames(w http.ResponseWriter, r *http.Request) {
	from, to := resultWindow(r)
	champIDs, err := parseIDList(r.URL.Query().Get("champ"))
	if err != nil || len(champIDs) == 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("champ is required (comma-separated, from /results/zone/champs)"))
		return
	}
	key := fmt.Sprintf("zone:games:%v:%d:%d", champIDs, from, to)
	v, err := s.events.GetOrLoad(key, func() (any, error) {
		return s.fetcher.GetZoneGames(r.Context(), champIDs, from, to)
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Errorf("upstream: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleZoneGame returns a finished game's minute-by-minute timeline.
func (s *Server) handleZoneGame(w http.ResponseWriter, r *http.Request) {
	from, to := resultWindow(r)
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("id is required (game id from /results/zone/games)"))
		return
	}
	key := fmt.Sprintf("zone:game:%d:%d:%d", id, from, to)
	v, err := s.events.GetOrLoad(key, func() (any, error) {
		return s.fetcher.GetZoneGame(r.Context(), id, from, to)
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Errorf("upstream: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleRulesMenu returns the official rules chapter menu (cached 24h).
func (s *Server) handleRulesMenu(w http.ResponseWriter, r *http.Request) {
	v, err := s.rules.GetOrLoad("rules:menu", func() (any, error) {
		return s.fetcher.GetRulesMenu(r.Context())
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Errorf("upstream: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleRuleChapter returns one rules chapter (cached 24h).
func (s *Server) handleRuleChapter(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	v, err := s.rules.GetOrLoad(fmt.Sprintf("rules:%d", id), func() (any, error) {
		return s.fetcher.GetRuleChapter(r.Context(), id)
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Errorf("upstream: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleLockEvents streams lock/unlock transitions as Server-Sent Events.
// Optional repeated ?game=ID params watch full markets of specific games
// while the connection is open.
func (s *Server) handleLockEvents(w http.ResponseWriter, r *http.Request) {
	if s.watcher == nil {
		writeErr(w, http.StatusServiceUnavailable, fmt.Errorf("lock watcher not enabled"))
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("streaming unsupported"))
		return
	}

	// watch requested games for the lifetime of this connection
	var watched []int64
	for _, g := range r.URL.Query()["game"] {
		id, err := strconv.ParseInt(g, 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid game: %q", g))
			return
		}
		watched = append(watched, id)
		s.watcher.Watch(id)
	}
	defer func() {
		for _, id := range watched {
			s.watcher.Unwatch(id)
		}
	}()

	ch, cancel := s.watcher.Subscribe()
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// replay recent history so a fresh consumer sees the current state
	for _, ev := range s.watcher.SortRecent() {
		writeLockSSE(w, ev)
	}
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-ch:
			writeLockSSE(w, ev)
			flusher.Flush()
		}
	}
}

// handleLockRecent returns recent lock events as JSON (newest first).
func (s *Server) handleLockRecent(w http.ResponseWriter, r *http.Request) {
	if s.watcher == nil {
		writeErr(w, http.StatusServiceUnavailable, fmt.Errorf("lock watcher not enabled"))
		return
	}
	writeJSON(w, http.StatusOK, s.watcher.SortRecent())
}

func writeLockSSE(w http.ResponseWriter, ev locks.Event) {
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	event := "unlock"
	if ev.Locked {
		event = "lock"
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
}

// handleLiveAll returns currently in-play events across all sports.
// ?sport=N filters to one sport; ?count=N caps the feed size.
func (s *Server) handleLiveAll(w http.ResponseWriter, r *http.Request) {
	v, err := s.liveEvents(w, r)
	if err != nil {
		if errors.Is(err, errInvalid) {
			return
		}
		writeErr(w, http.StatusBadGateway, fmt.Errorf("upstream: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleSubGames returns the attached sub-games of an event (e.g. "Special
// bets", "Knockdowns"). Each sub-game's markets are fetchable via
// /events/{subgame-id}/markets.
func (s *Server) handleSubGames(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	detail, err := s.game(r, id)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, detail.SubGames)
}

func (s *Server) handleMarkets(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	detail, err := s.game(r, id)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, detail.Markets)
}

func (s *Server) handleOdds(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	detail, err := s.game(r, id)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, detail.MainOdds)
}

func (s *Server) game(r *http.Request, id int64) (model.EventDetail, error) {
	v, err := s.games.GetOrLoad(fmt.Sprintf("game:%d", id), func() (any, error) {
		detail, err := s.fetcher.GetGame(r.Context(), id)
		if err != nil && isNotFound(err) {
			// in-play game: the line endpoint doesn't know it; try the live feed
			return s.fetcher.GetLiveGame(r.Context(), id)
		}
		return detail, err
	})
	if err != nil {
		return model.EventDetail{}, fmt.Errorf("upstream: %w", err)
	}
	return v.(model.EventDetail), nil
}

// isNotFound reports whether an upstream error means the game isn't in the
// line feed (so the live feed should be tried).
func isNotFound(err error) bool {
	s := err.Error()
	return strings.Contains(s, "Game is not found") || strings.Contains(s, "not found in Sports")
}

// handleRaw passes through a LineFeed call untouched — for verifying
// field mappings against live data. Usage: /debug/raw?path=GetChampsZip&sport=1
func (s *Server) handleRaw(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("missing path param (e.g. GetChampsZip|Get1x2_VZip|GetGameZip)"))
		return
	}
	ep := path
	if !strings.HasPrefix(ep, "/") {
		ep = "/LineFeed/" + ep
	}
	q := r.URL.Query()
	q.Del("path")
	body, err := s.fetcher.Raw(r.Context(), ep, q)
	if err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Errorf("upstream: %w", err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

// ---- helpers ----

func pathInt(r *http.Request, name string) (int, error) {
	n, err := strconv.Atoi(r.PathValue(name))
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %q", name, r.PathValue(name))
	}
	return n, nil
}

func pathInt64(r *http.Request, name string) (int64, error) {
	n, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %q", name, r.PathValue(name))
	}
	return n, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json: %v", err)
	}
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}
