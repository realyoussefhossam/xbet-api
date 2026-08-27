// Command server runs the 1xbet feed API.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"xbet-api/internal/api"
	"xbet-api/internal/locks"
	"xbet-api/internal/model"
	"xbet-api/internal/xbet"
)

// defaultMirrors: verified-working gateways first (regional domains + the
// lite mirror: reachable without Cloudflare challenges), then a small set of
// .com mirrors for networks where those resolve. Keeping the list small
// matters: v3 live calls try hosts x gr candidates, and a huge pool trips
// 1xbet's rate limiter. Override with XBET_MIRRORS or -mirrors.
var defaultMirrors = []string{
	// verified-working gateways first (regional domains + the lite mirror:
	// reachable without Cloudflare challenges), then a small set of .com
	// mirrors for networks where those resolve. Keeping the list small
	// matters: v3 live calls try hosts x gr candidates, and a huge pool
	// trips 1xbet's rate limiter. Override with XBET_MIRRORS or -mirrors.
	"1xbet.ng", "1xlite-11151.pro", "1xbet.co.ke", "1xbet.ci", "1xbet.ug",
	"1xbet.com", "ua.1xbet.com", "de.1xbet.com", "in.1xbet.com",
	"tr.1xbet.com", "br.1xbet.com",
}

// curatedSports maps 1xbet sport ids to display names. IDs verified against
// the live gateway (sport 56 = Martial Arts hosts UFC/MMA orgs).
var curatedSports = []model.Sport{
	{ID: 1, Name: "Football"},
	{ID: 2, Name: "Ice Hockey"},
	{ID: 3, Name: "Basketball"},
	{ID: 4, Name: "Tennis"},
	{ID: 5, Name: "Baseball"},
	{ID: 6, Name: "Volleyball"},
	{ID: 7, Name: "Rugby"},
	{ID: 8, Name: "Handball"},
	{ID: 9, Name: "Boxing"},
	{ID: 10, Name: "Table Tennis"},
	{ID: 11, Name: "Chess"},
	{ID: 12, Name: "Billiards"},
	{ID: 13, Name: "American Football"},
	{ID: 14, Name: "Futsal"},
	{ID: 16, Name: "Badminton"},
	{ID: 18, Name: "Motorsport"},
	{ID: 20, Name: "TV Games"},
	{ID: 21, Name: "Darts"},
	{ID: 26, Name: "Formula 1"},
	{ID: 27, Name: "Field Hockey"},
	{ID: 28, Name: "Australian Rules"},
	{ID: 30, Name: "Snooker"},
	{ID: 36, Name: "Bicycle Racing"},
	{ID: 40, Name: "Esports"},
	{ID: 41, Name: "Golf"},
	{ID: 44, Name: "Horse Racing"},
	{ID: 48, Name: "Lacrosse"},
	{ID: 53, Name: "Wrestling"},
	{ID: 56, Name: "Martial Arts (MMA orgs: ONE, KSW, ...)"},
	{ID: 80, Name: "Gaelic Football"},
	{ID: 189, Name: "UFC"},
}

func main() {
	var (
		addr    = flag.String("addr", ":8080", "listen address")
		mirrors = flag.String("mirrors", "", "comma-separated mirror hosts (overrides env/defaults)")
	)
	flag.Parse()

	list := *mirrors
	if list == "" {
		list = os.Getenv("XBET_MIRRORS")
	}
	if list != "" {
		defaultMirrors = strings.Split(list, ",")
		for i := range defaultMirrors {
			defaultMirrors[i] = strings.TrimSpace(defaultMirrors[i])
		}
	}

	client := xbet.NewClient(xbet.ClientOptions{
		Mirrors: defaultMirrors,
		Lng:     "en",
		Timeout: 15 * time.Second,
	})

	// lock-event watcher: polls live feeds at a safe interval and emits
	// lock/unlock transitions (1xbet has no websocket push; the v3 feed is
	// short-polled by design)
	lockCtx, lockCancel := context.WithCancel(context.Background())
	defer lockCancel()
	watcher := locks.New(client, 5*time.Second)
	go watcher.Start(lockCtx)

	// background: re-probe demoted mirrors every 60s
	probeCtx, probeCancel := context.WithCancel(context.Background())
	defer probeCancel()
	go func() {
		for {
			select {
			case <-probeCtx.Done():
				return
			case <-time.After(60 * time.Second):
				client.ProbeMirrors(probeCtx)
			}
		}
	}()

	srv := &http.Server{
		Addr: *addr,
		Handler: api.New(api.Options{
			Fetcher: client,
			Sports:  curatedSports,
			Watcher: watcher,
		}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("xbet-api listening on %s (%d mirrors)", *addr, len(defaultMirrors))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Println("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}
