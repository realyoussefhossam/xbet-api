// Package api exposes the normalized 1xbet feed as a REST API.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"xbet-api/internal/cache"
	"xbet-api/internal/model"
	"xbet-api/internal/xbet"
)

// Fetcher abstracts the 1xbet client so handlers are testable.
type Fetcher interface {
	GetChamps(ctx context.Context, sportID int) ([]model.League, error)
	GetEvents(ctx context.Context, sportID int, p xbet.EventsParams) ([]model.Event, error)
	GetGame(ctx context.Context, eventID int64) (model.EventDetail, error)
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
	sports    []model.Sport
}

// Options configures the API server.
type Options struct {
	Fetcher   Fetcher
	Sports    []model.Sport
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
		sports:    opts.Sports,
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
	mux.HandleFunc("GET /events/{id}/markets", s.handleMarkets)
	mux.HandleFunc("GET /events/{id}/odds", s.handleOdds)
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
		Champs: q.Get("league"),
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

	key := fmt.Sprintf("events:%d:%s:%d:%t:%d:%d", sportID, params.Champs, params.Count, params.Live, params.From, params.To)
	v, err := s.events.GetOrLoad(key, func() (any, error) {
		return s.fetcher.GetEvents(r.Context(), sportID, params)
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Errorf("upstream: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, v)
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
		return s.fetcher.GetGame(r.Context(), id)
	})
	if err != nil {
		return model.EventDetail{}, fmt.Errorf("upstream: %w", err)
	}
	return v.(model.EventDetail), nil
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
