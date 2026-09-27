package milclive

import (
	"strings"
	"testing"
	"time"

	"github.com/asaraog/mcp-cricket/internal/explainer"
)

// Kalshi's two start times for one game disagreed by four hours on Sep 27.
// The earlier is shown, flagged, and opens the listing window.
func TestStartDisagreement(t *testing.T) {
	gs := captureGames(t)

	chi := gs["bfd5348c"] // Chicago Kingsmen v Chicago Tigers: 19:30Z vs ticker 19:00Z
	if want := time.Date(2026, 9, 26, 19, 30, 0, 0, time.UTC); !chi.DisplayStart().Equal(want) {
		t.Errorf("display start = %v, want the milestone's %v (30 minutes apart is agreement)", chi.DisplayStart(), want)
	}
	if chi.StartUnconfirmed() {
		t.Error("a 30-minute difference was flagged as unconfirmed")
	}
	chi.Live = nil // the window by the clock alone
	if !chi.InWindow(time.Date(2026, 9, 26, 16, 0, 0, 0, time.UTC)) {
		t.Error("not listed three hours before the earlier (ticker) start")
	}
	if chi.InWindow(time.Date(2026, 9, 26, 15, 59, 0, 0, time.UTC)) {
		t.Error("listed before the window opens")
	}

	stl := gs["8f1efc49"] // Chicago Kingsmen v St Louis Americans, Sep 27: 15:00Z vs ticker 19:00Z
	if want := time.Date(2026, 9, 27, 15, 0, 0, 0, time.UTC); !stl.DisplayStart().Equal(want) {
		t.Errorf("display start = %v, want the earlier %v", stl.DisplayStart(), want)
	}
	if !stl.StartUnconfirmed() {
		t.Error("a four-hour disagreement was not flagged")
	}
	if other, fromTicker := stl.OtherStart(); !fromTicker || !other.Equal(time.Date(2026, 9, 27, 19, 0, 0, 0, time.UTC)) {
		t.Errorf("other start = %v (ticker=%v)", other, fromTicker)
	}
	stl.Live = nil
	if !stl.InWindow(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)) {
		t.Error("not listed from three hours before the earlier start")
	}
}

