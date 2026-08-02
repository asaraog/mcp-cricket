// Package commentary decodes ball-by-ball commentary jargon for newcomers.
package commentary

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Terms maps commentary jargon -> plain-English (baseball-flavored) translation.
var Terms = map[string]string{
	// line
	"middle and leg":    "aimed between the middle of the stumps and the batter's legs",
	"outside off":       "wide of the stumps on the batter's far side — the outer edge of the plate",
	"on the stumps":     "dead straight — would hit the stumps",
	"down the leg side": "drifting behind the batter's legs — well inside",
	// length
	"good length":      "the awkward in-between bounce point — hardest to attack",
	"decent length":    "the awkward in-between bounce point — hardest to attack",
	"back of a length": "bounced slightly short — arrives chest-high",
	"full toss":        "reached the batter without bouncing — usually a gift",
	"half volley":      "bounced right at the bat — easy to drive",
	"yorker":           "aimed at the batter's shoe tops — the clutch pitch",
	"bouncer":          "short and rearing at the head — the brushback",
	"full":             "pitched up close to the batter",
	"short":            "bounced halfway down — sits up chest/head high",
	// movement
	"off the seam":  "deviated sideways after bouncing",
	"seam movement": "sideways deviation off the bounce",
	"swing":         "curving in the air, like a two-seamer tailing",
	"turn":          "spin-induced sideways break off the pitch",
	// shots
	"punched":  "hit firmly with a short, controlled swing",
	"driven":   "hit with a full classic swing along the ground",
	"drives":   "hits with a full classic swing along the ground",
	"worked":   "nudged with soft hands into a gap — bat control over power",
	"flicked":  "wristy turn of the ball off the legs",
	"pulled":   "swiveled and hit a short ball to the leg side — like turning on an inside pitch",
	"cut":      "slashed a wide ball square on the off side",
	"swept":    "knelt and swung flat across a spinner",
	"defends":  "blocks it dead — no run intended",
	"defended": "blocked dead — no run intended",
	"leaves":   "shoulders it — lets it pass like taking a ball",
	// outcomes
	"outside edge":               "ball clipped the outer side of the bat — like a foul tip",
	"inside edge":                "ball clipped the inner side of the bat, often nearly out",
	"thickest of outside edges":  "a big foul-tip that flew off the bat's outer side",
	"play and a miss":            "swung and missed",
	"beaten":                     "swung/missed or completely misjudged it — a whiff",
	"dot ball":                   "no runs scored off that delivery — a clean strike for the bowler",
	// fielding positions
	"midwicket":  "leg-side fielder roughly where a deep second-base spot would be",
	"mid-on":     "straight-ish fielder on the leg side, close to the bowler",
	"mid-off":    "straight-ish fielder on the off side, close to the bowler",
	// no bare "cover": it false-positives on the verb ("cover the line")
	"the covers": "off-side zone between point and mid-off — cricket's shortstop territory",
	"covers":     "off-side zone between point and mid-off — cricket's shortstop territory",
	"point":      "square fielder on the off side, level with the batter",
	"gully":      "between point and the slips — catches thick edges",
	"slip":       "next to the keeper waiting for edges",
	"fine leg":   "behind the batter on the leg side, near the boundary",
	"square leg": "level with the batter on the leg side",
	"third man":  "behind the batter on the off side, near the boundary",
	"long-on":    "straight leg-side outfielder on the boundary",
	"long-off":   "straight off-side outfielder on the boundary",
	"deep":       "prefix meaning 'back near the boundary' — outfield depth",
	// footwork
	"front foot": "stepping toward the ball to play it",
	"back foot":  "rocking back toward the stumps to play it",
}

// Jargon is one decoded term found in a commentary line.
type Jargon struct {
	Term  string `json:"term"`
	Plain string `json:"plain"`
}

type pattern struct {
	term string
	re   *regexp.Regexp
}

var patterns []pattern

func init() {
	terms := make([]string, 0, len(Terms))
	for t := range Terms {
		terms = append(terms, t)
	}
	// Longest first so multi-word jargon beats its substrings; ties by name
	// for determinism.
	sort.Slice(terms, func(i, j int) bool {
		if len(terms[i]) != len(terms[j]) {
			return len(terms[i]) > len(terms[j])
		}
		return terms[i] < terms[j]
	})
	for _, t := range terms {
		patterns = append(patterns, pattern{
			term: t,
			re:   regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(t) + `\b`),
		})
	}
}

// Annotate finds known jargon in a commentary line. Each text region is
// claimed by the longest matching term only.
func Annotate(text string) []Jargon {
	var found []Jargon
	var claimed [][2]int
	seen := map[string]bool{}
	for _, p := range patterns {
		for _, m := range p.re.FindAllStringIndex(text, -1) {
			overlaps := false
			for _, c := range claimed {
				if m[0] < c[1] && c[0] < m[1] {
					overlaps = true
					break
				}
			}
			if overlaps {
				continue
			}
			claimed = append(claimed, [2]int{m[0], m[1]})
			if !seen[p.term] {
				seen[p.term] = true
				found = append(found, Jargon{Term: p.term, Plain: Terms[p.term]})
			}
		}
	}
	return found
}

// CheatSheet renders a compact decoder for the LLM system prompt.
func CheatSheet() string {
	terms := make([]string, 0, len(Terms))
	for t := range Terms {
		terms = append(terms, t)
	}
	sort.Strings(terms)
	var b strings.Builder
	b.WriteString("Commentary jargon decoder (line/length/shots/fielding positions):\n")
	for _, t := range terms {
		fmt.Fprintf(&b, "- %s: %s\n", t, Terms[t])
	}
	return strings.TrimRight(b.String(), "\n")
}
