// Package matchup serves career batter-vs-bowler head-to-heads — the
// cricket version of a baseball broadcast's batter-vs-pitcher card.
// Data is aggregated offline from Cricsheet ball-by-ball archives
// (limited-overs matches; pairs with 12+ legal balls faced) and embedded.
// Regenerate with scripts/winmodel's cricsheet zips:
//
//	python3 scripts/matchupgen.py <zips-dir> > internal/matchup/data/matchups.json
package matchup

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

//go:embed data/matchups.json
var matchupJSON []byte

// pair keys are "fmt|Batter|Bowler" with Cricsheet short names
// ("t20|V Kohli|JJ Bumrah") and values [ballsFaced, runsOffBat, dismissals].
var (
	once  sync.Once
	pairs map[string][3]int
	// names indexes "fmt|initial surname" -> exact Cricsheet names, so
	// ESPN's full names ("Virat Kohli") can resolve to "V Kohli".
	names map[string][]string
)

func load() {
	pairs = map[string][3]int{}
	_ = json.Unmarshal(matchupJSON, &pairs)
	names = map[string][]string{}
	seen := map[string]bool{}
	for k := range pairs {
		parts := strings.SplitN(k, "|", 3)
		if len(parts) != 3 {
			continue
		}
		for _, name := range parts[1:] {
			id := parts[0] + "|" + name
			if seen[id] {
				continue
			}
			seen[id] = true
			nk := parts[0] + "|" + shortKey(name)
			names[nk] = append(names[nk], name)
			// Live commentary uses bare surnames ("Pierre to Fletcher");
			// index those too, marked so they can't shadow full keys.
			sk := parts[0] + "|~" + surnameKey(name)
			names[sk] = append(names[sk], name)
		}
	}
}

// surnameKey drops the leading initials token: "KMA Pierre" -> "pierre",
// "F du Plessis" -> "du plessis", single names pass through.
func surnameKey(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.NewReplacer(".", " ", "-", " ", "'", "").Replace(s)
	tokens := strings.Fields(s)
	if len(tokens) <= 1 {
		return strings.Join(tokens, "")
	}
	return strings.Join(tokens[1:], " ")
}

// shortKey folds any name style to "v kohli": first initial + remaining
// tokens. "Virat Kohli", "V Kohli", and "SP Krishnamurthy"/"Sanjay
// Krishnamurthy" all meet in the middle.
func shortKey(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.NewReplacer(".", " ", "-", " ", "'", "").Replace(s)
	tokens := strings.Fields(s)
	if len(tokens) == 0 {
		return ""
	}
	if len(tokens) == 1 {
		return tokens[0]
	}
	return tokens[0][:1] + " " + strings.Join(tokens[1:], " ")
}

// Result is one head-to-head line.
type Result struct {
	Format     string  `json:"format"`
	Balls      int     `json:"balls"`
	Runs       int     `json:"runs"`
	Outs       int     `json:"outs"`
	StrikeRate float64 `json:"strike_rate"`
}

// Lookup resolves live-feed names against the embedded table. format is
// "t20" or "odi"; empty tries t20 first (the product's main formats).
// Ambiguous name resolutions pick the pairing with the most balls faced;
// missing data returns ok=false and the caller shows nothing.
func Lookup(format, batter, bowler string) (Result, bool) {
	once.Do(load)
	formats := []string{"t20", "odi"}
	if format == "t20" || format == "odi" {
		formats = []string{format}
	}
	resolve := func(f, q string) []string {
		if v := names[f+"|"+shortKey(q)]; len(v) > 0 {
			return v
		}
		return names[f+"|~"+surnameKey(q)]
	}
	for _, f := range formats {
		bats := resolve(f, batter)
		bowls := resolve(f, bowler)
		best, found := Result{}, false
		for _, ba := range bats {
			for _, bo := range bowls {
				if v, ok := pairs[f+"|"+ba+"|"+bo]; ok && v[0] > best.Balls {
					best = Result{Format: f, Balls: v[0], Runs: v[1], Outs: v[2],
						StrikeRate: float64(v[1]) / float64(v[0]) * 100}
					found = true
				}
			}
		}
		if found {
			return best, true
		}
	}
	return Result{}, false
}

// Line renders the card text the UI and the LLM context share — terse,
// scoreboard-style: "Kohli vs Bumrah: 164 off 110, out 5x".
func (r Result) Line(batter, bowler string) string {
	outs := fmt.Sprintf("out %dx", r.Outs)
	switch r.Outs {
	case 0:
		outs = "never out"
	case 1:
		outs = "out once"
	}
	return fmt.Sprintf("%s vs %s: %d off %d, %s",
		surname(batter), surname(bowler), r.Runs, r.Balls, outs)
}

// surname drops the leading given-name/initials token for card brevity;
// single-token names pass through.
func surname(name string) string {
	tokens := strings.Fields(strings.TrimSpace(name))
	if len(tokens) <= 1 {
		return name
	}
	return strings.Join(tokens[1:], " ")
}

// FindPairInText spots two players mentioned in free text ("how does
// Kohli do against Bumrah?") via the surname index and returns their best
// head-to-head. Requiring an actual pair record (12+ balls) filters out
// false surname hits from ordinary words.
func FindPairInText(format, text string) (Result, string, string, bool) {
	once.Do(load)
	norm := strings.NewReplacer("?", " ", ",", " ", ".", " ", "'s", "").Replace(strings.ToLower(text))
	var cands []string
	for _, w := range strings.Fields(norm) {
		if len(w) < 4 {
			continue
		}
		for _, f := range []string{"t20", "odi"} {
			if len(names[f+"|~"+w]) > 0 {
				dup := false
				for _, c := range cands {
					if c == w {
						dup = true
						break
					}
				}
				if !dup {
					cands = append(cands, w)
				}
				break
			}
		}
	}
	best, bestA, bestB, found := Result{}, "", "", false
	for i := 0; i < len(cands); i++ {
		for j := 0; j < len(cands); j++ {
			if i == j {
				continue
			}
			if r, ok := Lookup(format, cands[i], cands[j]); ok && r.Balls > best.Balls {
				best, bestA, bestB, found = r, cands[i], cands[j], true
			}
		}
	}
	if !found {
		return Result{}, "", "", false
	}
	return best, strings.Title(bestA), strings.Title(bestB), true
}