// Every shape the capture holds, read at the moment it was captured.
func TestStateFromCapture(t *testing.T) {
	gs := captureGames(t)
	at := capAt
	cases := []struct {
		id, name, state, phase, line, situation, result string
		scored                                          bool
	}{
		{id: "d92b25ba", name: "NYC Titans v Manhattan Yorkers (tomorrow)", state: "pre"},
		{id: "bfd5348c", name: "Chicago Kingsmen v Chicago Tigers (after the toss)", state: "in",
			phase: "Starting soon", situation: "Chicago Kingsmen won the toss and chose to field."},
		{id: "2263013a", name: "New England Eagles v NJ Somerset Cavaliers (rain)", state: "in", phase: "Rain delay"},
		{id: "b61978de", name: "Lone Star Athletics v MetroPlex Tracers (before its start)", state: "pre"},
		{id: "e720b1fd", name: "Atlanta Fire v Atlanta Lightning (innings break)", state: "in", phase: "Innings break",
			line:      "Atlanta Fire 205/5 (20 ov) · Atlanta Lightning yet to bat, target 206",
			situation: "Atlanta Lightning need 206 to win", scored: true},
		{id: "f1b70771", name: "East Bay Blazers v San Ramon Grizzlies (first innings)", state: "in",
			line: "East Bay Blazers 88/5 (13.1/20 ov) · San Ramon Grizzlies yet to bat", scored: true},
		{id: "041168aa", name: "The Philadelphians v NYC Titans (reduced to 10)", state: "in",
			line: "NYC Titans 8/0 (1.4/10 ov) · The Philadelphians yet to bat", scored: true},
		{id: "99cf27e2", name: "Manhattan Yorkers v New Jersey Stallions (finished, reduced)", state: "post",
			line:   "New Jersey Stallions 98 all out (15.5 ov) · Manhattan Yorkers 102/4 (12 ov)",
			result: "Manhattan Yorkers won by 6 wickets (reduced to 16 overs)", scored: true},
		{id: "6ab76371", name: "Michigan Cricket Stars v St Louis Americans (finished)", state: "post",
			result: "Michigan Cricket Stars won by 2 wickets", scored: true},
	}
	for _, c := range cases {
		g, ok := gs[c.id]
		if !ok {
			t.Fatalf("%s: not in the capture", c.name)
		}
		if got := g.State(at); got != c.state {
			t.Errorf("%s: state %q, want %q", c.name, got, c.state)
		}
		if got := g.Phase(at); got != c.phase {
			t.Errorf("%s: phase %q, want %q", c.name, got, c.phase)
		}
		if got := g.Scored(at); got != c.scored {
			t.Errorf("%s: scored %v, want %v", c.name, got, c.scored)
		}
		if c.line != "" || !c.scored {
			if got := g.ScoreLine(at); got != c.line {
				t.Errorf("%s: score line\n  got  %q\n  want %q", c.name, got, c.line)
			}
		}
		if got := g.Situation(at); got != c.situation {
			t.Errorf("%s: situation %q, want %q", c.name, got, c.situation)
		}
		if got := g.Result(); got != c.result {
			t.Errorf("%s: result %q, want %q", c.name, got, c.result)
		}
		// The scorer's nonsense after a finish ("require -3 runs from 24
		// balls") never reaches a reader.
		for _, s := range []string{g.ScoreLine(at), g.Result(), g.Situation(at), g.LatestFromScorer(at)} {
			if strings.Contains(s, "-3") || strings.Contains(s, "require") {
				t.Errorf("%s: scorer text leaked: %q", c.name, s)
			}
		}
	}

	ne := gs["2263013a"]
	if got := ne.Timing(at); got != "due" {
		t.Errorf("rain-delayed game timing %q, want due", got)
	}
	if !ne.InWindow(at) {
		t.Error("a rain-delayed game dropped off the list")
	}
	lone := gs["b61978de"]
	if got := lone.Timing(at); got != "future" {
		t.Errorf("a game before its start has timing %q", got)
	}

	// Kings XI Dallas is "All Stars" on the scoreboard. That names neither
	// side, so it is not evidence of a swap.
	if !gs["e6353445"].OrientationOK() {
		t.Error("Dallas Xforia Giants v Kings XI Dallas failed orientation over the scoreboard's 'All Stars'")
	}
	swapped := gs["f1b70771"]
	l := *swapped.Live
	l.SBHome, l.SBAway = l.SBAway, l.SBHome
	swapped.Live = &l
	if swapped.OrientationOK() || swapped.ScoreLine(at) != "" {
		t.Error("a scoreboard with home and away swapped still showed a score")
	}

	// A closed game with no winner and no text. The score settles it when
	// the chase reached its target: New Jersey Stallions were all out for
	// 98 and Manhattan Yorkers made 102/4, which is the result Kalshi
	// later published.
	bare := gs["99cf27e2"]
	bl := *bare.Live
	bl.StatusText, bl.Winner = "", ""
	bare.Live = &bl
	if got := bare.Result(); got != "Manhattan Yorkers won by 6 wickets (reduced to 16 overs)" {
		t.Errorf("bare finished result, target reached = %q", got)
	}
	// Short of the target, nothing is claimed.
	bl.Home.Runs = 50
	if got := bare.Result(); got != "Finished. The result hasn't been published yet." {
		t.Errorf("bare finished result = %q", got)
	}
	bl.StatusText = "Match Abandoned"
	if got := bare.Result(); got != "Match Abandoned" {
		t.Errorf("abandoned result = %q", got)
	}

	if got := gs["f1b70771"].LatestFromScorer(at); got != "East Bay Blazers are 88/5 after 13.1 overs." {
		t.Errorf("latest from the scorer = %q", got)
	}
}

