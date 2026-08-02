// Package glossary is the cricket-to-baseball translation layer.
package glossary

import (
	"fmt"
	"sort"
	"strings"
)

// Entry is one glossary item.
type Entry struct {
	Term     string `json:"term"`
	Baseball string `json:"baseball"`
	Plain    string `json:"plain"`
}

// Terms maps cricket term -> {baseball parallel, plain-English explanation}.
var Terms = map[string]Entry{
	"silly mid-on": {Baseball: "charging the batter on a bunt, but permanently",
		Plain: "An extremely close catching position a few feet from the batter on the leg side, waiting for the ball to pop off bat or pad. 'Silly' = dangerously close; it is NOT the ordinary mid-on run-saver."},
	"silly mid-off": {Baseball: "crashing the infield grass, off side",
		Plain: "Silly mid-on's off-side twin: a very close bat-pad catcher near the batter, not a regular inner-ring fielder."},
	"toss": {Baseball: "home-field choice, decided by a coin",
		Plain: "Before every match the two captains flip a coin; the winner chooses to bat or field first. It matters because pitches change through a game — day-night dew or a wearing Test surface can make one choice a real edge."},
	"long on": {Baseball: "straightaway left-center, at the wall",
		Plain: "Boundary fielder nearly straight down the ground on the LEG side (a right-hander's left as they face the bowler). Catches the big straight hits."},
	"long off": {Baseball: "straightaway right-center, at the wall",
		Plain: "Boundary fielder nearly straight down the ground on the OFF side — long on's mirror image."},
	"third man": {Baseball: "deep behind first base",
		Plain: "Boundary fielder behind square on the off side, behind the keeper's off shoulder. Collects edges and late cuts."},
	"fine leg": {Baseball: "deep behind third base",
		Plain: "Boundary fielder behind square on the leg side. Collects glances, hooks, and balls off the pads."},
	"gully": {Baseball: "a deep shortstop for edges",
		Plain: "Off-side catcher between the slips and point, waiting for thick edges that fly wide of the slip cordon."},
	"square leg": {Baseball: "third-base side, square to the batter",
		Plain: "Leg-side fielder level with the batter's crease. The square-leg umpire stands there too."},
	"mid-on": {Baseball: "shallow left-center, straight",
		Plain: "Inner-ring fielder on the leg side, fairly straight — saves the firm push down the ground."},
	"mid-off": {Baseball: "shallow right-center, straight",
		Plain: "Inner-ring fielder on the off side, fairly straight — mid-on's mirror."},
	"obstructing the field": {Baseball: "interference, called on the batter",
		Plain: "A dismissal: the batter deliberately blocks or impedes fielders — swatting the ball away from a catch, blocking a throw, or changing course to shield the stumps. Rare and dramatic. (It absorbed the old 'handled the ball' law in 2017.)"},
	"mankad": {Baseball: "picking off a runner leading too far",
		Plain: "Run out of the NON-striker by the bowler for backing up too early, before the ball is bowled. Fully legal, forever controversial."},
	"timed out": {Baseball: "forfeit at-bat for stalling",
		Plain: "A new batter must be ready within about 2-3 minutes of the previous wicket or they can be given out without facing a ball. Nearly never happens."},
	"bye": {Baseball: "passed ball",
		Plain: "Runs taken when the ball passes the batter touching NEITHER bat nor body. Charged against the keeper, not the bowler."},
	"leg bye": {Baseball: "hit-by-pitch that stays live",
		Plain: "Runs taken after the ball hits the batter's BODY (not the bat) — allowed only if they were playing a shot or avoiding the ball."},
	"cover": {Baseball: "the second-baseman zone, off side",
		Plain: "Fielding position on the off side, forward of square, guarding the classic drive. 'Extra cover' is one notch wider."},
	"midwicket": {Baseball: "shallow left-center",
		Plain: "Leg-side fielding position across from cover. 'Deep midwicket' is the same line pushed back to the boundary rope."},
	"net run rate": {Baseball: "run differential, cricket-style",
		Plain: "Tournament tiebreaker: runs scored per over minus runs conceded per over, across all matches. Win fast to boost it; a narrow loss still hurts it, just less."},
	"wicket": {Baseball: "an out (also: the strike zone made physical, and the field itself)",
		Plain: "Three meanings: (1) getting a batter out ('a wicket falls'), (2) the three wooden stumps behind the batter, (3) the strip of dirt they play on. In scores like 145/3, the 3 is wickets — think outs."},
	"batter": {Baseball: "batter",
		Plain: "Same job. Two batters are on the field at once, one at each end of the pitch; they swap ends when they run."},
	"bowler": {Baseball: "pitcher",
		Plain: "Throws the ball at the batter — but with a straight arm, usually bouncing it off the ground first."},
	"over": {Baseball: "a pitcher's 6-pitch mini-inning",
		Plain: "Six legal deliveries by one bowler. Bowlers rotate every over like relief pitchers on a strict schedule. '16.2 overs' means 16 overs plus 2 balls — not 16.2 in decimal."},
	"innings": {Baseball: "one giant half-inning",
		Plain: "Each team usually bats once (in T20). An innings ends after 20 overs (120 pitches) or when 10 outs are recorded, whichever comes first."},
	"run": {Baseball: "run",
		Plain: "Batters score by sprinting between the two ends of the pitch. One length = 1 run, and they can keep running for 2 or 3."},
	"four": {Baseball: "ground-rule double energy",
		Plain: "Ball reaches the boundary rope after touching the ground: automatic 4 runs."},
	"six": {Baseball: "home run",
		Plain: "Ball clears the boundary on the fly: automatic 6 runs."},
	"boundary": {Baseball: "outfield fence",
		Plain: "The rope around the field. 'Finding the boundary' = hitting fours and sixes."},
	"bowled": {Baseball: "strikeout, swinging through one",
		Plain: "The ball beats the batter and knocks over the stumps. Out."},
	"caught": {Baseball: "flyout / lineout",
		Plain: "Any hit caught on the fly is out — there's no foul territory, so edges behind the batter count too."},
	"lbw": {Baseball: "called strike three (umpire's judgment)",
		Plain: "Leg Before Wicket: the ball would have hit the stumps but the batter's leg blocked it. Out, by umpire or replay ruling."},
	"run out": {Baseball: "force out / tag out on the bases",
		Plain: "Fielders break the stumps while a batter is mid-run and short of the crease. Out."},
	"stumped": {Baseball: "caught stealing",
		Plain: "Batter steps out of the crease to swing, misses, and the keeper (catcher) breaks the stumps. Out."},
	"duck": {Baseball: "an 0-fer, but worse",
		Plain: "Out for zero runs. A 'golden duck' is out on the first ball faced."},
	"wide": {Baseball: "ball (way outside the zone)",
		Plain: "Delivery too wide to hit: 1 free run and the ball is re-bowled."},
	"no-ball": {Baseball: "balk / illegal pitch",
		Plain: "Illegal delivery (usually overstepping the line): 1 free run, re-bowled, and in T20 the next ball is a 'free hit' — the batter can't be out except by run out."},
	"free hit": {Baseball: "a do-over where you can't strike out",
		Plain: "Penalty ball after a no-ball where almost no dismissal counts. Batters swing for the fences."},
	"stumps": {Baseball: "the three sticks behind the batter; as a status, 'game suspended till tomorrow'",
		Plain: "Two meanings: the three wooden sticks the bowler is aiming at, and — as a match status — the end of a day's play in a multi-day Test. Stumps means play is paused overnight, not finished and not live."},
	"powerplay": {Baseball: "infield-in, mandated",
		Plain: "First 6 overs of a T20: only 2 fielders allowed deep. Batters attack because gaps in the outfield are open."},
	"strike rate": {Baseball: "slugging pace",
		Plain: "Batting: runs per 100 balls faced. 150+ is aggressive in T20. For bowlers it means balls per wicket (lower = better)."},
	"economy": {Baseball: "ERA",
		Plain: "Runs a bowler concedes per over. Under 7 is good in T20."},
	"all-rounder": {Baseball: "two-way player (Ohtani)",
		Plain: "Genuinely good at both batting and bowling."},
	"keeper": {Baseball: "catcher",
		Plain: "Wicketkeeper: gloved fielder behind the stumps."},
	"slip": {Baseball: "shortstop for edges",
		Plain: "Fielder(s) next to the keeper waiting for the ball to clip the bat's edge. Named for the batter's 'slip' — the mistake/edged ball they pounce on, not for fielders slipping anywhere."},
	"crease": {Baseball: "the base/batter's box line",
		Plain: "The safe line. Bat or body grounded behind it = safe from run outs and stumpings."},
	"maiden over": {Baseball: "1-2-3 inning",
		Plain: "An over where the bowler concedes zero runs. Rare and prized in T20."},
	"yorker": {Baseball: "unhittable low fastball at the shoe tops",
		Plain: "Delivery aimed at the batter's feet/base of the stumps. The classic clutch pitch at the death of an innings."},
	"bouncer": {Baseball: "high-and-tight brushback",
		Plain: "Short delivery that rears up at the batter's head/chest."},
	"super over": {Baseball: "extra innings",
		Plain: "Tie-breaker: each side gets one extra over, best score wins."},
	"googly": {Baseball: "screwball — breaks the wrong way",
		Plain: "A leg-spinner's surprise ball that turns INTO the right-handed batter instead of away, disguised with the same wrist action. The whole point is the batter reads it wrong."},
	"hat-trick": {Baseball: "striking out the side on 9 pitches, but rarer",
		Plain: "One bowler takes three wickets on three consecutive deliveries. Can span overs or even innings."},
	"silly point": {Baseball: "playing on the infield grass, absurdly close",
		Plain: "A fielder crouched a few feet from the batter on the off side, catching balls that pop off bat or pad. 'Silly' because it is a dangerous place to stand."},
	"declared": {Baseball: "voluntarily ending your at-bats to chase the win",
		Plain: "In Tests, the batting captain can end the innings early (e.g. 450/6 declared) to leave time to bowl the other side out. Trades runs for outs-in-time."},
	"the hundred": {Baseball: "a 7-inning game: shorter, same sport",
		Plain: "England's 100-ball format: each side faces exactly 100 balls, bowled in 5-ball sets (not 6-ball overs). A bowler can bowl 5 or 10 in a row. Everything else plays like T20."},
	"target": {Baseball: "the number to beat, plus one",
		Plain: "What the chasing team must score to win: the first innings total plus 1. 'Target 171' means 171 wins it."},
	"dls": {Baseball: "official game / rain-shortened ruling",
		Plain: "Duckworth-Lewis-Stern: the math that resets the target when rain shortens a game. Bets often settle on the DLS result."},
	"required run rate": {Baseball: "runs-per-inning pace you need while trailing late",
		Plain: "In a chase: runs still needed divided by overs left. The single most important live-betting number — compare it to the current run rate to see who's ahead."},
	"death overs": {Baseball: "the 9th inning",
		Plain: "The final overs (17-20 in T20) where batters go all-out and yorker specialists bowl."},
	"t20": {Baseball: "the 3-hour format",
		Plain: "20 overs (120 pitches) per side, ~3 hours. What MLC and Minor League Cricket play. There are also ODIs (50 overs, all day) and Tests (5 days)."},
}

