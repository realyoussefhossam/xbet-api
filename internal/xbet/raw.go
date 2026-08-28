package xbet

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Raw response shapes of 1xbet's LineFeed API (new /service-api gateway).
//
// New gateway responses share one envelope:
//
//	{"Id":0,"Success":true,"Error":"","Guid":"","Value":...}
//
// Legacy 1xbet.com responses use:
//
//	{"Error":{"Code":0},"Value":...}
//
// Error is handled as json.RawMessage so both shapes decode.

// FlexInt accepts JSON numbers and numeric strings.
type FlexInt int64

// UnmarshalJSON implements json.Unmarshaler.
func (f *FlexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("flexint: %w", err)
	}
	*f = FlexInt(n)
	return nil
}

// FlexFloat accepts JSON numbers (int or float) and numeric strings.
type FlexFloat float64

// UnmarshalJSON implements json.Unmarshaler.
func (f *FlexFloat) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("flexfloat: %w", err)
	}
	*f = FlexFloat(n)
	return nil
}

// apiEnvelope is the common response wrapper.
type apiEnvelope struct {
	Id      int             `json:"Id"`
	Success bool            `json:"Success"`
	Error   json.RawMessage `json:"Error"` // "" (new) or {"Code":n} (legacy)
	Guid    string          `json:"Guid"`
	Value   json.RawMessage `json:"Value"`
}

// ErrorCode returns the legacy error code, or 0.
func (e apiEnvelope) ErrorCode() int {
	var legacy struct {
		Code int `json:"Code"`
	}
	if len(e.Error) > 0 && e.Error[0] == '{' {
		_ = json.Unmarshal(e.Error, &legacy)
	}
	return legacy.Code
}

// rawChamp is an entry of GetChampsZip Value (new shape; legacy tags kept).
type rawChamp struct {
	// legacy
	I   FlexInt `json:"I"` // champ id
	L   string  `json:"L"` // name
	S   FlexInt `json:"S"` // sport id
	SId FlexInt `json:"SId"`
	Z   FlexInt `json:"Z"`
	P   bool    `json:"P"`
	Q   bool    `json:"Q"`
	IsG bool    `json:"IsG"`
	R   bool    `json:"R"`
	// new gateway
	SI    FlexInt `json:"SI"` // sport id
	SN    string  `json:"SN"` // sport name
	SR    string  `json:"SR"` // sport name (ru)
	LR    string  `json:"LR"` // league name (ru)
	LI    FlexInt `json:"LI"` // league id
	CI    FlexInt `json:"CI"` // champ id
	GC    FlexInt `json:"GC"` // game count
	CHIMG string  `json:"CHIMG"`
}

// rawEvent is an entry of Get1x2_Zip Value (new gateway shape).
type rawEvent struct {
	B   FlexInt `json:"B"` // feed bucket
	GVE FlexInt `json:"GVE"`
	GSE bool    `json:"GSE"`
	HL  bool    `json:"HL"`
	IG  bool    `json:"IG"`
	I   FlexInt `json:"I"` // event id
	N   FlexInt `json:"N"`
	CI  FlexInt `json:"CI"`
	T   FlexInt `json:"T"` // event type (1000 = line)
	E   []any   `json:"E"`
	EC  FlexInt `json:"EC"`
	TG  string  `json:"TG"`
	V   string  `json:"V"`
	VE  string  `json:"VE"`
	PN  string  `json:"PN"`
	TN  string  `json:"TN"`
	SS  FlexInt `json:"SS"`  // status
	SST FlexInt `json:"SST"` // status (alt)
	HSI bool    `json:"HSI"`
	SSI FlexInt `json:"SSI"`
	STI string  `json:"STI"`
	S   FlexInt `json:"S"`   // start time (unix)
	HS  FlexInt `json:"HS"`  // home score (live)
	SGC FlexInt `json:"SGC"` // ?
	O1  string  `json:"O1"`  // home team
	O2  string  `json:"O2"`  // away team
	O1E string  `json:"O1E"` // home team (en)
	O2E string  `json:"O2E"` // away team (en)
	O1R string  `json:"O1R"`
	O2R string  `json:"O2R"`
	O1I FlexInt `json:"O1I"`
	O2I FlexInt `json:"O2I"`
	O1C FlexInt `json:"O1C"`
	O2C FlexInt `json:"O2C"`
	DI  string  `json:"DI"` // e.g. "2 Matches" (express games)
	SI  FlexInt `json:"SI"` // sport id
	SN  string  `json:"SN"` // sport name
	L   string  `json:"L"`  // league name
	LR  string  `json:"LR"`
	LI  FlexInt `json:"LI"` // league id
	CN  string  `json:"CN"` // country
	COI FlexInt `json:"COI"`
	MS  []int   `json:"MS"`
	KI  FlexInt `json:"KI"`
	CID FlexInt `json:"CID"`
	GLI FlexInt `json:"GLI"`
	WP  *rawWP  `json:"WP"` // win probabilities (not odds)
}