// liveChase is a synthetic second innings built on the Michigan Cricket
// Stars v St Louis Americans fixture: Michigan 141/8 after 19 overs chasing
// St Louis's 152/9.
func liveChase(t *testing.T, at time.Time) Game {
	t.Helper()
	g := captureGames(t)["6ab76371"]
	g.Live = &Live{
		Widget: "live", Status: "live", StatusText: "Match in Progress - Match Stable (Bet Delay:4 seconds)",
		Batting: "home", LastPlay: "Michigan Cricket Stars require 12 runs from 6 balls.",
		MatchComment: "Michigan Cricket Stars require 12 runs from 6 balls.", Orientation: "milestone",
		SBHome: "Michigan Cricket Stars", SBAway: "St Louis Americans",
		SBBatting: "Michigan Cricket Stars", SBBatting1: "St Louis Americans",
		HadScoring: true, Innings: 2, FetchedAt: at,
		Home: Side{Name: "Michigan Cricket Stars", Runs: 141, Wkts: 8, Overs: "19", Balls: 114, OversOK: true},
		Away: Side{Name: "St Louis Americans", Runs: 152, Wkts: 9, Overs: "20", Balls: 120, OversOK: true},
	}
	g.MaxOv, g.StatusTarget = 0, 0
	return g
}

func TestChaseState(t *testing.T) {
	at := time.Date(2026, 9, 26, 18, 30, 0, 0, time.UTC)
	g := liveChase(t, at)
	st, ok := g.ChaseState(at)
	if !ok || st.Target == nil || *st.Target != 153 || st.TotalOvers != 20 || st.BallsLeft() != 6 {
		t.Fatalf("chase = %+v ok=%v, want target 153, 20 overs, 6 balls left", st, ok)
	}
	if st.BattingTeam != "Michigan Cricket Stars" || st.BowlingTeam != "St Louis Americans" || st.Par != 0 {
		t.Errorf("sides/par wrong: %+v", st)
	}
	if _, ok := g.ModelAllowed(at); !ok {
		t.Error("a fresh, consistent 20-over chase must be priceable")
	}
	if got := g.ScoreLine(at); got != "St Louis Americans 152/9 (20 ov) · Michigan Cricket Stars 141/8 (19/20 ov, target 153)" {
		t.Errorf("chase line = %q", got)
	}
	if got := g.Situation(at); got != "Michigan Cricket Stars need 12 from 6 balls" {
		t.Errorf("situation = %q", got)
	}

	reduced := liveChase(t, at)
	reduced.MaxOv = 16
	if _, ok := reduced.ChaseState(at); ok {
		t.Error("a require line implying 20 overs was accepted against MaxOv:16")
	}

	wrongSide := liveChase(t, at)
	wrongSide.Live.LastPlay = "St Louis Americans require 12 runs from 6 balls."
	wrongSide.Live.MatchComment = wrongSide.Live.LastPlay
	if _, ok := wrongSide.ChaseState(at); ok {
		t.Error("a require line naming the bowling side was accepted")
	}

	zero := liveChase(t, at)
	zero.Live.LastPlay = "Michigan Cricket Stars require 0 runs from 6 balls."
	zero.Live.MatchComment = zero.Live.LastPlay
	if _, ok := zero.ChaseState(at); ok {
		t.Error("'require 0 runs' was accepted as a chase")
	}

	ragged := liveChase(t, at)
	ragged.Live.Home.Overs, ragged.Live.Home.Balls = "18.1", 109 // 109 + 6 = 115
	if _, ok := ragged.ChaseState(at); ok {
		t.Error("balls bowled + balls left = 115, not whole overs, was accepted")
	}

	// No require line: the plain target, once the first innings is done.
	brk := captureGames(t)["e720b1fd"]
	if st, ok := brk.ChaseState(capAt); !ok || *st.Target != 206 || st.TotalOvers != 20 {
		t.Errorf("innings break: %+v ok=%v, want target 206 over 20", st, ok)
	}
	rain := captureGames(t)["e720b1fd"]
	rl := *rain.Live
	rl.Home.Overs, rl.Home.Balls = "15", 90
	rl.Status = "Rain Delay"
	rain.Live = &rl
	if _, ok := rain.ChaseState(capAt); ok {
		t.Error("a first innings cut at 15 overs in the rain gave a target with no require line")
	}

	// A consistent 10-over chase: real, but outside what the model knows.
	short := liveChase(t, at)
	short.MaxOv = 10
	short.Live.Home = Side{Name: "Michigan Cricket Stars", Runs: 50, Wkts: 2, Overs: "8", Balls: 48, OversOK: true}
	short.Live.Away = Side{Name: "St Louis Americans", Runs: 69, Wkts: 5, Overs: "10", Balls: 60, OversOK: true}
	short.Live.LastPlay = "Michigan Cricket Stars require 20 runs from 12 balls."
	short.Live.MatchComment = short.Live.LastPlay
	if st, ok := short.ChaseState(at); !ok || st.TotalOvers != 10 || *st.Target != 70 {
		t.Errorf("10-over chase: %+v ok=%v", st, ok)
	}
	if _, ok := short.ModelAllowed(at); ok {
		t.Error("the model priced a 10-over chase")
	}

	first := captureGames(t)["f1b70771"]
	if _, ok := first.ChaseState(capAt); ok {
		t.Error("the first innings produced a chase state")
	}
	if _, ok := first.ModelAllowed(capAt); ok {
		t.Error("the model priced a first innings")
	}
}

