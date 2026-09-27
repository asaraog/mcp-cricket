package milclive

// What state a game is in, derived from its data and a clock and nothing
// else. Every function here is pure, so a whole Saturday can be replayed
// from a capture.
//
// The rule that shapes all of it: state comes from widget_status, the
// closed status and the winner — never from the free-text status. The text
// is written for a broadcast overlay, not a program. At the innings break on
// 2026-09-26 Atlanta Fire v Atlanta Lightning read "Play to Restart Shortly
// - Match Stable" with the widget still "live"; the New England Eagles game
// read widget "pre" with status "Rain Delay" an hour and a half after it was
// due to start; and Chicago Kingsmen v Chicago Tigers read "live" with
// had_scoring false after the toss. A reading of the text alone dropped
// live games at every break.

import (
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/asaraog/mcp-cricket/internal/explainer"
)

var (
	maxOvRe    = regexp.MustCompile(`(?i)MaxOv:\s*(\d{1,2})`)
	targetRe   = regexp.MustCompile(`(?i)Target:\s*(\d{1,3})`)
	requireRe  = regexp.MustCompile(`(?i)^\s*(.+?) require (-?\d+) runs? from (\d+) balls?\.?\s*$`)
	forAfterRe = regexp.MustCompile(`(?i)^\s*(.+?) are (\d{1,3}) for (\d{1,2}) after (\d{1,2}(?:\.\d)?) overs?\.?\s*$`)
	tossRe     = regexp.MustCompile(`(?i)^\s*(.+?) won the toss and (?:have |has )?(?:elected|chosen|opted) to (bat|field|bowl)`)
	resultRe   = regexp.MustCompile(`(?i)^\s*match complete\s*-\s*(.+?)\s*$`)
	delayRe    = regexp.MustCompile(`(?i)\b(rain|delay(?:ed)?|toss pending|wet|weather|bad light)\b`)
	restartRe  = regexp.MustCompile(`(?i)restart shortly`)
	tickerRe   = regexp.MustCompile(`^[A-Z0-9]+-(\d{2}[A-Z]{3}\d{6})`)
	oversRe    = regexp.MustCompile(`^\d{1,2}(\.\d)?$`)
	wonByRe    = regexp.MustCompile(`(?i)\b(?:have|has) won by\b`)

	// betDelayRe matches the exchange's own suspension notice, which rides
	// on nearly every status string: "Match in Progress - Match Stable (Bet
	// Delay:4 seconds)". It says how long Kalshi holds an order, not
	// anything about the cricket, and delayRe reads its "Delay" as a delayed
	// match — every live game in the capture would have been "Delayed".
	betDelayRe = regexp.MustCompile(`(?i)\(?\s*bet\s+delay\s*:?\s*\d*\s*(?:seconds?|secs?|s)?\s*\)?`)
)

var eastern = mustLocation("America/New_York")

func mustLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}

// Eastern is the New York zone every "ET" time is written in.
func Eastern() *time.Location { return eastern }

// TickerStart reads the start encoded in an event ticker:
// KXT20MATCH-26SEP261500CHITIGCHKI is 3:00 PM New York time on 26 Sep 2026.
// Go parses month names case-insensitively, so "SEP" reads as September.
func TickerStart(t string) (time.Time, bool) {
	m := tickerRe.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(t)))
	if m == nil {
		return time.Time{}, false
	}
	ts, err := time.ParseInLocation("06Jan021504", m[1], eastern)
	if err != nil {
		return time.Time{}, false
	}
	return ts.UTC(), true
}

// ParseOvers turns cricket notation into balls: "19.3" is 117. The digit
// after the point counts balls and never exceeds 5, so "15.7" is refused
// rather than read as 97, and so is "5.83".
func ParseOvers(s string) (balls int, ok bool) {
	s = strings.TrimSpace(s)
	if !oversRe.MatchString(s) {
		return 0, false
	}
	whole, frac, _ := strings.Cut(s, ".")
	o, err := strconv.Atoi(whole)
	if err != nil {
		return 0, false
	}
	b := 0
	if frac != "" {
		b = int(frac[0] - '0')
		if b > 5 {
			return 0, false
		}
	}
	return o*6 + b, true
}

