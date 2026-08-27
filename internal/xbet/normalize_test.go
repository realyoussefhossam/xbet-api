package xbet

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"xbet-api/internal/model"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("../../testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return b
}

func decodeEnvelope(t *testing.T, name string) apiEnvelope {
	t.Helper()
	var env apiEnvelope
	if err := json.Unmarshal(loadFixture(t, name), &env); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return env
}

func TestNormalizeChampsNew(t *testing.T) {
	env := decodeEnvelope(t, "champs-new.json")
	if err := envErr(env); err != nil {
		t.Fatal(err)
	}
	var champs []rawChamp
	if err := json.Unmarshal(env.Value, &champs); err != nil {
		t.Fatal(err)
	}
	if len(champs) != 3 {
		t.Fatalf("want 3 champs, got %d", len(champs))
	}
	liga := normalizeLeague(champs[1])
	if liga.ID != 127733 || liga.Name != "Spain. La Liga" || liga.SportID != 1 {
		t.Errorf("bad league: %+v", liga)
	}
	if !liga.HasGames {
		t.Errorf("want HasGames true (GC=20)")
	}
	one := normalizeLeague(champs[2])
	if one.ID != 2485111 || one.SportID != 56 {
		t.Errorf("bad ONE champ: %+v", one)
	}
}

func TestNormalizeChampsLegacy(t *testing.T) {
	var env apiEnvelope
	if err := json.Unmarshal(loadFixture(t, "champs.json"), &env); err != nil {
		t.Fatal(err)
	}
	var champs []rawChamp
	if err := json.Unmarshal(env.Value, &champs); err != nil {
		t.Fatal(err)
	}
	germany := normalizeLeague(champs[0])
	if germany.ID != 88 || germany.Name != "Germany" || germany.SportID != 1 {
		t.Errorf("bad legacy champ: %+v", germany)
	}
}

func TestNormalizeEventsNew(t *testing.T) {
	env := decodeEnvelope(t, "events-new.json")
	var events []rawEvent
	if err := json.Unmarshal(env.Value, &events); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("want 2 events, got %d", len(events))
	}

	ev := normalizeEvent(events[1])
	if ev.ID != 736802022 {
		t.Errorf("bad id: %d", ev.ID)
	}
	if ev.Home != "Barcelona" || ev.Away != "Athletic Bilbao" {
		t.Errorf("bad teams: %s vs %s", ev.Home, ev.Away)
	}
	if ev.LeagueID != 127733 || ev.LeagueName != "Spain. La Liga" {
		t.Errorf("bad league: %+v", ev)
	}
	if ev.SportID != 1 {
		t.Errorf("bad sport: %d", ev.SportID)
	}
	if ev.Status != "prematch" {
		t.Errorf("want prematch, got %s (raw %d)", ev.Status, ev.RawStatus)
	}
	want := time.Unix(1787857200, 0).UTC()
	if !ev.StartTime.Equal(want) {
		t.Errorf("bad start time: %v want %v", ev.StartTime, want)
	}
}

func TestNormalizeGameFlat(t *testing.T) {
	env := decodeEnvelope(t, "game-new.json")
	var g rawGame
	if err := json.Unmarshal(env.Value, &g); err != nil {
		t.Fatal(err)
	}
	d := normalizeGameFlat(g)

	if d.Home != "Barcelona" || d.Away != "Athletic Bilbao" {
		t.Errorf("bad teams: %s vs %s", d.Home, d.Away)
	}
	if d.LeagueName != "Spain. La Liga" || d.SportID != 1 {
		t.Errorf("bad league/sport: %+v", d)
	}

	// markets grouped by G, in first-seen order: 1, 2, 8, 15, 12, 11
	if len(d.Markets) != 6 {
		t.Fatalf("want 6 markets, got %d", len(d.Markets))
	}

	mw := d.Markets[0]
	if mw.ID != 1 || mw.Name != "Match Winner" || len(mw.Outcomes) != 3 {
		t.Fatalf("bad match winner market: %+v", mw)
	}
	if mw.Outcomes[0].Name != "1" || mw.Outcomes[0].Odds != 1.27 {
		t.Errorf("bad outcome: %+v", mw.Outcomes[0])
	}
	if mw.Outcomes[1].Name != "X" || mw.Outcomes[2].Name != "2" {
		t.Errorf("bad 1X2 labels: %+v", mw.Outcomes)
	}

	ah := d.Markets[1]
	if ah.Name != "Asian Handicap" || len(ah.Outcomes) != 2 {
		t.Fatalf("bad asian handicap: %+v", ah)
	}
	if ah.Outcomes[0].Name != "Handicap -2.5" || ah.Outcomes[1].Name != "Handicap +2.5" {
		t.Errorf("bad handicap labels: %+v", ah.Outcomes)
	}

	total := d.Markets[3]
	if total.Name != "Total Goals" || total.Outcomes[0].Name != "Over 2.5" || total.Outcomes[1].Name != "Under 2.5" {
		t.Errorf("bad total labels: %+v", total.Outcomes)
	}

	scaled := d.Markets[4]
	if scaled.Name != "Goal Interval - Yes" {
		t.Fatalf("bad group 12 name: got %q, want official dict name", scaled.Name)
	}
	// official template for type 2951: "Team 1: ()-() Minutes", P=1.015 -> (1-15)
	if scaled.Outcomes[0].Name != "Team 1: (1-15) Minutes" {
		t.Errorf("goal interval render: got %q, want official template render", scaled.Outcomes[0].Name)
	}

	cs := d.Markets[5]
	if cs.Name != "HT-FT" {
		t.Errorf("bad group 11 name: got %q, want HT-FT", cs.Name)
	}
	if cs.Outcomes[0].Name != "W1/W1" {
		t.Errorf("bad ht-ft outcome: got %q, want W1/W1", cs.Outcomes[0].Name)
	}
}

