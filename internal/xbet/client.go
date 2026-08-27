package xbet

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"xbet-api/internal/model"
)

const (
	// DefaultBasePath is the new gateway path prefix. The legacy 1xbet.com
	// API used "/LineFeed/" — set via ClientOptions.BasePath when needed.
	DefaultBasePath = "/service-api/LineFeed/"

	// Default partner/group params observed in the current frontend.
	DefaultPartner = 159
	DefaultGr      = 412
	DefaultMode    = 3
)

// live feed endpoints (v3). gr (project id) varies per domain; the v3 calls
// try the known candidates per host.
const (
	epLiveGames = "/service-api/main-live-feed/v3/games1x2"
	epLiveGame  = "/service-api/main-live-feed/v3/gameEvents"
)

// liveGrCandidates: project ids seen across domains (1557 = lite, 412 = ng).
var liveGrCandidates = []int{1557, 412}

var defaultUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

// kv is one ordered query parameter.
type kv struct {
	k, v string
}

// orderedQuery preserves parameter order — the 1xbet edge gateway returns
// 406 for GetChampsZip unless "sport" leads and "lng" follows.
type orderedQuery []kv

func (q orderedQuery) encode() string {
	var b strings.Builder
	for i, p := range q {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(url.QueryEscape(p.k))
		b.WriteByte('=')
		b.WriteString(url.QueryEscape(p.v))
	}
	return b.String()
}

func (q orderedQuery) get(key string) string {
	for _, p := range q {
		if p.k == key {
			return p.v
		}
	}
	return ""
}

// Client is a mirror-failover client for 1xbet's LineFeed JSON API.
type Client struct {
	http     *http.Client
	pool     *MirrorPool
	scheme   string
	basePath string
	lng      string
	fcountry string
	timeout  time.Duration
	partner  int
	jarMu    sync.Mutex
	jars     map[string]http.CookieJar // per-host jars (cookies are host-scoped)
	booted   map[string]bool           // hosts we've fetched the homepage for
}

// ClientOptions configures a Client.
type ClientOptions struct {
	Mirrors  []string      // hosts, e.g. "1xbet.ng" or "ua.1xbet.com"
	Lng      string        // UI language; "en" gives UTF-8 JSON
	Timeout  time.Duration // per-request timeout
	Proxy    *url.URL      // optional proxy; empty uses HTTPS_PROXY env
	Scheme   string        // default "https"; "http" for tests
	BasePath string        // default DefaultBasePath
	Partner  int           // default DefaultPartner
	FCountry string        // frontend country id for v3 feeds; default "66"
}

// NewClient creates a Client. Use http.ProxyFromEnvironment when proxy==nil
// so HTTPS_PROXY/HTTP_PROXY env vars work out of the box.
func NewClient(opts ClientOptions) *Client {
	if opts.Scheme == "" {
		opts.Scheme = "https"
	}
	if opts.Lng == "" {
		opts.Lng = "en"
	}
	if opts.BasePath == "" {
		opts.BasePath = DefaultBasePath
	}
	if opts.Partner == 0 {
		opts.Partner = DefaultPartner
	}
	if opts.FCountry == "" {
		opts.FCountry = "66"
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 15 * time.Second
	}

	jar, _ := cookiejar.New(nil)
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     60 * time.Second,
	}
	if opts.Proxy != nil {
		transport.Proxy = http.ProxyURL(opts.Proxy)
	}

	return &Client{
		http: &http.Client{
			Timeout:   opts.Timeout,
			Transport: transport,
			Jar:       jar,
		},
		pool:     NewMirrorPool(opts.Mirrors),
		scheme:   opts.Scheme,
		basePath: opts.BasePath,
		lng:      opts.Lng,
		fcountry: opts.FCountry,
		timeout:  opts.Timeout,
		partner:  opts.Partner,
		jars:     map[string]http.CookieJar{},
		booted:   map[string]bool{},
	}
}

// GetChamps returns the list of leagues (championships) for a sport.
// NOTE: the edge gateway checks the query-string order for this endpoint:
// "sport" must be first and "lng" second, anything else returns 406.
func (c *Client) GetChamps(ctx context.Context, sportID int) ([]model.League, error) {
	q := orderedQuery{
		{"sport", fmt.Sprint(sportID)},
		{"lng", c.lng},
		{"partner", fmt.Sprint(c.partner)},
	}
	var env apiEnvelope
	if err := c.doJSON(ctx, "GetChampsZip", q, &env); err != nil {
		return nil, err
	}
	if err := envErr(env); err != nil {
		return nil, err
	}
	var champs []rawChamp
	if err := json.Unmarshal(env.Value, &champs); err != nil {
		return nil, fmt.Errorf("decode champs: %w", err)
	}
	out := make([]model.League, 0, len(champs))
	for _, ch := range champs {
		out = append(out, normalizeLeague(ch))
	}
	return out, nil
}