// ------------------------------------------------------------------ starts

// DisplayStart is the start a reader is shown. Kalshi's two starts usually
// agree; when they differ by more than an hour the EARLIER is shown and
// flagged, because a reader who arrives early loses nothing and one who
// arrives four hours late has missed the game.
func (f Fixture) DisplayStart() time.Time {
	if f.TickerStart.IsZero() || absDur(f.TickerStart.Sub(f.Start)) <= time.Hour {
		return f.Start
	}
	return f.EarliestStart()
}

// EarliestStart is the earlier of the two starts.
func (f Fixture) EarliestStart() time.Time {
	if !f.TickerStart.IsZero() && f.TickerStart.Before(f.Start) {
		return f.TickerStart
	}
	return f.Start
}

// StartUnconfirmed reports that the two starts differ by more than an hour.
func (f Fixture) StartUnconfirmed() bool {
	return !f.TickerStart.IsZero() && absDur(f.TickerStart.Sub(f.Start)) > time.Hour
}

// OtherStart is the start that is NOT displayed when the two disagree, and
// whether it is the market's (ticker) start.
func (f Fixture) OtherStart() (t time.Time, fromTicker bool) {
	if !f.StartUnconfirmed() {
		return time.Time{}, false
	}
	if f.DisplayStart().Equal(f.Start) {
		return f.TickerStart, true
	}
	return f.Start, false
}

// latestStart is the later of the two starts.
func (f Fixture) latestStart() time.Time {
	if f.TickerStart.After(f.Start) {
		return f.TickerStart
	}
	return f.Start
}

// betweenStarts reports a moment after the earlier of two disagreeing
// starts and before the later one: the game may be under way, or not due
// for hours, and nothing in the schedule says which.
func (f Fixture) betweenStarts(now time.Time) bool {
	return f.StartUnconfirmed() && !now.Before(f.EarliestStart()) && now.Before(f.latestStart())
}

// ---------------------------------------------------------------- freshness

// Usable reports live data recent enough to state: ten minutes. A final
// score cannot go stale, so a finished game's last data stays usable after
// the batch stops asking about it.
func (g Game) Usable(now time.Time) bool {
	if g.Live == nil {
		return false
	}
	return now.Sub(g.Live.FetchedAt) <= 10*time.Minute || g.Finished()
}

// Fresh reports live data recent enough to price: one minute.
func (g Game) Fresh(now time.Time) bool {
	return g.Live != nil && now.Sub(g.Live.FetchedAt) <= 60*time.Second
}

var conflictLogged sync.Map

// OrientationOK reports that the live data's home and away are the
// milestone's. A swap would put one side's score on the other's name, so
// a disagreement is logged and the score withheld. A scoreboard that names
// neither side (Kings XI Dallas appears as "All Stars") is not evidence of
// a swap and passes.
func (g Game) OrientationOK() bool {
	l := g.Live
	if l == nil {
		return false
	}
	if l.Orientation != "" && l.Orientation != "milestone" {
		return false
	}
	h, a := Canon(g.Fixture.Home), Canon(g.Fixture.Away)
	sbh, sba := Canon(l.SBHome), Canon(l.SBAway)
	if (sbh != "" && sbh == h) || (sba != "" && sba == a) {
		return true
	}
	if (sbh != "" && sbh == a) || (sba != "" && sba == h) {
		if _, seen := conflictLogged.LoadOrStore(g.ID, true); !seen {
			log.Printf("milc: orientation conflict %s", g.ID)
		}
		return false
	}
	return true
}

// Scored reports a score that may be shown.
func (g Game) Scored(now time.Time) bool {
	return g.Usable(now) && g.Live.HadScoring && g.OrientationOK()
}

