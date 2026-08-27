package xbet

import (
	"fmt"
	"math"
	"time"

	"xbet-api/internal/model"
)

// StatusFromST maps 1xbet's numeric event status (SS/ST) to a normalized
// status. Values observed in the LINE feed: 1-2 = not started (prematch).
// Live phases and finished states need verification against live data
// (use /debug/raw on a reachable network to confirm).
func StatusFromST(st int) string {
	switch {
	case st <= 2:
		return model.StatusPrematch
	default:
		return model.StatusUnknown
	}
}

// normalizeLeague converts a raw champ entry into a model.League.
// Handles both the new gateway shape (LI/SI) and the legacy shape (I/S).
func normalizeLeague(c rawChamp) model.League {
	id := int(c.LI)
	if id == 0 {
		id = int(c.I)
	}
	sport := int(c.SI)
	if sport == 0 {
		sport = int(c.S)
	}
	return model.League{
		ID:       id,
		Name:     firstNonEmpty(c.L, c.SN),
		SportID:  sport,
		HasGames: c.GC > 0 || c.IsG || c.R,
	}
}

// normalizeEvent converts a raw feed event (new gateway) into model.Event.
func normalizeEvent(e rawEvent) model.Event {
	home, away := e.O1E, e.O2E
	if home == "" {
		home = e.O1
	}
	if away == "" {
		away = e.O2
	}

	ev := model.Event{
		ID:         int64(e.I),
		LeagueID:   int(e.LI),
		LeagueName: e.L,
		Home:       home,
		Away:       away,
		Status:     StatusFromST(int(e.SS)),
		RawStatus:  int(e.SS),
		SportID:    int(e.SI),
	}
	if ts := int64(e.S); ts > 0 {
		ev.StartTime = time.Unix(ts, 0).UTC()
	}
	if e.HS != 0 {
		// live scores would appear here; LINE feed has no score yet
		ev.Score = map[string]int{"home": int(e.HS)}
	}
	return ev
}

// marketNames is a fallback dictionary for base market groups that have no
// template in the official dictionary (groups 1,2,8,15,17 are app-builtins).
var marketNames = map[int]string{
	1:  "Match Winner",
	2:  "Asian Handicap",
	8:  "Double Chance",
	11: "HT-FT",
	15: "Total Goals",
	17: "Home Total",
	62: "Both Teams To Score",
}

// outcomeLabels maps outcome type ids (E[].T) to base labels.
func outcomeLabel(t int, p float64) string {
	switch t {
	case 1:
		return "1"
	case 2:
		return "X"
	case 3:
		return "2"
	case 4:
		return "1X"
	case 5:
		return "12"
	case 6:
		return "X2"
	case 13:
		return "Yes"
	case 14:
		return "No"
	case 7, 8:
		return fmt.Sprintf("Handicap %+.1f", p)
	case 9, 11:
		return fmt.Sprintf("Over %s", fmtLine(p))
	case 10, 12:
		return fmt.Sprintf("Under %s", fmtLine(p))
	case 2951, 2953:
		return fmt.Sprintf("Over %s", decodeScaledLine(p))
	case 2952, 2954:
		return fmt.Sprintf("Under %s", decodeScaledLine(p))
	case 15, 16, 17, 18, 19, 20, 21, 22, 23, 2858, 2859:
		return scoreLabel(t)
	default:
		return fmt.Sprintf("Outcome %d", t)
	}
}

// scoreLabel maps correct-score outcome types to score lines (G=11).
// Ordering follows 1xbet's standard score list; verify against live data.
func scoreLabel(t int) string {
	scores := []string{
		"1:0", "2:0", "2:1", "1:1", "0:0", "0:1", "1:2", "0:2", "3:0", "3:1", "Any other",
	}
	idx := map[int]int{
		15: 0, 16: 1, 17: 2, 18: 3, 19: 4, 20: 5, 21: 6, 22: 7, 23: 8, 2858: 9, 2859: 10,
	}
	if i, ok := idx[t]; ok && i < len(scores) {
		return scores[i]
	}
	return fmt.Sprintf("Score %d", t)
}