// The real chase in the 20:04 capture: "Atlanta Lightning require 178 runs
// from 91 balls" at 28/4 after 4.5 overs.
func TestChaseFromCapture(t *testing.T) {
	g := games(t, "milestones_2004z.json", "live_batch_2004z.json", chaseAt)["e720b1fd"]
	st, ok := g.ModelAllowed(chaseAt)
	if !ok || *st.Target != 206 || st.TotalOvers != 20 || st.BallsLeft() != 91 || st.BattingTeam != "Atlanta Lightning" {
		t.Fatalf("Atlanta chase = %+v ok=%v", st, ok)
	}
	if got := g.ScoreLine(chaseAt); got != "Atlanta Fire 205/5 (20 ov) · Atlanta Lightning 28/4 (4.5/20 ov, target 206)" {
		t.Errorf("line = %q", got)
	}
	if got := g.LatestFromScorer(chaseAt); got != "Atlanta Lightning need 178 from 91 balls." {
		t.Errorf("latest = %q", got)
	}
	// A rain delay mid-innings keeps the game in play, paused.
	ne := games(t, "milestones_2004z.json", "live_batch_2004z.json", chaseAt)["2263013a"]
	if ne.State(chaseAt) != "in" || ne.Phase(chaseAt) != "Rain delay" {
		t.Errorf("mid-innings rain: state %q phase %q", ne.State(chaseAt), ne.Phase(chaseAt))
	}
	if _, ok := ne.MathState(chaseAt); ok {
		t.Error("the run-rate arithmetic ran with the innings length unknown in the rain")
	}
}

// Minor League teams have no ratings, and none may creep in: the model's
// only team-strength term must be zero for every name the league uses.
func TestModelHasNoRatingsForMiLC(t *testing.T) {
	target := 153
	base := explainer.MatchState{Runs: 120, Wickets: 5, Overs: 16, TotalOvers: 20, Innings: 2, Target: &target}
	unrated := base
	unrated.BattingTeam, unrated.BowlingTeam = "Unrated Side A", "Unrated Side B"
	want := explainer.WinProbability(unrated).BattingTeamWinProb
	names := map[string]bool{"All Stars": true}
	for _, g := range captureGames(t) {
		names[g.Home], names[g.Away] = true, true
	}
	for a := range names {
		for b := range names {
			if a == b {
				continue
			}
			st := base
			st.BattingTeam, st.BowlingTeam = a, b
			if got := explainer.WinProbability(st).BattingTeamWinProb; got != want {
				t.Errorf("%s v %s priced %.3f, unrated %.3f: a rating crept in", a, b, got, want)
			}
		}
	}
}

