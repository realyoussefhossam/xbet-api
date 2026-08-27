package xbet

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || (len(s) > 0 && indexOf(s, sub) >= 0))
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