type rawWP struct {
	P1 float64 `json:"P1"`
	P2 float64 `json:"P2"`
	PX float64 `json:"PX"`
}

// rawFlatOutcome is one outcome of the flat game odds list (GetGameZip E).
type rawFlatOutcome struct {
	T       FlexInt   `json:"T"` // outcome type id
	P       FlexFloat `json:"P"` // parameter (handicap line / total)
	C       FlexFloat `json:"C"` // decimal odds
	CV      string    `json:"CV"`
	G       FlexInt   `json:"G"` // market (group) id
	CE      FlexInt   `json:"CE"`
	PL      *rawPL    `json:"PL"`      // pre-built label (event specials combos)
	B       bool      `json:"B"`       // true = betting locked (line GetGameZip)
	Block   FlexInt   `json:"Block"`   // legacy lock marker
	Blocked bool      `json:"blocked"` // true = betting locked (live payloads)
}

// rawPL is a pre-built outcome label (e.g. "Chantelle Cameron to Win in
// Round 10 & Mikaela Mayer to be Knocked Down in Round 9").
type rawPL struct {
	N string  `json:"N"`
	I FlexInt `json:"I"`
}

// rawGroupedMarket is one market group of the grouped (GE) game format.
type rawGroupedMarket struct {
	G  FlexInt            `json:"G"`
	GS FlexInt            `json:"GS"`
	E  [][]rawFlatOutcome `json:"E"`
}

// rawSubGame is an attached sub-game (SG) of a game.
type rawSubGame struct {
	I   FlexInt `json:"I"`
	N   FlexInt `json:"N"`
	TG  string  `json:"TG"`
	PN  string  `json:"PN"` // period name for period variants
	EC  FlexInt `json:"EC"`
	O1E string  `json:"O1E"`
	O1  string  `json:"O1"`
	O2E string  `json:"O2E"`
	O2  string  `json:"O2"`
	L   string  `json:"L"`
	SI  FlexInt `json:"SI"`
}

// rawMEC is a market category entry.
type rawMEC struct {
	MT FlexInt `json:"MT"`
	EC FlexInt `json:"EC"`
	N  string  `json:"N"`
}

// rawGame is the Value of GetGameZip (new flat format).
type rawGame struct {
	B     FlexInt            `json:"B"`
	I     FlexInt            `json:"I"` // event id
	SI    FlexInt            `json:"SI"`
	SN    string             `json:"SN"`
	SR    string             `json:"SR"`
	SE    string             `json:"SE"`
	L     string             `json:"L"` // league
	LR    string             `json:"LR"`
	LE    string             `json:"LE"`
	LI    FlexInt            `json:"LI"`
	CN    string             `json:"CN"`
	COI   FlexInt            `json:"COI"`
	T     FlexInt            `json:"T"`
	S     FlexInt            `json:"S"` // start time
	SS    FlexInt            `json:"SS"`
	SST   FlexInt            `json:"SST"`
	O1    string             `json:"O1"`
	O2    string             `json:"O2"`
	O1E   string             `json:"O1E"`
	O2E   string             `json:"O2E"`
	O1R   string             `json:"O1R"`
	O2R   string             `json:"O2R"`
	HS    FlexInt            `json:"HS"` // home score
	GS    FlexInt            `json:"GS"` // away score (if present)
	G1    FlexInt            `json:"G1"` // legacy score keys
	G2    FlexInt            `json:"G2"`
	MIS   []any              `json:"MIS"`
	MIO   any                `json:"MIO"`
	E     []rawFlatOutcome   `json:"E"`   // flat outcomes
	GE    []rawGroupedMarket `json:"GE"`  // grouped outcomes (frontend-style call)
	SG    []rawSubGame       `json:"SG"`  // attached sub-games (special bets etc.)
	MEC   []rawMEC           `json:"MEC"` // market categories
	SmI   FlexInt            `json:"SmI"`
	MS    []int              `json:"MS"`
	KI    FlexInt            `json:"KI"`
	MEC2  any                `json:"MEC2"`
	HHTHS bool               `json:"HHTHS"`
	GLI   FlexInt            `json:"GLI"`
	SUBA  bool               `json:"SUBA"`
}