// Finished reports a game the data says is over: the widget, the closed
// status or the winner — or a chase that has reached its target.
//
// The last is arithmetic, not text, and it is there because the three
// fields lag the cricket. When The Philadelphians hit the winning runs on
// 2026-09-26 (68/0 chasing 68, 21:02Z), the widget stayed "live" and the
// winner "" for another 75 seconds, while the status already read "Match
// Complete - ". For that minute the row stayed 🔴, the card read "68/0
// (6/10 ov, target 68)" and chat said the model "doesn't price this
// chase". The free text is still never read for state; the score is.
func (g Game) Finished() bool { return finished(g.Live) || g.targetReached() }

func finished(l *Live) bool {
	return l != nil && (l.Widget == "finished" || strings.EqualFold(l.Status, "closed") || l.Winner != "")
}

// targetReached reports a second innings whose side batting now has scored
// at least the target. The target is one this data states outright — the
// reduced-game Target from the status, else the first innings plus one once
// that innings is demonstrably complete — never one read off the scorer's
// "require" line, which can move ahead of the score it describes. It takes
// no clock, because Usable asks Finished and a final score cannot go stale.
func (g Game) targetReached() bool {
	l := g.Live
	if l == nil || l.Innings < 2 || !l.HadScoring || !g.OrientationOK() {
		return false
	}
	bat, _, ok := g.battingSides()
	if !ok || !bat.OversOK || !batted(bat) {
		return false
	}
	first, second, ok := g.Sides()
	if !ok || second != bat {
		return false
	}
	target := g.StatusTarget
	if target == 0 {
		total, totalOK := g.TotalOvers()
		if !firstInningsComplete(first, total, totalOK) {
			return false
		}
		target = first.Runs + 1
	}
	return bat.Runs >= target
}

// ------------------------------------------------------------------- state

// cleanStatus strips the exchange's "(Bet Delay:4 seconds)" notice.
func cleanStatus(s string) string {
	return strings.TrimSpace(betDelayRe.ReplaceAllString(s, " "))
}

// delayTexts are the strings that can announce a delay. The milestone's
// status is read only when there is no live record: it is cached for five
// minutes, and a stale "Toss Pending" there would otherwise hold a game
// that is under way at "Toss delayed".
func (g Game) delayTexts() []string {
	if l := g.Live; l != nil {
		return []string{cleanStatus(l.Status), cleanStatus(l.StatusText), l.LastPlay, l.MatchComment}
	}
	return []string{cleanStatus(g.Fixture.Status)}
}

// delayWords returns the delay words present, lower-cased, in order.
func (g Game) delayWords() []string { return delayMatches(g.delayTexts()) }

// delayWordsAt is delayWords from data recent enough to state. Live data
// more than ten minutes old has had its score dropped, and its "Rain Delay"
// is no more current than the score was; the milestone's status, refreshed
// every five minutes, speaks for the game instead.
func (g Game) delayWordsAt(now time.Time) []string {
	if g.Live != nil && !g.Usable(now) {
		return delayMatches([]string{cleanStatus(g.Fixture.Status)})
	}
	return g.delayWords()
}

func delayMatches(texts []string) []string {
	var out []string
	for _, t := range texts {
		for _, m := range delayRe.FindAllString(t, -1) {
			out = append(out, strings.ToLower(m))
		}
	}
	return out
}

func (g Game) hasDelay() bool { return len(g.delayWords()) > 0 }

// onlyTossPending reports delay text that is nothing but "Toss Pending".
func onlyTossPending(words []string) bool {
	for _, w := range words {
		if w != "toss pending" {
			return false
		}
	}
	return len(words) > 0
}

// interruptionSeen reports rain, weather or a delay in this game's data at
// or after its start. "Toss Pending" alone is not one: Kalshi posts it
// before every toss (Lone Star Athletics read it 24 minutes before an
// on-time start), and a toss a few minutes late costs no overs. The cache
// remembers the answer (Game.Interrupted), because the delay text comes and
// goes while the overs it cost stay cut.
func (g Game) interruptionSeen(now time.Time) bool {
	if now.Before(g.DisplayStart()) {
		return false
	}
	for _, w := range g.delayWords() {
		if w != "toss pending" {
			return true
		}
	}
	return false
}

