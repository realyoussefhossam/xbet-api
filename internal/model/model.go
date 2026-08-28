// Package model defines the normalized data model exposed by the API.
package model

import "time"

// Event statuses (normalized from 1xbet's numeric status codes).
const (
	StatusPrematch = "prematch"
	StatusLive     = "live"
	StatusFinished = "finished"
	StatusUnknown  = "unknown"
)

// Sport is a top-level sport category.
type Sport struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// League is a championship/league within a sport.
type League struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	SportID  int    `json:"sport_id"`
	Country  string `json:"country,omitempty"`
	HasGames bool   `json:"has_games,omitempty"`
}

// Odds is the main 1X2 market for an event.
type Odds struct {
	Home float64 `json:"1"`
	Draw float64 `json:"X"`
	Away float64 `json:"2"`
}

// Event is a single match in the feed.
type Event struct {
	ID         int64          `json:"id"`
	SportID    int            `json:"sport_id"`
	LeagueID   int            `json:"league_id"`
	LeagueName string         `json:"league_name,omitempty"`
	Home       string         `json:"home"`
	Away       string         `json:"away"`
	StartTime  time.Time      `json:"start_time"`
	Status     string         `json:"status"`
	RawStatus  int            `json:"raw_status"`
	Score      map[string]int `json:"score,omitempty"` // {"home":..,"away":..}
	MainOdds   *Odds          `json:"main_odds,omitempty"`
	Locked     bool           `json:"locked,omitempty"` // betting suspended (live)
}

// IsLive reports whether the event is currently in play.
func (e Event) IsLive() bool { return e.Status == StatusLive }

// Outcome is a single betting outcome within a market.
type Outcome struct {
	ID     int64   `json:"id"`
	Name   string  `json:"name"`
	Odds   float64 `json:"odds"`
	Locked bool    `json:"locked,omitempty"` // 1xbet "Block": betting closed
}

// Market is a group of outcomes (e.g. "Match Winner").
type Market struct {
	ID       int64     `json:"id"`
	Name     string    `json:"name"`
	Group    string    `json:"group,omitempty"`
	Locked   bool      `json:"locked,omitempty"`
	Outcomes []Outcome `json:"outcomes"`
}

// SubGame is an attached game (e.g. "Special bets", "Knockdowns") whose
// markets live under its own game id, fetchable via /events/{id}/markets.
type SubGame struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	OutcomeCount int    `json:"outcome_count,omitempty"`
	Home         string `json:"home,omitempty"`
	Away         string `json:"away,omitempty"`
	League       string `json:"league,omitempty"`
	SportID      int    `json:"sport_id,omitempty"`
}

// EventDetail is a full event including all markets and odds.
type EventDetail struct {
	Event
	Markets  []Market  `json:"markets"`
	SubGames []SubGame `json:"sub_games,omitempty"`
}

// ---- results (finished games) ----

// ResultSport is a sport that has results in a window.
type ResultSport struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	IsTop bool   `json:"is_top,omitempty"`
}

// ResultChamp is a champ with finished games in a window.
type ResultChamp struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	SportID    int    `json:"sport_id"`
	GamesCount int    `json:"games_count"`
}

// ResultGame is a finished game with its final result.
type ResultGame struct {
	ID        int64           `json:"id"`
	SportID   int             `json:"sport_id"`
	ChampID   int             `json:"champ_id"`
	ChampName string          `json:"champ_name"`
	Home      string          `json:"home"`
	Away      string          `json:"away"`
	Score     string          `json:"score"` // multi-line result text
	StartTime time.Time       `json:"start_time"`
	SubGames  []ResultSubGame `json:"sub_games,omitempty"`
}

// ResultSubGame is a sub-game score of a finished game (corners, cards...).
type ResultSubGame struct {
	Title string `json:"title"`
	Score string `json:"score"`
}

// ---- rules (settlement documentation) ----

// RuleChapter is a chapter of the official betting rules.
type RuleChapter struct {
	ID          int           `json:"id"`
	Title       string        `json:"title"`
	Description string        `json:"description,omitempty"` // HTML (chapters only)
	Children    []RuleChapter `json:"children,omitempty"`    // menu only
}

// ---- X-Zone ----

// ZoneGame is a finished game with detailed stats available.
type ZoneGame struct {
	ID        int64             `json:"id"`
	SportID   int               `json:"sport_id"`
	ChampID   int               `json:"champ_id"`
	ChampName string            `json:"champ_name"`
	Home      string            `json:"home"`
	Away      string            `json:"away"`
	Score     string            `json:"score"`
	StartTime time.Time         `json:"start_time"`
	MatchInfo map[string]string `json:"match_info,omitempty"`
}

// ZoneEvent is one minute-by-minute event of a finished game.
type ZoneEvent struct {
	Order    int    `json:"order"`
	Time     string `json:"time"`
	Opponent int    `json:"opponent"` // 1 = home, 2 = away
	Event    string `json:"event"`
}