// legacyGame is the Value of GetGameZip on the legacy 1xbet.com API
// (grouped markets with inline names). Kept for compatibility.
type legacyGame struct {
	G   FlexInt             `json:"G"`
	I   FlexInt             `json:"I"` // event id
	S   FlexInt             `json:"S"` // sport id
	L   string              `json:"L"` // league name
	C   string              `json:"C"` // country name
	T   FlexInt             `json:"T"` // start time (unix)
	ST  FlexInt             `json:"ST"`
	O1E string              `json:"O1E"`
	O2E string              `json:"O2E"`
	H1  string              `json:"H1"`
	H2  string              `json:"H2"`
	G1  FlexInt             `json:"G1"`
	G2  FlexInt             `json:"G2"`
	E   []legacyMarketGroup `json:"E"`
}

type legacyMarketGroup struct {
	G FlexInt        `json:"G"`
	N string         `json:"N"`
	E []legacyMarket `json:"E"`
}

type legacyMarket struct {
	I FlexInt         `json:"I"`
	N string          `json:"N"`
	P FlexInt         `json:"P"`
	E []legacyOutcome `json:"E"`
}

type legacyOutcome struct {
	I FlexInt   `json:"I"`
	O FlexFloat `json:"O"`
	N string    `json:"N"`
	C FlexInt   `json:"C"`
}

// ---- live feed (main-live-feed/v3) ----

// rawLiveGame is one entry of games1x2 (live games list).
type rawLiveGame struct {
	ID      FlexInt `json:"id"`
	ConstID FlexInt `json:"constId"`
	StartTs FlexInt `json:"startTs"`
	Kind    FlexInt `json:"kind"`
	Blocked bool    `json:"blocked"` // whole game locked (present only when locked)
	Sport   struct {
		ID   FlexInt `json:"id"`
		Name string  `json:"name"`
	} `json:"sport"`
	Liga struct {
		ID   FlexInt `json:"id"`
		Name string  `json:"name"`
	} `json:"liga"`
	Opponent1 struct {
		FullName string `json:"fullName"`
	} `json:"opponent1"`
	Opponent2 struct {
		FullName string `json:"fullName"`
	} `json:"opponent2"`
	Scores      rawLiveScores  `json:"scores"`
	EventGroups []rawLiveGroup `json:"eventGroups"`
}

// rawLiveScores is the in-play score block.
type rawLiveScores struct {
	ScoreOpp1         int    `json:"scoreOpp1"`
	ScoreOpp2         int    `json:"scoreOpp2"`
	CurrentPeriodName string `json:"currentPeriodName"`
	FullScore         string `json:"fullScore"`
}

// rawLiveGroup is one market group of the live event feed.
type rawLiveGroup struct {
	GroupID FlexInt            `json:"groupId"`
	Events  [][]rawLiveOutcome `json:"events"`
	Blocked bool               `json:"blocked"` // market locked (present only when locked)
}

// rawLiveOutcome is one outcome of a live market group.
type rawLiveOutcome struct {
	Type      FlexInt   `json:"type"`
	CF        FlexFloat `json:"cf"`
	Parameter FlexFloat `json:"parameter"`
	Blocked   bool      `json:"blocked"` // betting locked (present only when locked)
}

