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

// EventDetail is a full event including all markets and odds.
type EventDetail struct {
	Event
	Markets []Market `json:"markets"`
}