func TestFreshnessAndWindow(t *testing.T) {
	gs := captureGames(t)
	atlanta := gs["e720b1fd"]
	start := atlanta.DisplayStart()
	at := start.Add(2 * time.Hour)
	aged := func(age time.Duration) Game {
		g := atlanta
		l := *g.Live
		l.FetchedAt = at.Add(-age)
		g.Live = &l
		return g
	}
	if g := aged(30 * time.Second); !g.Fresh(at) || !g.Scored(at) {
		t.Error("30 seconds old must be fresh")
	}
	if g := aged(90 * time.Second); g.Fresh(at) || !g.Scored(at) {
		t.Error("90 seconds old: still stated, no longer fresh")
	} else if _, ok := g.ModelAllowed(at); ok {
		t.Error("the model priced a 90-second-old score")
	}
	if g := aged(11 * time.Minute); g.Scored(at) || g.ScoreLine(at) != "" {
		t.Error("an 11-minute-old score was still shown")
	} else if g.State(at) != "in" {
		t.Errorf("stale data: state %q, want in by the clock", g.State(at))
	}
	if g := aged(11 * time.Minute); g.State(start.Add(4*time.Hour)) != "post" {
		t.Error("with no usable data the clock must end the game after 3h30m")
	}

	// A game not yet begun: listed 2h before, not 4h before.
	pre := gs["b564fe71"] // Los Angeles Lashings v Seattle Thunderbolts, Sep 26
	pre.Live = nil
	if !pre.InWindow(pre.EarliestStart().Add(-2 * time.Hour)) {
		t.Error("not listed two hours before")
	}
	if pre.InWindow(pre.EarliestStart().Add(-4 * time.Hour)) {
		t.Error("listed four hours before")
	}

	// Live: listed as long as it is live.
	late := start.Add(7 * time.Hour)
	live := atlanta
	ll := *live.Live
	ll.FetchedAt = late
	live.Live = &ll
	if !live.InWindow(late) {
		t.Error("a live game fell off the list at start+7h")
	}

	// Finished, seen at start+3h: until 2h after that.
	fin := gs["99cf27e2"]
	ds := fin.DisplayStart()
	fin.FinishedAt, fin.FinishedOnFirstSight = ds.Add(3*time.Hour), false
	if !fin.InWindow(ds.Add(5*time.Hour - time.Minute)) {
		t.Error("finished game gone before 2h after its result")
	}
	if fin.InWindow(ds.Add(5*time.Hour + time.Minute)) {
		t.Error("finished game still listed 2h after its result")
	}
	// Finished before this process saw it: the result time is unknown, so
	// the clock assumes start+4h.
	fin.FinishedOnFirstSight = true
	if !fin.InWindow(ds.Add(6*time.Hour-time.Minute)) || fin.InWindow(ds.Add(6*time.Hour+time.Minute)) {
		t.Error("a game finished on first sight must be listed until start+6h")
	}

	// Past its start, not begun: start+4h, or start+8h while delayed.
	waiting := gs["b564fe71"]
	wl := *waiting.Live
	waiting.Live = &wl
	wds := waiting.DisplayStart()
	probe := func(t2 time.Time) Game {
		g := waiting
		l := *g.Live
		l.FetchedAt = t2
		g.Live = &l
		return g
	}
	if g := probe(wds.Add(4*time.Hour - time.Minute)); !g.InWindow(wds.Add(4*time.Hour - time.Minute)) {
		t.Error("a late-starting game gone before start+4h")
	}
	if g := probe(wds.Add(4*time.Hour + time.Minute)); g.InWindow(wds.Add(4*time.Hour + time.Minute)) {
		t.Error("a game with no delay text still listed after start+4h")
	}
	wl.Status = "Rain Delay"
	if g := probe(wds.Add(8*time.Hour - time.Minute)); !g.InWindow(wds.Add(8*time.Hour - time.Minute)) {
		t.Error("a rain-delayed game gone before start+8h")
	}
	if g := probe(wds.Add(8*time.Hour + time.Minute)); g.InWindow(wds.Add(8*time.Hour + time.Minute)) {
		t.Error("a rain-delayed game still listed after start+8h")
	}
}