// Lookup finds an entry by exact or substring match; nil if none.
func isWordChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
}

// containsPhrase reports whether phrase occurs in s on word boundaries —
// "over" must not match inside "cover", "wicket" not inside "midwicket".
func containsPhrase(s, phrase string) bool {
	for idx := 0; idx+len(phrase) <= len(s); {
		i := strings.Index(s[idx:], phrase)
		if i < 0 {
			return false
		}
		start := idx + i
		end := start + len(phrase)
		if (start == 0 || !isWordChar(s[start-1])) && (end == len(s) || !isWordChar(s[end])) {
			return true
		}
		idx = start + 1
	}
	return false
}

func sortedNames() []string {
	names := make([]string, 0, len(Terms))
	for name := range Terms {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func Lookup(term string) *Entry {
	key := strings.ToLower(strings.TrimSpace(term))
	if e, ok := Terms[key]; ok {
		e.Term = key
		return &e
	}
	// Word-boundary phrase fallback, longest name wins: "net run rate"
	// resolves to its own entry, never "run".
	best := ""
	for _, name := range sortedNames() {
		if len(name) <= len(best) {
			continue
		}
		// name-inside-key needs the name to carry at least half the key,
		// or "over" would hijack "over-rate penalties".
		if containsPhrase(key, name) && len(name)*2 >= len(key) {
			best = name
		} else if containsPhrase(name, key) {
			best = name
		}
	}
	if best == "" {
		return nil
	}
	e := Terms[best]
	e.Term = best
	return &e
}

// FindInText returns the longest glossary term present (word-bounded)
// anywhere in free text — for "WICKET WICKET what does it mean" asks.
func FindInText(text string) *Entry {
	t := strings.ToLower(text)
	best := ""
	for _, name := range sortedNames() {
		if len(name) > len(best) && containsPhrase(t, name) {
			best = name
		}
	}
	if best == "" {
		return nil
	}
	e := Terms[best]
	e.Term = best
	return &e
}

// CheatSheet renders the full glossary as text (rule-based paths, docs).
func CheatSheet() string {
	names := make([]string, 0, len(Terms))
	for name := range Terms {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		e := Terms[name]
		fmt.Fprintf(&b, "- %s — baseball: %s. %s\n", name, e.Baseball, e.Plain)
	}
	return strings.TrimRight(b.String(), "\n")
}

// CompactSheet is the token-thrifty version for the LLM system prompt:
// term -> baseball parallel only. Keeps per-request tokens low enough for
// free-tier TPM caps (Groq: 12K/min).
func CompactSheet() string {
	names := make([]string, 0, len(Terms))
	for name := range Terms {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, "%s=%s; ", name, Terms[name].Baseball)
	}
	return strings.TrimRight(b.String(), "; ")
}
