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
	"xbet-api/internal/model"
	"xbet-api/internal/xbet"
)

// defaultMirrors: the working regional gateways first (reachable from most
// networks, no Cloudflare challenge for the feed API), then the cleaned .com
// mirror list from the subdomain enumeration. Override with XBET_MIRRORS or
// the -mirrors flag.
var defaultMirrors = []string{
	"1xbet.ng", "1xbet.co.ke", "1xbet.ci", "1xbet.ug",
	"1xbet.com",
	"ua.1xbet.com", "de.1xbet.com", "ge.1xbet.com", "in.1xbet.com",
	"my.1xbet.com", "nl.1xbet.com", "br.1xbet.com", "th.1xbet.com",
	"jp.1xbet.com", "vn.1xbet.com", "ar.1xbet.com", "md.1xbet.com",
	"bg.1xbet.com", "sg.1xbet.com", "id.1xbet.com", "cl.1xbet.com",
	"co.1xbet.com", "az.1xbet.com", "tn.1xbet.com", "ve.1xbet.com",
	"pe.1xbet.com", "so.1xbet.com", "bf.1xbet.com", "kr.1xbet.com",
	"aze.1xbet.com", "india.1xbet.com", "chile.1xbet.com", "bra.1xbet.com",
	"eg.1xbet.com", "ca.1xbet.com", "qa.1xbet.com", "am.1xbet.com",
	"tr.1xbet.com", "kw.1xbet.com", "ind.1xbet.com", "dz.1xbet.com",
	"om.1xbet.com", "mr.1xbet.com", "bol.1xbet.com", "cr.1xbet.com",
	"jo.1xbet.com", "bh.1xbet.com", "mobil.1xbet.com", "sa.1xbet.com",
	"ven.1xbet.com", "irq.1xbet.com", "tur.1xbet.com", "sy.1xbet.com",
	"ie.1xbet.com", "lb.1xbet.com", "singa.1xbet.com", "ir.1xbet.com",
	"afg.1xbet.com", "ps.1xbet.com", "ly.1xbet.com", "ye.1xbet.com",
	"indi.1xbet.com", "mn.1xbet.com", "py.1xbet.com", "lk.1xbet.com",
	"np.1xbet.com", "ury.1xbet.com", "thai.1xbet.com", "korea.1xbet.com",
	"al.1xbet.com", "indian.1xbet.com", "tw.1xbet.com", "ru.1xbet.com",
	"mm.1xbet.com", "mda.1xbet.com", "sing.1xbet.com", "par.1xbet.com",
	"argen.1xbet.com", "pk.1xbet.com", "ga.1xbet.com", "ht.1xbet.com",
	"sd.1xbet.com", "malay.1xbet.com", "colombia.1xbet.com", "hn.1xbet.com",
	"uz.1xbet.com", "do.1xbet.com", "sv.1xbet.com", "ni.1xbet.com",
	"gt.1xbet.com", "nepal.1xbet.com", "bd.1xbet.com", "indonesia.1xbet.com",
	"cambodia.1xbet.com", "a.1xbet.com", "mongolia.1xbet.com", "nepali.1xbet.com",
	"bo.1xbet.com",
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
	{ID: 56, Name: "Martial Arts (MMA/UFC)"},
	{ID: 80, Name: "Gaelic Football"},
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
		Addr:              *addr,
		Handler:           api.New(api.Options{Fetcher: client, Sports: curatedSports}).Handler(),
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