// State is pre, in or post.
func (g Game) State(now time.Time) string {
	if g.Finished() {
		return "post"
	}
	ds := g.DisplayStart()
	if !g.Usable(now) {
		switch {
		case now.Before(ds):
			return "pre"
		case !now.After(ds.Add(3*time.Hour + 30*time.Minute)):
			return "in"
		default:
			return "post"
		}
	}
	if g.Live.Widget == "live" {
		return "in"
	}
	if !now.Before(ds) && g.hasDelay() {
		// Between two disagreeing starts, "Toss Pending" is what Kalshi
		// posts before any toss, so it says nothing about a delay. Chicago
		// Kingsmen v St Louis Americans (Sep 27) is listed for 15:00Z and
		// its ticker for 19:00Z; read as a delay from 15:00Z, the card
		// announced "Toss delayed" for a game likely not due until noon PT.
		if g.betweenStarts(now) && onlyTossPending(g.delayWords()) {
			return "pre"
		}
		return "in"
	}
	return "pre"
}

// Phase names a pause in a game that is in progress, or "" when play is on.
// A game that has not started or has finished has no phase.
func (g Game) Phase(now time.Time) string {
	if g.State(now) != "in" {
		return ""
	}
	words := strings.Join(g.delayWordsAt(now), " ")
	switch {
	case strings.Contains(words, "rain"):
		return "Rain delay"
	case strings.Contains(words, "toss pending") && !now.Before(g.DisplayStart()):
		return "Toss delayed"
	case words != "":
		return "Delayed"
	}
	l := g.Live
	if l == nil || !g.Usable(now) {
		return ""
	}
	if bat, _, ok := g.battingSides(); ok && l.Widget == "live" && l.Innings == 2 && l.HadScoring && bat.Balls == 0 {
		return "Innings break"
	}
	if restartRe.MatchString(l.Status) || restartRe.MatchString(l.StatusText) {
		return "Break in play"
	}
	if l.Widget == "live" && !l.HadScoring {
		return "Starting soon"
	}
	return ""
}

// Timing says whether the start is ahead, passed with play begun, or
// passed with nothing begun yet.
func (g Game) Timing(now time.Time) string {
	if now.Before(g.DisplayStart()) {
		return "future"
	}
	if l := g.Live; (l != nil && (l.Widget == "live" || l.Widget == "finished")) || g.State(now) == "post" {
		return "started"
	}
	return "due"
}

// InWindow reports whether the game belongs in the match list now. It
// appears three hours before the earlier of Kalshi's two starts and leaves
// two hours after its result.
func (g Game) InWindow(now time.Time) bool {
	if now.Before(g.EarliestStart().Add(-3 * time.Hour)) {
		return false
	}
	ds := g.DisplayStart()
	switch g.State(now) {
	case "in":
		// Play is on: always listed. A game held "in" only by delay text
		// with the widget still "pre" gets the delayed game's allowance,
		// not forever.
		if g.Usable(now) && g.Live.Widget != "live" && g.hasDelay() {
			return now.Before(ds.Add(8 * time.Hour))
		}
		return true
	case "post":
		if !g.Finished() {
			// Over by the clock alone: no result was ever seen.
			return now.Before(ds.Add(5*time.Hour + 30*time.Minute))
		}
		fin := g.FinishedAt
		if g.FinishedOnFirstSight || fin.IsZero() {
			fin = ds.Add(4 * time.Hour)
		}
		return now.Before(fin.Add(2 * time.Hour))
	default:
		if g.hasDelay() {
			return now.Before(ds.Add(8 * time.Hour))
		}
		return now.Before(ds.Add(4 * time.Hour))
	}
}