// EventsParams are optional filters for GetEvents.
type EventsParams struct {
	Champs string // comma-separated league ids; empty = all
	Count  int    // max events; default 50
	Live   bool   // unused on the new gateway (LINE feed); kept for API compat
	From   int64  // unix ts: earliest start (tsFrom)
	To     int64  // unix ts: latest start (tsTo)
}

// GetAllEvents returns ALL events for a sport over the next maxDays days,
// paging through time windows internally. The upstream feed caps at ~50
// events per request, so windows that hit the cap are subdivided until
// complete. Events are deduplicated by id and sorted by start time.
func (c *Client) GetAllEvents(ctx context.Context, sportID int, p EventsParams, maxDays int) ([]model.Event, error) {
	if maxDays <= 0 {
		maxDays = 180
	}
	p.Count = 50 // completeness requires the max per-request window
	now := time.Now().Unix()
	var out []model.Event
	if err := c.collectEvents(ctx, sportID, p, now, now+int64(maxDays)*86400, &out); err != nil {
		return nil, err
	}
	// dedupe by id, keep earliest occurrence
	seen := map[int64]bool{}
	dedup := out[:0]
	for _, e := range out {
		if !seen[e.ID] {
			seen[e.ID] = true
			dedup = append(dedup, e)
		}
	}
	sort.Slice(dedup, func(i, j int) bool { return dedup[i].StartTime.Before(dedup[j].StartTime) })
	return dedup, nil
}

// collectEvents fetches events in [from,to), subdividing windows that hit
// the upstream per-request cap. maxEvents guards against runaway loops.
func (c *Client) collectEvents(ctx context.Context, sportID int, p EventsParams, from, to int64, out *[]model.Event) error {
	if to-from < 3600 || len(*out) > 5000 {
		return nil
	}
	p.From, p.To = from, to
	evs, err := c.GetEvents(ctx, sportID, p)
	if err != nil {
		return err
	}
	*out = append(*out, evs...)
	// cap hit -> window may be truncated, subdivide
	if len(evs) >= 50 {
		mid := from + (to-from)/2
		if err := c.collectEvents(ctx, sportID, p, from, mid, out); err != nil {
			return err
		}
		return c.collectEvents(ctx, sportID, p, mid, to, out)
	}
	return nil
}

// GetEvents returns feed events (line/prematch) for a sport.
func (c *Client) GetEvents(ctx context.Context, sportID int, p EventsParams) ([]model.Event, error) {
	if p.Count <= 0 {
		p.Count = 50
	}
	q := orderedQuery{
		{"sports", fmt.Sprint(sportID)},
		{"lng", c.lng},
		{"partner", fmt.Sprint(c.partner)},
		{"getEmpty", "true"},
		{"gr", fmt.Sprint(DefaultGr)},
		{"mode", fmt.Sprint(DefaultMode)},
		{"count", fmt.Sprint(p.Count)},
	}
	if p.Champs != "" {
		q = append(q, kv{"champs", p.Champs})
	}
	if p.From > 0 {
		q = append(q, kv{"tsFrom", fmt.Sprint(p.From)})
	}
	if p.To > 0 {
		q = append(q, kv{"tsTo", fmt.Sprint(p.To)})
	}
	var env apiEnvelope
	if err := c.doJSON(ctx, "Get1x2_Zip", q, &env); err != nil {
		return nil, err
	}
	if err := envErr(env); err != nil {
		return nil, err
	}
	var events []rawEvent
	if err := json.Unmarshal(env.Value, &events); err != nil {
		return nil, fmt.Errorf("decode events: %w", err)
	}
	out := make([]model.Event, 0, len(events))
	for _, e := range events {
		ev := normalizeEvent(e)
		if ev.SportID == 0 {
			ev.SportID = sportID
		}
		out = append(out, ev)
	}
	return out, nil
}