func TestNormalizeGameLegacy(t *testing.T) {
	var env apiEnvelope
	if err := json.Unmarshal(loadFixture(t, "game.json"), &env); err != nil {
		t.Fatal(err)
	}
	var g legacyGame
	if err := json.Unmarshal(env.Value, &g); err != nil {
		t.Fatal(err)
	}
	d := normalizeGameLegacy(g)
	if d.Home != "Bayern Munich" || d.Away != "Borussia Dortmund" {
		t.Errorf("bad teams: %+v", d)
	}
	if len(d.Markets) != 3 {
		t.Fatalf("want 3 markets, got %d", len(d.Markets))
	}
	mw := d.Markets[0]
	if mw.Name != "Match Winner" || mw.Outcomes[0].Odds != 1.85 {
		t.Errorf("bad legacy market: %+v", mw)
	}
}

func TestOutcomeLabeler(t *testing.T) {
	cases := []struct {
		t    int
		p    float64
		want string
	}{
		{1, 0, "1"}, {2, 0, "X"}, {3, 0, "2"},
		{4, 0, "1X"}, {5, 0, "12"}, {6, 0, "X2"},
		{7, -2.5, "Handicap -2.5"}, {8, 2.5, "Handicap +2.5"},
		{9, 2.5, "Over 2.5"}, {10, 2.5, "Under 2.5"},
		{11, 3, "Over 3"}, {12, 3, "Under 3"},
		{2951, 1.015, "Over 0.5"}, {2952, 16.03, "Under 1.5"},
		{2953, 31.045, "Over 2.5"}, {2954, 46.06, "Under 3.5"},
		{15, 0, "1:0"}, {16, 0, "2:0"}, {2859, 0, "Any other"},
		{999, 0, "Outcome 999"},
	}
	for _, c := range cases {
		if got := outcomeLabel(c.t, c.p); got != c.want {
			t.Errorf("outcomeLabel(%d,%v) = %q, want %q", c.t, c.p, got, c.want)
		}
	}
}

func TestStatusFromST(t *testing.T) {
	cases := []struct {
		st   int
		want string
	}{
		{0, "prematch"}, {1, "prematch"}, {2, "prematch"},
		{3, "unknown"}, {10, "unknown"},
	}
	for _, c := range cases {
		if got := StatusFromST(c.st); got != c.want {
			t.Errorf("StatusFromST(%d) = %q, want %q", c.st, got, c.want)
		}
	}
}

func TestEnvErr(t *testing.T) {
	ok := apiEnvelope{Success: true}
	if err := envErr(ok); err != nil {
		t.Errorf("want nil, got %v", err)
	}
	// new-style error string
	bad := apiEnvelope{Success: false, Error: json.RawMessage(`"Game is not found in Sports!"`)}
	if err := envErr(bad); err == nil {
		t.Error("want error for failed envelope")
	}
	// legacy error object
	legacy := apiEnvelope{Success: false, Error: json.RawMessage(`{"Code":5}`)}
	if err := envErr(legacy); err == nil {
		t.Error("want error for legacy error code")
	}
}