// TotalOvers is the innings length the status alone supports: MaxOv for a
// reduced game, unknown while a delay is on (the overs are being cut and
// the new number is not out yet) or once this process has seen the game
// interrupted, otherwise the 20 of a T20.
//
// "Once seen" is the part that matters. Kalshi publishes MaxOv for a
// reduced game only some of the time: New England Eagles v New Jersey
// Somerset Cavaliers was cut to 7 overs on 2026-09-26 and never carried it,
// and from 20:54 to 21:17Z its status flipped between "Rain Delay", "Ball
// in Progress" and "Markets Suspended" every 15 to 30 seconds. Keyed on
// the delay word alone, the length read 20 whenever the word was missing,
// and the card flipped between "(2.3 ov)" and "(2.3/20 ov)" in a 7-over
// chase.
func (g Game) TotalOvers() (int, bool) {
	if g.MaxOv > 0 && g.MaxOv <= 20 {
		return g.MaxOv, true
	}
	if g.hasDelay() || g.Interrupted {
		return 0, false
	}
	return 20, true
}

// InningsOvers is the length of the innings in play: MaxOv when the status
// gives it, else in the chase the length the scorer's require line proves
// (balls bowled plus balls left), else TotalOvers. Everything that prints or
// counts an innings length reads it here, so the score line cannot say
// "/20" while the need line counts from 42 balls.
func (g Game) InningsOvers(now time.Time) (int, bool) {
	if g.MaxOv > 0 && g.MaxOv <= 20 {
		return g.MaxOv, true
	}
	if l := g.Live; l != nil && l.Innings >= 2 {
		if st, ok := g.ChaseState(now); ok {
			return st.TotalOvers, true
		}
	}
	return g.TotalOvers()
}

// ------------------------------------------------------------------- sides

// sideOf maps a team name, in either the milestone's or the scoreboard's
// spelling, to "home" or "away".
func (g Game) sideOf(name string) string {
	l := g.Live
	c := Canon(name)
	if c == "" || c == "0" || l == nil {
		return ""
	}
	switch c {
	case Canon(g.Fixture.Home), Canon(l.SBHome):
		return "home"
	case Canon(g.Fixture.Away), Canon(l.SBAway):
		return "away"
	}
	return ""
}

// battingKey is "home" or "away" for the side batting now.
func (g Game) battingKey() string {
	l := g.Live
	if l == nil {
		return ""
	}
	if l.Batting == "home" || l.Batting == "away" {
		return l.Batting
	}
	return g.sideOf(l.SBBatting)
}

// battingSides returns the side batting now and the side bowling.
func (g Game) battingSides() (bat, bowl Side, ok bool) {
	l := g.Live
	if l == nil {
		return Side{}, Side{}, false
	}
	switch g.battingKey() {
	case "home":
		return l.Home, l.Away, true
	case "away":
		return l.Away, l.Home, true
	}
	return Side{}, Side{}, false
}

// BattingNow returns the side batting and the side bowling, when the live
// data says which is which.
func (g Game) BattingNow() (bat, bowl Side, ok bool) { return g.battingSides() }

// Sides returns the side that batted first and the side that batted (or
// will bat) second.
func (g Game) Sides() (first, second Side, ok bool) {
	l := g.Live
	if l == nil {
		return Side{}, Side{}, false
	}
	key := g.sideOf(l.SBBatting1)
	if key == "" {
		switch bk := g.battingKey(); {
		case bk == "":
		case l.Innings <= 1:
			key = bk
		case bk == "home":
			key = "away"
		default:
			key = "home"
		}
	}
	switch key {
	case "home":
		return l.Home, l.Away, true
	case "away":
		return l.Away, l.Home, true
	}
	return Side{}, Side{}, false
}

func batted(s Side) bool { return s.Balls > 0 || s.Runs > 0 || s.Wkts > 0 }

func runsWkts(s Side) string {
	if s.Wkts >= 10 {
		return fmt.Sprintf("%d all out", s.Runs)
	}
	return fmt.Sprintf("%d/%d", s.Runs, s.Wkts)
}