// 20:56:08Z on 2026-09-26: New England Eagles 26/3 after 2.3 overs, chasing
// 63 in a game cut to 7 overs — "require 37 runs from 27 balls" — and
// Kalshi never published MaxOv for it. Its status flipped between "Rain
// Delay" and "Ball in Progress" every 15 to 30 seconds.
var reducedAt = time.Date(2026, 9, 26, 20, 56, 8, 0, time.UTC)

// The innings length the score line prints is the one the require line
// proves, not a T20's 20. The line read "(2.3/20 ov, target 63)" beside
// "need 37 from 27 balls", and flipped to "(2.3 ov, ...)" whenever the
// status said rain.
func TestReducedChaseLengthComesFromTheRequireLine(t *testing.T) {
	g := games(t, "milestones.json", "live_batch_2056z.json", reducedAt)["2263013a"]
	if g.MaxOv != 0 {
		t.Fatalf("precondition: the capture carries no MaxOv, got %d", g.MaxOv)
	}
	want := "New Jersey Somerset Cavaliers 62/3 (7 ov) · New England Eagles 26/3 (2.3/7 ov, target 63)"
	if got := g.ScoreLine(reducedAt); got != want {
		t.Errorf("reduced chase line\n  got  %q\n  want %q", got, want)
	}
	if got := g.Situation(reducedAt); got != "New England Eagles need 37 from 27 balls" {
		t.Errorf("situation = %q", got)
	}
	if n, ok := g.InningsOvers(reducedAt); !ok || n != 7 {
		t.Errorf("innings overs = %d, %v; want 7", n, ok)
	}
	// The same poll a few seconds later, status "Rain Delay": same line.
	rain := g
	rl := *rain.Live
	rl.Status, rl.StatusText = "Rain Delay", "Rain Delay"
	rain.Live = &rl
	if got := rain.ScoreLine(reducedAt); got != want {
		t.Errorf("the line changed when the status said rain:\n  got  %q\n  want %q", got, want)
	}
	if rain.Phase(reducedAt) != "Rain delay" {
		t.Errorf("phase = %q", rain.Phase(reducedAt))
	}
	for _, gg := range []Game{g, rain} {
		if _, ok := gg.ModelAllowed(reducedAt); ok {
			t.Error("the model priced a 7-over chase")
		}
	}
}

