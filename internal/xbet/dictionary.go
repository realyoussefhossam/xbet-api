package xbet

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sync"
)

// markets_en.json.gz is the official 1xbet market-group template dictionary
// (extracted from /genfiles/cms/betstemplates/bets_model_{short,full}_en_{chunk}.json,
// merged: group id -> {name, markets: {outcome type -> name template}}).
//
//go:embed data/markets_en.json.gz
var marketsDictGz []byte

// marketTpl is the name template for one outcome type within a market group.
type marketTpl struct {
	N string `json:"n"` // name template; "()" = parameter placeholder
	T int    `json:"t"` // template type: 1=plain, 3=single param, 5=param pair
}

// marketGroupTpl is the template for one market group.
type marketGroupTpl struct {
	Name    string               `json:"name"`
	Short   string               `json:"short"`
	Markets map[string]marketTpl `json:"markets"`
}

var (
	dictOnce sync.Once
	dict     map[string]marketGroupTpl
	dictErr  error
)

func loadDict() (map[string]marketGroupTpl, error) {
	dictOnce.Do(func() {
		zr, err := gzip.NewReader(bytes.NewReader(marketsDictGz))
		if err != nil {
			dictErr = fmt.Errorf("markets dict gunzip: %w", err)
			return
		}
		raw, err := io.ReadAll(zr)
		zr.Close()
		if err != nil {
			dictErr = fmt.Errorf("markets dict read: %w", err)
			return
		}
		var d map[string]marketGroupTpl
		if err := json.Unmarshal(raw, &d); err != nil {
			dictErr = fmt.Errorf("markets dict decode: %w", err)
			return
		}
		dict = d
	})
	return dict, dictErr
}

// dictMarketName returns the official market name for a group id, or "".
func dictMarketName(gid int) string {
	d, err := loadDict()
	if err != nil || d == nil {
		return ""
	}
	if g, ok := d[fmt.Sprint(gid)]; ok {
		return g.Name
	}
	return ""
}

// dictOutcomeName renders the outcome name for (group, type, param).
// Returns "" when no template exists.
func dictOutcomeName(gid, tid int, param float64) string {
	d, err := loadDict()
	if err != nil || d == nil {
		return ""
	}
	g, ok := d[fmt.Sprint(gid)]
	if !ok {
		return ""
	}
	t, ok := g.Markets[fmt.Sprint(tid)]
	if !ok {
		return ""
	}
	return renderTemplate(t.N, t.T, param)
}

// renderTemplate fills the parameter placeholder(s) of a name template.
//   - T=1: plain template, no parameter
//   - T=3: replace "()" with "(param)"
//   - T=5: replace "()-()" with "(p1-p2)" (param encodes p1.p2 as int.frac*1000)
func renderTemplate(tpl string, t int, param float64) string {
	if tpl == "" {
		return ""
	}
	switch t {
	case 1:
		return tpl
	case 3:
		if !containsStr(tpl, "()") {
			return tpl
		}
		return replaceOnce(tpl, "()", "("+fmtParam(param)+")")
	case 5:
		if !containsStr(tpl, "()-()") {
			return tpl
		}
		return replaceOnce(tpl, "()-()", "("+fmtParamPair(param)+")")
	default:
		if param != 0 && containsStr(tpl, "()") {
			return replaceOnce(tpl, "()", "("+fmtParam(param)+")")
		}
		return tpl
	}
}

// fmtParam renders a handicap/total/round parameter without trailing zeros.
func fmtParam(p float64) string {
	if p == math.Trunc(p) {
		return fmt.Sprintf("%.0f", p)
	}
	s := fmt.Sprintf("%.3f", p)
	// strip trailing zeros
	for len(s) > 1 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}

// fmtParamPair decodes the int.frac*1000 encoding used for round/minute
// intervals (e.g. 1.003 -> "1-3", 1.015 -> "1-15").
func fmtParamPair(p float64) string {
	i := int(p)
	f := int(math.Round((p - float64(i)) * 1000))
	return fmt.Sprintf("%d-%d", i, f)
}

func replaceOnce(s, old, new string) string {
	i := indexOf(s, old)
	if i < 0 {
		return s
	}
	return s[:i] + new + s[i+len(old):]
}

// containsStr reports whether s contains sub.
func containsStr(s, sub string) bool {
	return indexOf(s, sub) >= 0
}

// indexOf returns the byte index of sub in s, or -1.
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