// ScoreParts is the score line's two halves, first innings first. Empty
// unless the score may be shown.
func (g Game) ScoreParts(now time.Time) []string {
	if !g.Scored(now) {
		return nil
	}
	first, second, ok := g.Sides()
	if !ok {
		return nil
	}
	state := g.State(now)
	total, totalOK := g.InningsOvers(now)
	chasing := state == "in" && g.Live.Innings >= 2
	target := 0
	if chasing {
		if st, ok := g.ChaseState(now); ok && st.Target != nil {
			target = *st.Target
		} else if g.StatusTarget > 0 {
			target = g.StatusTarget
		} else if firstInningsComplete(first, total, totalOK) {
			target = first.Runs + 1
		}
	}
	overs := func(s Side, battingNow bool) string {
		if !s.OversOK || s.Overs == "" {
			return ""
		}
		if battingNow && totalOK {
			return fmt.Sprintf("%s/%d ov", s.Overs, total)
		}
		return s.Overs + " ov"
	}
	text := func(s Side, battingNow, isChaser bool) string {
		if !batted(s) {
			out := s.Name + " yet to bat"
			if isChaser && target > 0 {
				out += fmt.Sprintf(", target %d", target)
			}
			return out
		}
		out := s.Name + " " + runsWkts(s)
		ov := overs(s, battingNow)
		switch {
		case ov != "" && isChaser && battingNow && target > 0:
			out += fmt.Sprintf(" (%s, target %d)", ov, target)
		case ov != "":
			out += " (" + ov + ")"
		case isChaser && battingNow && target > 0:
			out += fmt.Sprintf(" (target %d)", target)
		}
		return out
	}
	firstNow := state == "in" && g.Live.Innings <= 1
	return []string{
		text(first, firstNow, false),
		text(second, chasing, chasing),
	}
}

// ScoreLine is "<first> · <second>", or "" when the score may not be shown.
func (g Game) ScoreLine(now time.Time) string {
	return strings.Join(g.ScoreParts(now), " · ")
}

// firstInningsComplete: ten wickets down, or the full allocation of a known
// length bowled. The length is the caller's, so ChaseState can pass the
// status's and ScoreParts the one the require line proves without either
// asking the other.
func firstInningsComplete(first Side, total int, totalOK bool) bool {
	if first.Wkts >= 10 {
		return true
	}
	return totalOK && total > 0 && first.OversOK && first.Balls == total*6
}

// sideName is a team named in the scorer's text, spelled as the fixture
// spells it. The scoreboard has its own names — Kings XI Dallas is "All
// Stars" there — and a line printed as sent would name a side that is not
// in the game's title. A name that maps to neither side is left as sent.
func (g Game) sideName(name string) string {
	name = strings.TrimSpace(name)
	switch g.sideOf(name) {
	case "home":
		return g.Fixture.Home
	case "away":
		return g.Fixture.Away
	}
	return name
}

// tossLine returns "X won the toss and chose to field." from the scorer's
// text, when one of its lines is the toss.
func (g Game) tossLine() string {
	l := g.Live
	if l == nil {
		return ""
	}
	for _, t := range []string{l.LastPlay, l.MatchComment} {
		if m := tossRe.FindStringSubmatch(t); m != nil {
			choice := "field"
			if strings.EqualFold(m[2], "bat") {
				choice = "bat"
			}
			return fmt.Sprintf("%s won the toss and chose to %s.", g.sideName(m[1]), choice)
		}
	}
	return ""
}

// Situation is the one line under the score: the toss before any scoring,
// what the chasing side needs during the chase, or nothing.
func (g Game) Situation(now time.Time) string {
	l := g.Live
	if l == nil || !g.Usable(now) || g.State(now) == "post" {
		return ""
	}
	if !l.HadScoring {
		return g.tossLine()
	}
	st, ok := g.ChaseState(now)
	if !ok || st.Target == nil {
		return ""
	}
	need := *st.Target - st.Runs
	if need <= 0 {
		return ""
	}
	if st.BallsBowled() == 0 {
		return fmt.Sprintf("%s need %d to win", st.BattingTeam, need)
	}
	if left := st.BallsLeft(); left > 0 {
		return fmt.Sprintf("%s need %d from %d balls", st.BattingTeam, need, left)
	}
	return ""
}