// GetGame returns a full event: all markets and odds, plus attached
// sub-games (special bets, knockdowns, ...). Uses the frontend's param set
// (isSubGames=true) so sub-games come back in SG; falls back to the minimal
// call on older gateways.
func (c *Client) GetGame(ctx context.Context, eventID int64) (model.EventDetail, error) {
	q := orderedQuery{
		{"id", fmt.Sprint(eventID)},
		{"lng", c.lng},
		{"isSubGames", "true"},
		{"GroupEvents", "true"},
		{"countevents", "2000"},
		{"grMode", "4"},
		{"topGroups", ""},
		{"country", "66"},
		{"marketType", "1"},
		{"isNewBuilder", "true"},
	}
	var env apiEnvelope
	err := c.doJSON(ctx, "GetGameZip", q, &env)
	if err != nil {
		// older gateway: minimal params
		q2 := orderedQuery{
			{"id", fmt.Sprint(eventID)},
			{"lng", c.lng},
			{"partner", fmt.Sprint(c.partner)},
		}
		env = apiEnvelope{}
		err = c.doJSON(ctx, "GetGameZip", q2, &env)
	}
	if err != nil {
		return model.EventDetail{}, err
	}
	if err := envErr(env); err != nil {
		return model.EventDetail{}, err
	}

	// new flat format
	var flat rawGame
	if err := json.Unmarshal(env.Value, &flat); err != nil {
		return model.EventDetail{}, fmt.Errorf("decode game: %w", err)
	}
	if flat.I != 0 && (len(flat.E) > 0 || flat.L != "") {
		return normalizeGameFlat(flat), nil
	}

	// legacy grouped format fallback
	var legacy legacyGame
	if err := json.Unmarshal(env.Value, &legacy); err != nil {
		return model.EventDetail{}, fmt.Errorf("decode legacy game: %w", err)
	}
	return normalizeGameLegacy(legacy), nil
}

// GetLiveEvents returns currently in-play events (optionally filtered by
// sportID; 0 = all sports) with live scores and main 1X2 odds.
func (c *Client) GetLiveEvents(ctx context.Context, sportID, count int) ([]model.Event, error) {
	if count <= 0 {
		count = 40
	}
	q := orderedQuery{
		{"cfView", "3"},
		{"count", fmt.Sprint(count)},
		{"fcountry", c.fcountry},
		// gr inserted at index 3 by doV3
		{"grMode", "4"},
		{"lng", c.lng},
		{"ref", "1"},
	}
	body, err := c.doV3(ctx, epLiveGames, q, 3)
	if err != nil {
		return nil, err
	}
	var games []rawLiveGame
	if err := json.Unmarshal(body, &games); err != nil {
		return nil, fmt.Errorf("decode live games: %w", err)
	}
	out := make([]model.Event, 0, len(games))
	for _, g := range games {
		if sportID > 0 && int(g.Sport.ID) != sportID {
			continue
		}
		out = append(out, normalizeLiveGame(g))
	}
	return out, nil
}

// GetLiveGame returns all markets for an in-play game.
func (c *Client) GetLiveGame(ctx context.Context, gameID int64) (model.EventDetail, error) {
	q := orderedQuery{
		{"cfView", "3"},
		{"countEvents", "250"},
		{"fcountry", c.fcountry},
		{"gameId", fmt.Sprint(gameID)},
		// gr inserted at index 4 by doV3
		{"grMode", "4"},
		{"lng", c.lng},
		{"marketType", "1"},
		{"ref", "1"},
	}
	body, err := c.doV3(ctx, epLiveGame, q, 4)
	if err != nil {
		return model.EventDetail{}, err
	}
	var ge rawLiveGameEvents
	if err := json.Unmarshal(body, &ge); err != nil {
		return model.EventDetail{}, fmt.Errorf("decode live game: %w", err)
	}
	return normalizeLiveGameEvents(ge), nil
}

// doV3 performs a GET on a v3 endpoint, trying each mirror host with each
// known gr (project id) candidate. gr must sit at grIndex: the v3 gateway
// validates parameter order and 400s on any other arrangement.
func (c *Client) doV3(ctx context.Context, ep string, q orderedQuery, grIndex int) ([]byte, error) {
	var lastErr error
	for _, host := range c.pool.Hosts() {
		hostOK := false
		for _, gr := range liveGrCandidates {
			q2 := insertKV(q, grIndex, kv{"gr", fmt.Sprint(gr)})
			body, err := c.doHost(ctx, host, ep, q2)
			if err == nil {
				c.pool.ReportSuccess(host)
				return body, nil
			}
			lastErr = err
			var hse *httpStatusError
			if !errors.As(err, &hse) {
				hostOK = true // transport failure -> candidate for demotion
			}
		}
		if !hostOK {
			c.pool.ReportFailure(host)
		}
	}
	return nil, fmt.Errorf("all mirrors failed for %s: %w", ep, lastErr)
}

// insertKV returns a copy of q with the kv inserted at index i.
func insertKV(q orderedQuery, i int, kv kv) orderedQuery {
	out := make(orderedQuery, 0, len(q)+1)
	out = append(out, q[:i]...)
	out = append(out, kv)
	out = append(out, q[i:]...)
	return out
}

// Raw performs a passthrough call to a LineFeed endpoint and returns the
// decoded JSON as-is. Useful for verifying field mappings against live data.
func (c *Client) Raw(ctx context.Context, path string, q url.Values) ([]byte, error) {
	var oq orderedQuery
	for k, vs := range q {
		for _, v := range vs {
			oq = append(oq, kv{k, v})
		}
	}
	if oq.get("lng") == "" {
		oq = append(oq, kv{"lng", c.lng})
	}
	if oq.get("partner") == "" {
		oq = append(oq, kv{"partner", fmt.Sprint(c.partner)})
	}
	return c.do(ctx, path, oq)
}