func TestDictionaryRendering(t *testing.T) {
	cases := []struct {
		gid, tid int
		param    float64
		wantName string // market name
		wantOut  string // outcome name
	}{
		{403, 1365, 1, "Win In Round", "W1 In Round (1)"},
		{403, 1366, 3, "Win In Round", "W2 In Round (3)"},
		{969, 1371, 0, "Method Of Victory", "W1 By KO, TKO, DQ Or Refusal"},
		{1134, 2292, 0, "Fight To Go The Distance", "Yes"},
		{1138, 2299, 2, "When Will Bout End", "In Round (2)"},
		{8255, 2064, 1.003, "Win In Rounds Interval", "W1 In Round (1-3)"},
		{10638, 14330, 2.5, "Bout Duration, Minutes", "Over (2.5) Minutes"},
		{11, 15, 0, "HT-FT", "W1/W1"},
		{12, 2951, 1.015, "Goal Interval - Yes", "Team 1: (1-15) Minutes"},
		{2296, 2605, 0, "Method Of Win", "KO"},
		{7077, 5713, 0, "Win Inside The Distance", "Yes"},
		{7059, 5698, 0, "How The Bout Will Be Won", "Points Victory"},
	}
	for _, c := range cases {
		if got := dictMarketName(c.gid); got != c.wantName {
			t.Errorf("dictMarketName(%d) = %q, want %q", c.gid, got, c.wantName)
		}
		if got := dictOutcomeName(c.gid, c.tid, c.param); got != c.wantOut {
			t.Errorf("dictOutcomeName(%d,%d,%v) = %q, want %q", c.gid, c.tid, c.param, got, c.wantOut)
		}
	}
	// base groups fall back to the hardcoded map
	if got := dictMarketName(1); got != "" {
		t.Errorf("group 1 should not be in dict, got %q", got)
	}
}

func TestNormalizeLiveGames(t *testing.T) {
	env := loadFixture(t, "live-games.json")
	var games []rawLiveGame
	if err := json.Unmarshal(env, &games); err != nil {
		t.Fatal(err)
	}
	if len(games) != 2 {
		t.Fatalf("want 2 live games, got %d", len(games))
	}
	for _, g := range games {
		ev := normalizeLiveGame(g)
		if ev.Status != "live" {
			t.Errorf("want live status, got %s", ev.Status)
		}
		if ev.Home == "" || ev.Away == "" {
			t.Errorf("missing teams: %+v", ev)
		}
		if ev.MainOdds == nil {
			t.Errorf("missing main odds for %s vs %s", ev.Home, ev.Away)
		}
	}
	// first game is the live football match
	ev := normalizeLiveGame(games[0])
	if ev.Score == nil {
		t.Errorf("want live score, got nil")
	} else {
		t.Logf("score %d-%d", ev.Score["home"], ev.Score["away"])
	}
}

func TestNormalizeLiveGameEvents(t *testing.T) {
	var ge rawLiveGameEvents
	if err := json.Unmarshal(loadFixture(t, "live-game-events.json"), &ge); err != nil {
		t.Fatal(err)
	}
	d := normalizeLiveGameEvents(ge)
	if d.Status != "live" {
		t.Errorf("want live, got %s", d.Status)
	}
	if len(d.Markets) == 0 {
		t.Fatal("want markets")
	}
	mw := d.Markets[0]
	if mw.ID != 1 || mw.Name != "Match Winner" {
		t.Errorf("bad first market: %+v", mw)
	}
	if len(mw.Outcomes) != 3 {
		t.Errorf("want 3 outcomes in match winner, got %d", len(mw.Outcomes))
	}
}

func TestNormalizeLockedMarkets(t *testing.T) {
	var ge rawLiveGameEvents
	if err := json.Unmarshal(loadFixture(t, "live-game-locked.json"), &ge); err != nil {
		t.Fatal(err)
	}
	d := normalizeLiveGameEvents(ge)
	if !d.Locked {
		t.Error("game should be locked (blocked=true)")
	}
	// eventGroups[1] was marked locked
	var lockedMarket *model.Market
	for i := range d.Markets {
		if d.Markets[i].ID == 8 { // group 8 = Double Chance in fixture
			lockedMarket = &d.Markets[i]
		}
	}
	if lockedMarket == nil {
		t.Fatal("market 8 not found")
	}
	if !lockedMarket.Locked {
		t.Error("market 8 should be locked")
	}
	if !lockedMarket.Outcomes[0].Locked {
		t.Error("outcome should be locked (Block=1)")
	}
}
