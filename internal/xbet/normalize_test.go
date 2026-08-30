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

// TestLineLockFieldB: line (GetGameZip) outcomes carry the lock flag as
// "B": true — verified against Carlos Jamil De Leon Castro vs Maxwel Montez,
// where Double Chance 1X (T=4) and 12 (T=5) are locked at 1.001.
func TestLineLockFieldB(t *testing.T) {
	var env apiEnvelope
	if err := json.Unmarshal(loadFixture(t, "game-locked-b.json"), &env); err != nil {
		t.Fatal(err)
	}
	var g rawGame
	if err := json.Unmarshal(env.Value, &g); err != nil {
		t.Fatal(err)
	}
	d := normalizeGameFlat(g)
	var dc *model.Market
	for i := range d.Markets {
		if d.Markets[i].ID == 8 {
			dc = &d.Markets[i]
		}
	}
	if dc == nil {
		t.Fatal("double chance market (G=8) not found")
	}
	if len(dc.Outcomes) != 3 {
		t.Fatalf("want 3 outcomes, got %d", len(dc.Outcomes))
	}
	want := []struct {
		id     int64
		locked bool
		odds   float64
	}{
		{4, true, 1.001}, // 1X locked
		{5, true, 1.001}, // 12 locked
		{6, false, 8.15}, // X2 open
	}
	for i, w := range want {
		o := dc.Outcomes[i]
		if o.ID != w.id || o.Locked != w.locked || o.Odds != w.odds {
			t.Errorf("outcome %d: got id=%d locked=%v odds=%v, want id=%d locked=%v odds=%v",
				i, o.ID, o.Locked, o.Odds, w.id, w.locked, w.odds)
		}
	}
}

// TestSubGamesAndPL: frontend-style GetGameZip returns grouped markets (GE),
// attached sub-games (SG) and pre-built labels (PL) for event specials.
// Verified against Chantelle Cameron vs Mikaela Mayer (Special bets sub-game
// 747442125: 117 combo outcomes).
func TestSubGamesAndPL(t *testing.T) {
	var env apiEnvelope
	if err := json.Unmarshal(loadFixture(t, "game-subgames.json"), &env); err != nil {
		t.Fatal(err)
	}
	var g rawGame
	if err := json.Unmarshal(env.Value, &g); err != nil {
		t.Fatal(err)
	}
	if len(g.GE) == 0 {
		t.Fatal("want grouped markets (GE)")
	}
	d := normalizeGameFlat(g)
	// 12 markets from GE (incl. 1X2, totals...)
	if len(d.Markets) == 0 {
		t.Fatal("want markets from GE")
	}
	if len(d.SubGames) < 2 {
		t.Fatalf("want sub-games, got %d: %+v", len(d.SubGames), d.SubGames)
	}
	names := map[string]bool{}
	for _, sg := range d.SubGames {
		names[sg.Name] = true
	}
	if !names["Special bets"] || !names["Knockdowns"] {
		t.Fatalf("want Special bets + Knockdowns sub-games, got %v", d.SubGames)
	}
}

// TestPLOutcomeNames: the Special bets sub-game outcomes carry pre-built
// labels (PL.N) that must win over template rendering.
func TestPLOutcomeNames(t *testing.T) {
	var env apiEnvelope
	if err := json.Unmarshal(loadFixture(t, "game-subgames.json"), &env); err != nil {
		t.Fatal(err)
	}
	var g rawGame
	if err := json.Unmarshal(env.Value, &g); err != nil {
		t.Fatal(err)
	}
	// GE contains the specials? no - specials live in the sub-game payload.
	// Simulate: outcome with PL label.
	o := rawFlatOutcome{
		T:  1893,
		G:  9242,
		PL: &rawPL{N: "Chantelle Cameron to Win in Round 10 & Mikaela Mayer to be Knocked Down in Round 9"},
	}
	if got := outcomeName(9242, o); got != "Chantelle Cameron to Win in Round 10 & Mikaela Mayer to be Knocked Down in Round 9 - Yes" {
		t.Errorf("PL label not used: %q", got)
	}
	// template suffix: "[] - Yes" + PL -> "label - Yes"
	o2 := rawFlatOutcome{T: 1893, G: 9242, PL: &rawPL{N: "Both Fighters to be Knocked Down 2+ Times Each"}}
	if got := renderPLLabel(dictOutcomeTemplate(9242, 1893), o2.PL.N); got != "Both Fighters to be Knocked Down 2+ Times Each - Yes" {
		t.Errorf("PL template render: %q", got)
	}
}