// A game this process has seen interrupted stops assuming 20 overs in its
// first innings, even while the delay word is missing from the status. The
// runs-per-over arithmetic refuses rather than count from 120 balls.
func TestInterruptedFirstInningsHasNoAssumedLength(t *testing.T) {
	g := games(t, "milestones_2004z.json", "live_batch_2004z.json", chaseAt)["2263013a"]
	l := *g.Live
	l.Status, l.StatusText = "live", "Match in Progress - Ball in Progress"
	g.Live = &l
	if got := g.ScoreLine(chaseAt); got != "New Jersey Somerset Cavaliers 9/0 (0.5/20 ov) · New England Eagles yet to bat" {
		t.Fatalf("precondition, never interrupted: %q", got)
	}
	g.Interrupted = true
	if got := g.ScoreLine(chaseAt); got != "New Jersey Somerset Cavaliers 9/0 (0.5 ov) · New England Eagles yet to bat" {
		t.Errorf("interrupted first innings line = %q", got)
	}
	if _, ok := g.TotalOvers(); ok {
		t.Error("an interrupted game still assumed a length")
	}
	if _, ok := g.MathState(chaseAt); ok {
		t.Error("run-rate arithmetic ran on an assumed 20 overs in an interrupted game")
	}
	// Seen at or after the start, rain marks the game; "Toss Pending"
	// alone never does.
	raw := games(t, "milestones_2004z.json", "live_batch_2004z.json", chaseAt)["2263013a"]
	if !raw.interruptionSeen(chaseAt) {
		t.Error("rain after the start was not seen as an interruption")
	}
	if raw.interruptionSeen(raw.DisplayStart().Add(-time.Minute)) {
		t.Error("rain before the start counted")
	}
	toss := raw
	tl := *toss.Live
	tl.Status, tl.StatusText, tl.LastPlay, tl.MatchComment = "Toss Pending", "Toss Pending", "Toss Pending", ""
	toss.Live = &tl
	if toss.interruptionSeen(chaseAt) {
		t.Error("\"Toss Pending\" alone counted as an interruption")
	}
}

// The Philadelphians hit the winning runs at 21:02Z: 68/0 chasing 68 in a
// 10-over game. The widget still said "live" and the winner was empty for
// another 75 seconds; the score alone says the game is over.
func TestAChaseThatReachedItsTargetIsFinished(t *testing.T) {
	at := time.Date(2026, 9, 26, 21, 2, 1, 0, time.UTC)
	g := games(t, "milestones.json", "live_batch_2102z.json", at)["041168aa"]
	if g.Live.Widget != "live" || g.Live.Winner != "" {
		t.Fatalf("precondition: widget %q winner %q", g.Live.Widget, g.Live.Winner)
	}
	if g.State(at) != "post" || !g.Finished() {
		t.Errorf("state %q, want post", g.State(at))
	}
	if got := g.Result(); got != "The Philadelphians won by 10 wickets (reduced to 10 overs)" {
		t.Errorf("result = %q", got)
	}
	if got := g.ScoreLine(at); got != "NYC Titans 67/6 (10 ov) · The Philadelphians 68/0 (6 ov)" {
		t.Errorf("final line = %q", got)
	}
	if s := g.Situation(at) + g.LatestFromScorer(at); s != "" {
		t.Errorf("a finished chase still has a situation: %q", s)
	}

	// Fifteen seconds earlier, two short: still in play.
	before := g
	bl := *before.Live
	bl.Home.Runs, bl.Home.Overs, bl.Home.Balls = 66, "5.5", 35
	bl.LastPlay = "The Philadelphians require 2 runs from 25 balls."
	bl.MatchComment = bl.LastPlay
	before.Live = &bl
	if before.State(at) != "in" || before.Finished() {
		t.Errorf("two runs short: state %q", before.State(at))
	}
	if got := before.ScoreLine(at); got != "NYC Titans 67/6 (10 ov) · The Philadelphians 66/0 (5.5/10 ov, target 68)" {
		t.Errorf("two short line = %q", got)
	}
}

