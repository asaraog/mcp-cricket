package explainer

// Par: the total a side is expected to post batting first.
//
// This used to be one constant — 165 for any T20, 285 for any ODI. Measured
// across 7,696 first innings, the truth runs from 153.7 (international T20s)
// to 172.5 (MLC), and grounds inside a single league differ by more than
// that: Wankhede 176.2, Sharjah 160.4.
//
// It matters because two of the seven innings-one features are measured
// against par (proj-par and runs/par), so a par that is wrong per-league did
// not just shift one match's answer — it distorted the fitted coefficients
// for every match, by forcing one line through contradictory data. Refitting
// against real pars moved held-out T20 log-loss from 0.6030 to 0.5967.
//
// Built offline from the ball-by-ball archive. Ground means are
// shrunk toward their league mean (K=15, chosen on held-out loss), so a
// ground with four matches does not get to claim its own number.

import (
	_ "embed"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

//go:embed data/par.json
var parJSON []byte

type parTable struct {
	Global map[string]float64 `json:"global"`
	League map[string]float64 `json:"league"`
	Ground map[string]float64 `json:"ground"`
}

var (
	parOnce sync.Once
	parData parTable
)

func parLoad() {
	if err := json.Unmarshal(parJSON, &parData); err != nil {
		parData = parTable{}
	}
}

var parPunct = regexp.MustCompile(`[^a-z0-9 ]`)

// GroundKey normalises a venue string to the ground it names. Cricsheet
// writes one ground several ways ("Warner Park, Basseterre, St Kitts" and
// "Warner Park, Basseterre") and ESPN writes it differently again, so
// everything before the first comma is the only part worth trusting.
// Normalising merged 471 raw venue strings into 319 grounds.
func GroundKey(venue string) string {
	if i := strings.Index(venue, ","); i >= 0 {
		venue = venue[:i]
	}
	return strings.TrimSpace(parPunct.ReplaceAllString(strings.ToLower(venue), ""))
}

// ParFor returns the expected first-innings total for a venue and league at
// this innings length. It falls back ground -> league -> global, so an
// unrecognised ground still gets its league's number, and an unrecognised
// league still beats the old constant. ESPN and Cricsheet disagree on plenty
// of ground spellings; that miss is a soft one by design.
//
// totalOvers scales the answer for shortened games, matching the old
// behaviour: a 10-over innings is half a 20-over par.
func ParFor(venue, league string, totalOvers, bpo int) float64 {
	if bpo == 5 {
		// The Hundred has its own fitted anchor and its own archive.
		return hndPar * float64(totalOvers) / 20.0
	}
	parOnce.Do(parLoad)
	base, full := 0.0, 20
	if totalOvers > 20 {
		full = 50
	}
	suffix := "|" + strconv.Itoa(full)
	if k := GroundKey(venue); k != "" {
		if v, ok := parData.Ground[k+suffix]; ok {
			base = v
		}
	}
	if base == 0 && league != "" {
		if v, ok := parData.League[leagueKey(league)+suffix]; ok {
			base = v
		}
	}
	if base == 0 {
		if v, ok := parData.Global[strconv.Itoa(full)]; ok {
			base = v
		}
	}
	if base == 0 {
		// Table missing entirely: the pre-table constants, so a broken
		// build degrades to the old behaviour rather than to zero.
		if full == 20 {
			base = 165.0
		} else {
			base = 285.0
		}
	}
	return base * float64(totalOvers) / float64(full)
}

// leagueNames maps the competition names ESPN uses to the Cricsheet archive
// keys the par table is built from. Anything unlisted falls through to the
// global figure, which is still better than the old constant.
var leagueNames = map[string]string{
	"indian premier league":       "ipl",
	"big bash league":             "bbl",
	"caribbean premier league":    "cpl",
	"pakistan super league":       "psl",
	"major league cricket":        "mlc",
	"minor league cricket":        "mlc",
	"pakistan super league t20":   "psl",
	"one day internationals":      "odis_male",
	"icc cricket world cup":       "odis_male",
	"icc men's cricket world cup": "odis_male",
}

func leagueKey(league string) string {
	l := strings.ToLower(strings.TrimSpace(league))
	if k, ok := leagueNames[l]; ok {
		return k
	}
	// Substring pass: ESPN decorates names ("Indian Premier League 2026",
	// "Caribbean Premier League Qualifier").
	for name, k := range leagueNames {
		if strings.Contains(l, name) {
			return k
		}
	}
	// International cricket is the archive's largest T20 pool and the one
	// furthest from the old constant, at 153.7 against 165.
	if strings.Contains(l, "tour of") || strings.Contains(l, "tri-series") ||
		strings.Contains(l, "world cup") || strings.Contains(l, "asia cup") {
		return "t20s_male"
	}
	return ""
}