// TestWin2WayFallback: group 8389 ("Win (2Way)", 2-way winner market for
// combat sports) has no template in 1xbet's dictionary yet — the base
// fallback must name it and its outcomes. Verified live on Daniel Hooker vs
// Salahdine Parnasse: types 7736/7737 at 4.77/1.175, where
// 1/(1/5.03+1/33)=4.36 and 1/(1/1.196+1/33)=1.15 confirm the draw is folded
// into both sides.
func TestWin2WayFallback(t *testing.T) {
	g := rawGame{
		I: 742911339, O1: "Daniel Hooker", O2: "Salahdine Parnasse", SS: 2,
		GE: []rawGroupedMarket{{
			G: 8389, GS: 2456,
			E: [][]rawFlatOutcome{
				{{T: 7736, C: 4.77, G: 8389}},
				{{T: 7737, C: 1.175, G: 8389}},
			},
		}},
	}
	d := normalizeGameFlat(g)
	if len(d.Markets) != 1 {
		t.Fatalf("markets = %d, want 1", len(d.Markets))
	}
	m := d.Markets[0]
	if m.Name != "Win (2Way)" {
		t.Errorf("market name = %q, want %q", m.Name, "Win (2Way)")
	}
	if len(m.Outcomes) != 2 {
		t.Fatalf("outcomes = %d, want 2", len(m.Outcomes))
	}
	if m.Outcomes[0].Name != "W1" || m.Outcomes[1].Name != "W2" {
		t.Errorf("outcome names = %q/%q, want W1/W2", m.Outcomes[0].Name, m.Outcomes[1].Name)
	}
}

// TestMarketLockSemantics: market.Locked means the market is suspended
// (ALL outcomes closed), not "any outcome is locked". 1xbet floors a dead
// leg (DC 12 @ 1.001) and locks only that outcome while 1X/X2 stay
// bettable — the market must remain open. Verified live: Castro-Montez
// (1X+12 locked, X2 open at 8.15) and Hooker-Parnasse (12 locked only).
func TestMarketLockSemantics(t *testing.T) {
	// Partial lock: market open, outcome 5 locked.
	g := rawGame{
		I: 1, O1: "A", O2: "B", SS: 2,
		E: []rawFlatOutcome{
			{T: 4, C: 4.455, G: 8},
			{T: 5, C: 1.001, G: 8, B: true},
			{T: 6, C: 1.16, G: 8},
		},
	}
	d := normalizeGameFlat(g)
	m := d.Markets[0]
	if m.Locked {
		t.Error("partially locked market must stay open")
	}
	if !m.Outcomes[1].Locked {
		t.Error("outcome 5 must stay locked")
	}
	if m.Outcomes[0].Locked || m.Outcomes[2].Locked {
		t.Error("open outcomes must not be locked")
	}

	// Full lock: every outcome closed -> market suspended.
	g2 := rawGame{
		I: 2, O1: "C", O2: "D", SS: 2,
		E: []rawFlatOutcome{
			{T: 4, C: 2.1, G: 1, B: true},
			{T: 5, C: 3.2, G: 1, B: true},
			{T: 6, C: 3.4, G: 1, B: true},
		},
	}
	d2 := normalizeGameFlat(g2)
	if !d2.Markets[0].Locked {
		t.Error("fully locked market must be suspended")
	}
}