// Result is the finished game's result line. The scorer's last_play is
// never read once a game is over: it says "require -3 runs from 24 balls".
func (g Game) Result() string {
	l := g.Live
	if l == nil || !g.Finished() {
		return ""
	}
	text := cleanStatus(l.StatusText)
	if m := resultRe.FindStringSubmatch(text); m != nil {
		r := strings.TrimSpace(wonByRe.ReplaceAllString(m[1], "won by"))
		low := strings.ToLower(r)
		if strings.Contains(low, "won by") && g.MaxOv > 0 && g.MaxOv < 20 {
			r += fmt.Sprintf(" (reduced to %d overs)", g.MaxOv)
		}
		return r
	}
	if low := strings.ToLower(text); strings.Contains(low, "abandon") || strings.Contains(low, "no result") {
		return text
	}
	switch l.Winner {
	case "home":
		return g.Fixture.Home + " won"
	case "away":
		return g.Fixture.Away + " won"
	}
	if l.Winner != "" {
		return l.Winner + " won"
	}
	// Over by the score before any field says so: the chasing side won,
	// by the wickets it still has.
	if g.targetReached() {
		if bat, _, ok := g.battingSides(); ok {
			r := bat.Name + " won"
			if left := 10 - bat.Wkts; left > 0 {
				r += fmt.Sprintf(" by %d wicket", left)
				if left > 1 {
					r += "s"
				}
				if g.MaxOv > 0 && g.MaxOv < 20 {
					r += fmt.Sprintf(" (reduced to %d overs)", g.MaxOv)
				}
			}
			return r
		}
	}
	return "Finished. The result hasn't been published yet."
}

// LatestFromScorer is the scorer's most recent line, tidied: the nearest
// thing this league has to ball-by-ball.
func (g Game) LatestFromScorer(now time.Time) string {
	l := g.Live
	if l == nil || g.State(now) != "in" || !g.Usable(now) {
		return ""
	}
	lp := strings.TrimSpace(l.LastPlay)
	if m := requireRe.FindStringSubmatch(lp); m != nil {
		n, _ := strconv.Atoi(m[2])
		b, _ := strconv.Atoi(m[3])
		if n <= 0 || b <= 0 {
			return ""
		}
		return fmt.Sprintf("%s need %d from %d balls.", g.sideName(m[1]), n, b)
	}
	if m := forAfterRe.FindStringSubmatch(lp); m != nil {
		return fmt.Sprintf("%s are %s/%s after %s overs.", g.sideName(m[1]), m[2], m[3], m[4])
	}
	if t := g.tossLine(); t != "" && tossRe.MatchString(lp) {
		return t
	}
	return lp
}

// ------------------------------------------------------------------- model

// MathState is the state the run-rate arithmetic works on. It needs a
// fresh score, and in the first innings a known innings length.
func (g Game) MathState(now time.Time) (explainer.MatchState, bool) {
	if !g.Fresh(now) || !g.Scored(now) {
		return explainer.MatchState{}, false
	}
	l := g.Live
	if l.Innings >= 2 {
		return g.ChaseState(now)
	}
	bat, bowl, ok := g.battingSides()
	total, totalOK := g.TotalOvers()
	if !ok || !totalOK || !bat.OversOK || bat.Balls == 0 {
		return explainer.MatchState{}, false
	}
	overs, _ := strconv.ParseFloat(bat.Overs, 64)
	return explainer.MatchState{
		BattingTeam: bat.Name, BowlingTeam: bowl.Name,
		Runs: bat.Runs, Wickets: bat.Wkts, Overs: overs,
		TotalOvers: total, Innings: 1,
	}, true
}