// decodeScaledLine decodes the scaled total/handicap parameter used by
// G=12/G=13 markets. Observed: P=1.015 -> 0.5, 16.03 -> 1.5, 31.045 -> 2.5...
func decodeScaledLine(p float64) string {
	if p == 0 {
		return "0"
	}
	line := (p-1.015)/15.015 + 0.5
	return fmtLine(line)
}

// fmtLine renders a handicap/total line without trailing zeros.
func fmtLine(l float64) string {
	if l == math.Trunc(l) {
		return fmt.Sprintf("%.0f", l)
	}
	return fmt.Sprintf("%.1f", l)
}

// normalizeGameFlat converts the new flat GetGameZip payload into a
// normalized EventDetail with markets grouped by market id (G).
func normalizeGameFlat(g rawGame) model.EventDetail {
	ev := model.Event{
		ID:         int64(g.I),
		LeagueID:   int(g.LI),
		LeagueName: g.L,
		Home:       firstNonEmpty(g.O1E, g.O1),
		Away:       firstNonEmpty(g.O2E, g.O2),
		Status:     StatusFromST(int(g.SS)),
		RawStatus:  int(g.SS),
		SportID:    int(g.SI),
	}
	if ts := int64(g.S); ts > 0 {
		ev.StartTime = time.Unix(ts, 0).UTC()
	}
	if g.HS != 0 || g.G1 != 0 || g.G2 != 0 {
		home, away := int(g.HS), int(g.G2)
		if g.G1 != 0 {
			home = int(g.G1)
		}
		if g.GS != 0 {
			away = int(g.GS)
		}
		ev.Score = map[string]int{"home": home, "away": away}
	}

	// MEC gives market *category* names (Popular/Total/Handicap/...), but its
	// MT ids are not the same as the market ids (E[].G), so we cannot map them
	// reliably without the frontend's private dictionary. Leave group empty.

	// group outcomes by market id
	byMarket := map[int][]rawFlatOutcome{}
	order := []int{}
	for _, o := range g.E {
		gid := int(o.G)
		if _, ok := byMarket[gid]; !ok {
			order = append(order, gid)
		}
		byMarket[gid] = append(byMarket[gid], o)
	}

	detail := model.EventDetail{Event: ev}
	for _, gid := range order {
		outs := byMarket[gid]
		market := model.Market{
			ID:   int64(gid),
			Name: firstNonEmpty(dictMarketName(gid), marketNames[gid]),
		}
		if market.Name == "" {
			market.Name = fmt.Sprintf("Market %d", gid)
		}
		for _, o := range outs {
			market.Outcomes = append(market.Outcomes, model.Outcome{
				ID:   int64(o.T),
				Name: firstNonEmpty(dictOutcomeName(gid, int(o.T), float64(o.P)), outcomeLabel(int(o.T), float64(o.P))),
				Odds: float64(o.C),
			})
		}
		detail.Markets = append(detail.Markets, market)
	}
	return detail
}

// normalizeGameLegacy converts the legacy grouped GetGameZip payload
// (markets with inline names) into a normalized EventDetail.
func normalizeGameLegacy(g legacyGame) model.EventDetail {
	ev := model.Event{
		ID:         int64(g.I),
		LeagueID:   0,
		LeagueName: g.L,
		Home:       firstNonEmpty(g.O1E, g.H1),
		Away:       firstNonEmpty(g.O2E, g.H2),
		Status:     StatusFromST(int(g.ST)),
		RawStatus:  int(g.ST),
		SportID:    int(g.S),
	}
	if ts := int64(g.T); ts > 0 {
		ev.StartTime = time.Unix(ts, 0).UTC()
	}
	if g.G1 != 0 || g.G2 != 0 {
		ev.Score = map[string]int{"home": int(g.G1), "away": int(g.G2)}
	}

	detail := model.EventDetail{Event: ev}
	for _, group := range g.E {
		for _, m := range group.E {
			market := model.Market{
				ID:    int64(m.I),
				Name:  m.N,
				Group: group.N,
			}
			for _, o := range m.E {
				market.Outcomes = append(market.Outcomes, model.Outcome{
					ID:   int64(o.I),
					Name: o.N,
					Odds: float64(o.O),
				})
			}
			detail.Markets = append(detail.Markets, market)
		}
	}
	return detail
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