// rawLiveGameEvents is the response of gameEvents for a live game.
type rawLiveGameEvents struct {
	EventGroups       []rawLiveGroup `json:"eventGroups"`
	MarketEventsCount []rawMEC       `json:"marketEventsCount"`
	Scores            rawLiveScores  `json:"scores"`
	ID                FlexInt        `json:"id"`
	StartTs           FlexInt        `json:"startTs"`
	Kind              FlexInt        `json:"kind"`
	Blocked           bool           `json:"blocked"` // game locked
}

// ---- results API (result/web/api) ----

// rawResultSports is the response of v2/sports.
type rawResultSports struct {
	Count int              `json:"count"`
	Items []rawResultSport `json:"items"`
}

type rawResultSport struct {
	ID    FlexInt `json:"id"`
	Name  string  `json:"name"`
	IsTop bool    `json:"isTop"`
}

// rawResultChamps is the response of v2/champs.
type rawResultChamps struct {
	Count int              `json:"count"`
	Items []rawResultChamp `json:"items"`
}

type rawResultChamp struct {
	ID         FlexInt `json:"id"`
	Name       string  `json:"name"`
	SportID    FlexInt `json:"sportId"`
	GamesCount FlexInt `json:"gamesCount"`
	Image      string  `json:"image"`
}

// rawResultGames is the response of v3/games (finished games).
type rawResultGames struct {
	Count int             `json:"count"`
	Items []rawResultGame `json:"items"`
}

type rawResultGame struct {
	ID           FlexInt            `json:"id"`
	SportID      FlexInt            `json:"sportId"`
	ChampID      FlexInt            `json:"champId"`
	ChampName    string             `json:"champName"`
	Opp1         string             `json:"opp1"`
	Opp2         string             `json:"opp2"`
	Score        string             `json:"score"` // multi-line: winner / round / method
	DopInfo      string             `json:"dopInfo"`
	HasSubGame   bool               `json:"hasSubGame"`
	DateStart    FlexInt            `json:"dateStart"`
	CountSubGame FlexInt            `json:"countSubGame"`
	SubGame      []rawResultSubGame `json:"subGame"`
}

type rawResultSubGame struct {
	Title string `json:"title"`
	Score string `json:"score"`
}

// ---- rules API (agreements-legacy-api) ----

// rawRuleMenu is the rules chapter menu.
type rawRuleMenu struct {
	Chapters []rawRuleChapterNode `json:"chapters"`
}

type rawRuleChapterNode struct {
	ID       FlexInt              `json:"id"`
	Title    string               `json:"title"`
	Sort     FlexInt              `json:"sort"`
	Link     string               `json:"link"`
	Source   string               `json:"source"`
	Tags     []string             `json:"tags"`
	Segment  string               `json:"segment"`
	Children []rawRuleChapterNode `json:"children,omitempty"`
}

// rawRuleChapter is one rules chapter's content.
type rawRuleChapter struct {
	ID          FlexInt                      `json:"id"`
	Title       string                       `json:"title"`
	Description string                       `json:"description"` // HTML
	Subsections map[string]rawRuleSubsection `json:"rule_subsection"`
}

type rawRuleSubsection struct {
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Sort        FlexInt `json:"sort"`
}

// ---- X-Zone (result1xzone) ----

// rawZoneGame is one zone game (finished game with stats availability).
type rawZoneGame struct {
	ID         FlexInt           `json:"id"`
	SportID    FlexInt           `json:"sportId"`
	ChampID    FlexInt           `json:"champId"`
	ChampName  string            `json:"champName"`
	Opp1       string            `json:"opp1"`
	Opp2       string            `json:"opp2"`
	Score      string            `json:"score"`
	DateStart  FlexInt           `json:"dateStart"`
	MatchInfos map[string]string `json:"matchInfos"`
}

// rawZoneGameDetail is one zone game with its minute-by-minute timeline.
type rawZoneGameDetail struct {
	ID    FlexInt        `json:"id"`
	Stats []rawZoneEvent `json:"stats"`
}

type rawZoneEvent struct {
	EventOrder FlexInt `json:"eventOrder"`
	Time       string  `json:"time"`
	OppNumber  FlexInt `json:"oppNumber"`
	Event      string  `json:"event"`
}