// envErr converts a backend error envelope into an error (nil if ok).
func envErr(env apiEnvelope) error {
	if env.Success || env.ErrorCode() == 0 {
		if len(env.Error) > 0 && env.Error[0] == '"' {
			var s string
			if json.Unmarshal(env.Error, &s) == nil && s != "" {
				return fmt.Errorf("1xbet: %s", s)
			}
		}
		return nil
	}
	return fmt.Errorf("1xbet error code %d", env.ErrorCode())
}

// doJSON decodes a gzip'd JSON response into v, failing over across mirrors.
func (c *Client) doJSON(ctx context.Context, ep string, q orderedQuery, v any) error {
	body, err := c.do(ctx, ep, q)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("decode %s: %w", ep, err)
	}
	return nil
}

// httpStatusError marks an HTTP-level rejection (4xx/5xx) as opposed to a
// transport failure. Hosts are only demoted on transport failures: an HTTP
// rejection (e.g. a rate-limit blip or a wrong gr) says nothing about host
// health, and demoting on it poisons the pool during blips.
type httpStatusError struct{ status int }

func (e *httpStatusError) Error() string { return fmt.Sprintf("http %d", e.status) }

// do performs a GET across the mirror pool until one succeeds.
func (c *Client) do(ctx context.Context, ep string, q orderedQuery) ([]byte, error) {
	var lastErr error
	for _, host := range c.pool.Hosts() {
		body, err := c.doHost(ctx, host, ep, q)
		if err == nil {
			c.pool.ReportSuccess(host)
			return body, nil
		}
		lastErr = err
		var hse *httpStatusError
		if !errors.As(err, &hse) {
			c.pool.ReportFailure(host)
		}
	}
	return nil, fmt.Errorf("all mirrors failed for %s: %w", ep, lastErr)
}

// doHost performs a single request against one mirror host. ep may be an
// absolute path (starts with "/", e.g. v3 feed endpoints) or a LineFeed
// endpoint name relative to basePath.
func (c *Client) doHost(ctx context.Context, host, ep string, q orderedQuery) ([]byte, error) {
	if err := c.bootstrap(ctx, host); err != nil {
		return nil, fmt.Errorf("bootstrap %s: %w", host, err)
	}

	path := ep
	if !strings.HasPrefix(path, "/") {
		path = c.basePath + path
	}
	u := c.scheme + "://" + host + path
	if len(q) > 0 {
		u += "?" + q.encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", defaultUA)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Accept-Encoding", "gzip")

	jar := c.jarFor(host)
	for _, ck := range jar.Cookies(req.URL) {
		req.AddCookie(ck)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if jar != nil {
		jar.SetCookies(req.URL, resp.Cookies())
	}

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("%s: %w", host, &httpStatusError{status: resp.StatusCode})
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	body, err = maybeGunzip(body)
	if err != nil {
		return nil, fmt.Errorf("%s: gunzip: %w", host, err)
	}
	return body, nil
}

// bootstrap fetches the homepage once per host to obtain session cookies.
func (c *Client) bootstrap(ctx context.Context, host string) error {
	c.jarMu.Lock()
	if c.booted[host] {
		c.jarMu.Unlock()
		return nil
	}
	c.jarMu.Unlock()

	u := c.scheme + "://" + host + "/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", defaultUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if jar := c.jarFor(host); jar != nil {
		jar.SetCookies(req.URL, resp.Cookies())
	}

	c.jarMu.Lock()
	c.booted[host] = true
	c.jarMu.Unlock()
	return nil
}

// jarFor returns a per-host cookie jar.
func (c *Client) jarFor(host string) http.CookieJar {
	c.jarMu.Lock()
	defer c.jarMu.Unlock()
	jar, ok := c.jars[host]
	if !ok {
		jar, _ = cookiejar.New(nil)
		c.jars[host] = jar
	}
	return jar
}

// ProbeMirrors re-checks demoted mirrors so they can rejoin the pool.
// Run periodically (e.g. every 60s) as a goroutine.
func (c *Client) ProbeMirrors(ctx context.Context) {
	c.pool.ProbeAndRestore(func(host string) error {
		u := c.scheme + "://" + host + "/"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", defaultUA)
		resp, err := c.http.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("probe %s: http %d", host, resp.StatusCode)
		}
		return nil
	})
}

// maybeGunzip decompresses the body if it is gzip (by header magic).
func maybeGunzip(b []byte) ([]byte, error) {
	if len(b) < 2 || b[0] != 0x1f || b[1] != 0x8b {
		return b, nil
	}
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}