// ChaseState is the second innings as the model reads it.
//
// The target and the innings length come from the scorer's own line, "X
// require N runs from B balls": target = runs + N, total balls = balls
// bowled + B. That is the only place a rain-reduced target appears while
// the game is on, and it is cross-checked against whatever MaxOv and Target
// the status text carries. A line that fails any check refuses the state
// outright rather than falling back, because a failing line is evidence the
// numbers disagree. With no such line at all, the plain T20 target applies,
// but only once the first innings is demonstrably complete.
//
// Par stays zero: a first-innings par would come from professional cricket.
func (g Game) ChaseState(now time.Time) (explainer.MatchState, bool) {
	l := g.Live
	if l == nil || l.Innings < 2 || !g.Scored(now) {
		return explainer.MatchState{}, false
	}
	bat, bowl, ok := g.battingSides()
	if !ok || !bat.OversOK {
		return explainer.MatchState{}, false
	}
	first, _, ok := g.Sides()
	if !ok {
		return explainer.MatchState{}, false
	}
	overs, _ := strconv.ParseFloat(bat.Overs, 64)
	st := explainer.MatchState{
		BattingTeam: bat.Name, BowlingTeam: bowl.Name,
		Runs: bat.Runs, Wickets: bat.Wkts, Overs: overs, Innings: 2,
	}
	for _, text := range []string{l.LastPlay, l.MatchComment} {
		m := requireRe.FindStringSubmatch(strings.TrimSpace(text))
		if m == nil {
			continue
		}
		key := g.sideOf(m[1])
		n, _ := strconv.Atoi(m[2])
		b, _ := strconv.Atoi(m[3])
		if key == "" || key != g.battingKey() || n <= 0 {
			return explainer.MatchState{}, false
		}
		target := bat.Runs + n
		totalBalls := bat.Balls + b
		if totalBalls%6 != 0 {
			return explainer.MatchState{}, false
		}
		totalOvers := totalBalls / 6
		if (g.MaxOv > 0 && totalOvers != g.MaxOv) || totalOvers <= 0 || totalOvers > 20 {
			return explainer.MatchState{}, false
		}
		if g.StatusTarget > 0 && target != g.StatusTarget {
			return explainer.MatchState{}, false
		}
		st.TotalOvers, st.Target = totalOvers, &target
		return st, true
	}
	total, totalOK := g.TotalOvers()
	if !totalOK || !firstInningsComplete(first, total, totalOK) {
		return explainer.MatchState{}, false
	}
	target := first.Runs + 1
	if g.StatusTarget > 0 {
		target = g.StatusTarget
	}
	st.TotalOvers, st.Target = total, &target
	return st, true
}

// ModelAllowed returns the chase state our model may price, and whether it
// may price at all. Only in the chase — Minor League teams have no ratings,
// so a first-innings number would be the league average dressed up — and
// only on fresh data whose batting side three fields agree on, in a game of
// at least 15 overs, since nothing shorter is in the fitted sample.
//
// Never during a delay either. The require line was written before the
// rain, at the old length, and TotalOvers already treats the length as
// unknown while a delay is on; pricing the old line as "right now" would be
// the one place that forgot.
func (g Game) ModelAllowed(now time.Time) (explainer.MatchState, bool) {
	if g.State(now) != "in" || !g.Fresh(now) || !g.Scored(now) || g.hasDelay() {
		return explainer.MatchState{}, false
	}
	l := g.Live
	if l.SBBatting == "" || l.SBBatting == "0" {
		return explainer.MatchState{}, false
	}
	var sb string
	switch Canon(l.SBBatting) {
	case Canon(l.SBHome):
		sb = "home"
	case Canon(l.SBAway):
		sb = "away"
	}
	if sb == "" || sb != l.Batting {
		return explainer.MatchState{}, false
	}
	st, ok := g.ChaseState(now)
	if !ok || st.TotalOvers < 15 {
		return explainer.MatchState{}, false
	}
	return st, true
}