// Between two disagreeing starts, "Toss Pending" is Kalshi's routine
// pre-toss status, not a delay: Sunday's Chicago Kingsmen v St Louis
// Americans is listed for 15:00Z and its ticker for 19:00Z.
func TestTossPendingBetweenTwoStartsIsNotADelay(t *testing.T) {
	base := captureGames(t)["8f1efc49"]
	at := func(h, m int, status string) (Game, time.Time) {
		now := time.Date(2026, 9, 27, h, m, 0, 0, time.UTC)
		g := base
		l := *g.Live
		l.FetchedAt = now
		l.Status, l.StatusText, l.LastPlay, l.MatchComment = status, status, "", ""
		if status == "Toss Pending" {
			l.LastPlay, l.MatchComment = "Toss Pending", "Toss Pending"
		}
		g.Live = &l
		return g, now
	}
	if g, now := at(16, 0, "Match Scheduled"); g.State(now) != "pre" || g.Phase(now) != "" || g.Timing(now) != "due" {
		t.Errorf("16:00Z: state %q phase %q timing %q", g.State(now), g.Phase(now), g.Timing(now))
	}
	if g, now := at(18, 40, "Toss Pending"); g.State(now) != "pre" || g.Phase(now) != "" {
		t.Errorf("18:40Z, toss pending before the later start: state %q phase %q, want pre and no phase", g.State(now), g.Phase(now))
	}
	if g, now := at(19, 5, "Toss Pending"); g.State(now) != "in" || g.Phase(now) != "Toss delayed" {
		t.Errorf("19:05Z, toss pending after both starts: state %q phase %q", g.State(now), g.Phase(now))
	}
	if g, now := at(18, 40, "Rain Delay"); g.State(now) != "in" || g.Phase(now) != "Rain delay" {
		t.Errorf("18:40Z, rain: state %q phase %q", g.State(now), g.Phase(now))
	}
}

// The scorer writes the scoreboard's names; the reader is shown the
// fixture's. Kings XI Dallas is "All Stars" on the scoreboard.
func TestScorerLinesUseTheFixtureSpelling(t *testing.T) {
	g := captureGames(t)["e6353445"]
	now := g.DisplayStart().Add(time.Hour)
	l := *g.Live
	l.Widget, l.Status, l.StatusText, l.Winner = "live", "live", "Match in Progress", ""
	l.Innings, l.Batting, l.SBBatting, l.FetchedAt = 1, "away", "All Stars", now
	l.Home = Side{Name: g.Home}
	l.Away = Side{Name: g.Away, Runs: 70, Wkts: 3, Overs: "11.3", Balls: 69, OversOK: true}
	l.LastPlay = "All Stars are 70 for 3 after 11.3 overs."
	g.Live = &l
	if got := g.LatestFromScorer(now); got != "Kings XI Dallas are 70/3 after 11.3 overs." {
		t.Errorf("latest = %q", got)
	}
	l.HadScoring = false
	l.LastPlay = "All Stars won the toss and have elected to bat"
	if got := g.Situation(now); got != "Kings XI Dallas won the toss and chose to bat." {
		t.Errorf("toss = %q", got)
	}
	l.HadScoring = true
	l.LastPlay = "Somebody Else are 5 for 0 after 1 overs."
	if got := g.LatestFromScorer(now); got != "Somebody Else are 5/0 after 1 overs." {
		t.Errorf("an unmapped name was not left as sent: %q", got)
	}
}

// Delay text is read only from data recent enough to state: an eleven-
// minute-old "Rain Delay" is no more current than the score it came with.
func TestPhaseIgnoresDelayTextInStaleData(t *testing.T) {
	g := games(t, "milestones_2004z.json", "live_batch_2004z.json", chaseAt)["e720b1fd"]
	l := *g.Live
	l.Status, l.FetchedAt = "Rain Delay", chaseAt.Add(-11*time.Minute)
	g.Live = &l
	if g.State(chaseAt) != "in" {
		t.Fatalf("precondition: state %q", g.State(chaseAt))
	}
	if ph := g.Phase(chaseAt); ph != "" {
		t.Errorf("stale live data still named a pause: %q", ph)
	}
}

// A delay stops the model: the require line was written before the rain,
// at a length the rain may already have cut.
func TestModelStopsDuringADelay(t *testing.T) {
	at := time.Date(2026, 9, 26, 18, 30, 0, 0, time.UTC)
	g := liveChase(t, at)
	g.Live.Status = "Rain Delay"
	if _, ok := g.ChaseState(at); !ok {
		t.Fatal("precondition: the chase state itself is fine")
	}
	if _, ok := g.ModelAllowed(at); ok {
		t.Error("the model priced a chase stopped by rain")
	}
}
