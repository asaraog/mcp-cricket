package mcp

// Minor League Cricket over MCP.
//
// The league went live on cricketfornoobs.com from Kalshi's public live data
// (internal/milclive), and for the first hours of it an assistant connected
// to an MCP server was told nothing: cricket_live_matches read ESPN alone,
// ESPN does not carry the league, and "how are the Atlanta Fire doing?" got
// "No matches listed right now" while the site showed the game at an innings
// break. So the list now ends with the Minor League games in the site's own
// window, and cricket_minor_league answers for one game the way the site's
// card does.
//
// What it will not say is as deliberate as what it will. The numbers here
// follow the site's rules exactly, from the same code:
//
//   - A market percentage only when the book is a price under
//     milclive.PriceView. Most Minor League books are 23/73 placeholders
//     whose midpoint reads as a real 48%, and an assistant quoting that
//     would be quoting nobody.
//   - Midpoints only, never last_price: in a book this thin the last trade
//     can be hours old and on the other side of the spread.
//   - Our model only when milclive.ModelAllowed says so — in play, in the
//     chase, on a fresh score, in a game long enough for the fitted sample.
//     These teams have no ratings, so a first-innings number would be the
//     league average dressed up.
//   - Never the provider's bookmaker probability. milclive never decodes
//     it, so there is nothing here that could print it.
//
// The same rules hold on the two general tools an assistant reaches for
// next. cricket_market_odds answers a Minor League pairing from
// milclive.Markets, bound by event ticker, instead of the team-name scan
// that printed a 24/72 placeholder as "48% implied" for both sides, twice
// each. cricket_win_probability refuses a Minor League state the site would
// not price, so the Score line copied out of cricket_minor_league cannot
// buy the first-innings number this league is never shown.
//
// Every clock read goes through milcNow, never time.Now, so the tests can
// replay a Saturday from the capture it was recorded on.

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/asaraog/mcp-cricket/internal/cricinfo"
	"github.com/asaraog/mcp-cricket/internal/explainer"
	"github.com/asaraog/mcp-cricket/internal/kalshi"
	"github.com/asaraog/mcp-cricket/internal/milclive"
)

var (
	// milcNow is the clock every Minor League answer reads.
	milcNow = time.Now
	// liveESPN is the ESPN list, a var so a test can list matches without
	// the network.
	liveESPN = cricinfo.LiveMatches
)

// milcSourceNote closes every cricket_minor_league answer that shows a
// game. It never prints "(market)" or "(model)" itself: those labels mean a
// number is on that line, and nothing else may carry them.
const milcSourceNote = "Scores and prices come from Kalshi's live data, which Kalshi licenses from a scoring provider. " +
	"A market figure is the midpoint of Kalshi's order book, given only when the book is tight or well traded on both sides; " +
	"a model figure is this server's win model, which prices Minor League Cricket only during the chase, because these teams have no ratings. " +
	"Informational only, not betting advice.\n" +
	"Kalshi's feed has no ball-by-ball commentary and names no batters or bowlers, so there is nothing here on who is batting or bowling, dismissals or deliveries.\n" +
	"Watch: " + milclive.WatchURL + "\n" +
	"Scorecards: " + milclive.ScorecardsURL

// milcListNote follows the Minor League lines in cricket_live_matches, so an
// assistant that found a game there knows where its detail is.
const milcListNote = "Minor League Cricket scores come from Kalshi's live data; cricket_minor_league gives each game's situation and win chances."

// milcWindowText is the window, in words, that "nothing is on" refers to.
const milcWindowText = "nothing live, starting within 3 hours, or finished in the last 2 hours"

// ------------------------------------------------------------ live list

// milcListLines is one line per Minor League game in the site's window:
// live, starting within three hours, or finished within two.
func milcListLines(now time.Time) []string {
	var out []string
	for _, g := range milclive.Games(now) {
		var b strings.Builder
		fmt.Fprintf(&b, "%s v %s — %s — %s", g.Home, g.Away, milclive.League, milcListState(g, now))
		score := milcScore(g, now)
		if score != "" {
			b.WriteString(": " + score)
		}
		switch g.State(now) {
		case "pre":
			if now.Before(g.DisplayStart()) {
				b.WriteString(" starts " + milcStart(g))
			} else {
				b.WriteString(", was due " + milcStart(g))
			}
		case "post":
			if g.Finished() {
				b.WriteString(". " + endSentence(g.Result()))
			}
		}
		out = append(out, b.String())
	}
	return out
}

// milcListState is the list's state word, in the ESPN lines' vocabulary.
func milcListState(g milclive.Game, now time.Time) string {
	switch g.State(now) {
	case "in":
		if ph := g.Phase(now); ph != "" {
			return "LIVE, " + strings.ToLower(ph)
		}
		return "LIVE"
	case "post":
		if !g.Finished() {
			// Over by the clock alone: nothing says it finished.
			return "no live data"
		}
		return "finished"
	}
	if !now.Before(g.DisplayStart()) {
		if g.StartUnconfirmed() {
			return "upcoming, start time unconfirmed"
		}
		return "upcoming, start delayed"
	}
	return "upcoming"
}

// ------------------------------------------------------ one-league tool

// minorLeagueTool answers for every Minor League game in the window, or
// for one team's.
func minorLeagueTool(args map[string]any) (string, error) {
	if !milclive.Enabled() {
		return "", fmt.Errorf("Minor League Cricket live data is switched off on this server")
	}
	now := milcNow()
	team := argStr(args, "team")
	if everyTeam(team) {
		team = ""
	}
	window := milclive.Games(now)
	all := milclive.AllGames(now)
	picked := window
	if team != "" {
		picked = milcTeamGames(window, all, team)
	}
	if len(picked) == 0 {
		return milcNothingOn(team, all, now), nil
	}
	views := milcViews(picked, now)
	var b strings.Builder
	for i, g := range picked {
		b.WriteString(milcGameText(g, views[i], now))
		b.WriteString("\n")
	}
	b.WriteString(milcSourceNote)
	return b.String(), nil
}

// everyTeam reads a team argument that asks for the whole league.
func everyTeam(team string) bool {
	switch milclive.Canon(team) {
	case "", "all", "any", "every", "milc", "minor league", milclive.Canon(milclive.League):
		return true
	}
	return false
}

// milcViews reads each game's market, in parallel: a game in play reads
// its event through the network on a cold cache, and five games at once on
// a Saturday afternoon would otherwise wait on five round trips in turn.
func milcViews(gs []milclive.Game, now time.Time) []milclive.MarketView {
	out := make([]milclive.MarketView, len(gs))
	var wg sync.WaitGroup
	for i := range gs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out[i] = milclive.Markets(gs[i], now)
		}(i)
	}
	wg.Wait()
	return out
}

// milcTeamGames returns the games in window where query names a side.
//
// Whole words are tried first, so "Tigers" or "Chicago Kingsmen" pick
// exactly their team; any part of a name is the fallback, so
// "Philadelphia" still finds The Philadelphians. Which of the two applies
// is decided on the whole listing, not the window: "Star" is Lone Star
// Athletics, and on a day Lone Star are not playing the fallback would
// otherwise hand back Michigan Cricket Stars.
//
// A fixture, "Atlanta Fire v Atlanta Lightning", is read as its two teams,
// each by the same rule, and keeps the games where they are the two sides.
// That is the exact form cricket_live_matches prints, so it is what a
// client copies; read whole, it named no side, and the answer was "No
// Minor League Cricket team in Kalshi's listing matches" above a list
// holding both.
func milcTeamGames(window, all []milclive.Game, query string) []milclive.Game {
	if a, b, ok := splitFixture(query); ok {
		return fixtureMatches(window, a, wholeIn(window, all, a), b, wholeIn(window, all, b))
	}
	return teamMatches(window, query, wholeIn(window, all, query))
}

// wholeIn reports whether query names a side of some game in either list as
// whole words.
func wholeIn(window, all []milclive.Game, query string) bool {
	return len(teamMatches(all, query, true)) > 0 || len(teamMatches(window, query, true)) > 0
}

// splitFixture splits "A v B", "A vs B", "A vs. B" or "A versus B" into
// its two canonical team names. Canon has already dropped the period of
// "vs.". No Minor League team has a "v" or "vs" word in its name.
func splitFixture(query string) (a, b string, ok bool) {
	q := milclive.Canon(query)
	for _, sep := range []string{" versus ", " vs ", " v "} {
		if x, y, found := strings.Cut(q, sep); found {
			x, y = strings.TrimSpace(x), strings.TrimSpace(y)
			if x != "" && y != "" {
				return x, y, true
			}
		}
	}
	return "", "", false
}

// fixtureMatches returns the games in gs where a names one side and b the
// other, in either order. Two different sides: in a derby "Atlanta"
// names both, and "Atlanta v Atlanta" must not match a game twice over
// through one of them.
func fixtureMatches(gs []milclive.Game, a string, wholeA bool, b string, wholeB bool) []milclive.Game {
	var out []milclive.Game
	for _, g := range gs {
		if (nameMatches(g.Home, a, wholeA) && nameMatches(g.Away, b, wholeB)) ||
			(nameMatches(g.Away, a, wholeA) && nameMatches(g.Home, b, wholeB)) {
			out = append(out, g)
		}
	}
	return out
}

// fixtureName is the pairing in the fixture's own spelling when every game
// in gs is between the same two teams, and the query as typed otherwise.
func fixtureName(gs []milclive.Game, typed string) string {
	pair := func(g milclive.Game) string {
		h, a := milclive.Canon(g.Home), milclive.Canon(g.Away)
		if h > a {
			h, a = a, h
		}
		return h + " | " + a
	}
	if len(gs) == 0 {
		return fmt.Sprintf("%q", typed)
	}
	for _, g := range gs[1:] {
		if pair(g) != pair(gs[0]) {
			return fmt.Sprintf("%q", typed)
		}
	}
	return gs[0].Home + " v " + gs[0].Away
}

func teamMatches(gs []milclive.Game, query string, whole bool) []milclive.Game {
	q := milclive.Canon(query)
	if q == "" {
		return nil
	}
	var out []milclive.Game
	for _, g := range gs {
		if side := teamSide(g, q, whole); side != "" {
			out = append(out, g)
		}
	}
	return out
}

// teamSide is the side of g that the canonical query names, or "".
func teamSide(g milclive.Game, q string, whole bool) string {
	for _, side := range []string{g.Home, g.Away} {
		if nameMatches(side, q, whole) {
			return side
		}
	}
	return ""
}

// nameMatches reports whether the canonical query q names team: as whole
// words of it, or as any part of it.
func nameMatches(team, q string, whole bool) bool {
	c := milclive.Canon(team)
	if c == "" || q == "" {
		return false
	}
	if whole {
		return strings.Contains(" "+c+" ", " "+q+" ")
	}
	return strings.Contains(c, q)
}

// milcNothingOn answers when no game in the window matches: when the next
// one is, for the league or for the team asked about.
func milcNothingOn(team string, all []milclive.Game, now time.Time) string {
	if len(all) == 0 {
		return "Kalshi's Minor League Cricket listing is empty or unavailable right now, so there is no game to report and no next game to name."
	}
	if team == "" {
		s := "No Minor League Cricket game is on right now (" + milcWindowText + ")."
		if next, ok := nextGame(all, now); ok {
			return s + " The next one is " + next.Home + " v " + next.Away + ", " + milcStart(next) + "."
		}
		return s + " Kalshi lists no later Minor League Cricket game."
	}
	theirs := milcTeamGames(all, all, team)
	_, _, fixture := splitFixture(team)
	if len(theirs) == 0 {
		what := "team"
		if fixture {
			what = "game"
		}
		return fmt.Sprintf("No Minor League Cricket %s in Kalshi's listing matches %q. Teams listed: %s.",
			what, team, strings.Join(teamNames(all), ", "))
	}
	name := fixtureName(theirs, team)
	if !fixture {
		q := milclive.Canon(team)
		name = matchedName(theirs, q, wholeIn(nil, all, q), team)
	}
	s := "No Minor League Cricket game for " + name + " is on right now (" + milcWindowText + ")."
	if last, ok := lastResult(theirs, now); ok {
		s += " " + lastResultText(last, now)
	}
	if next, ok := nextGame(theirs, now); ok {
		return s + " Their next game is " + next.Home + " v " + next.Away + ", " + milcStart(next) + "."
	}
	return s + " Kalshi lists no later game for them."
}

// lastResult is the latest game in gs, which is in start order, that the
// data says finished.
//
// A game leaves the window two hours after its result, but the process
// keeps its live record: batchIDs asks about it until eight hours past the
// start, and nothing prunes what it answered. So "how did the Manhattan
// Yorkers do today?" asked that evening had the final score in memory and
// got only "not on right now" and the next fixture. Finished() is the only
// guard, and it is enough: a process launched after that eight-hour mark
// never fetched the game, has no live record, and reports no result
// rather than inventing one.
func lastResult(gs []milclive.Game, now time.Time) (milclive.Game, bool) {
	for i := len(gs) - 1; i >= 0; i-- {
		if g := gs[i]; g.Finished() && g.DisplayStart().Before(now) {
			return g, true
		}
	}
	return milclive.Game{}, false
}

// lastResultText is "Their last game, <Home> v <Away> on <day>, finished:
// <score>. <result>." A finished game's start is named by its day alone:
// the hour matters to nobody once it is over, and a start the two
// schedules disagree on would drag its caveat into a sentence about the
// result.
func lastResultText(g milclive.Game, now time.Time) string {
	s := "Their last game, " + g.Home + " v " + g.Away + " on " +
		g.DisplayStart().In(milclive.Eastern()).Format("Mon Jan 2") + ", finished"
	if line := g.ScoreLine(now); line != "" {
		s += ": " + line
	}
	return s + ". " + endSentence(g.Result())
}

// nextGame is the soonest game in gs, which is in start order, that has
// not started.
func nextGame(gs []milclive.Game, now time.Time) (milclive.Game, bool) {
	for _, g := range gs {
		if g.State(now) == "pre" && g.DisplayStart().After(now) {
			return g, true
		}
	}
	return milclive.Game{}, false
}

// matchedName is the team's own spelling when the query names exactly one
// team, and the query as typed otherwise ("Chicago" is two teams).
func matchedName(gs []milclive.Game, q string, whole bool, typed string) string {
	names := map[string]bool{}
	for _, g := range gs {
		for _, side := range []string{g.Home, g.Away} {
			if nameMatches(side, q, whole) {
				names[side] = true
			}
		}
	}
	if len(names) == 1 {
		for n := range names {
			return n
		}
	}
	return fmt.Sprintf("%q", typed)
}

func teamNames(gs []milclive.Game) []string {
	seen := map[string]bool{}
	var out []string
	for _, g := range gs {
		for _, side := range []string{g.Home, g.Away} {
			if !seen[side] {
				seen[side] = true
				out = append(out, side)
			}
		}
	}
	sort.Strings(out)
	return out
}

// milcGameText is one game's block: title, state, start, score, what the
// chase needs or the result, and the win chances or why there are none.
func milcGameText(g milclive.Game, v milclive.MarketView, now time.Time) string {
	state := g.State(now)
	var b strings.Builder
	fmt.Fprintf(&b, "%s v %s (%s)\n", g.Home, g.Away, milclive.League)
	fmt.Fprintf(&b, "State: %s\n", milcStateText(g, now))
	fmt.Fprintf(&b, "Start: %s\n", milcStart(g))
	if score := milcScore(g, now); score != "" {
		fmt.Fprintf(&b, "Score: %s\n", score)
	} else if state == "in" {
		if g.Usable(now) {
			b.WriteString("Score: none yet in Kalshi's live data.\n")
		} else {
			b.WriteString("Score: none. " + milcNoDataText(g) + "\n")
		}
	}
	if state == "post" {
		if g.Finished() {
			b.WriteString("Result: " + endSentence(g.Result()) + "\n")
		} else {
			b.WriteString("Result: unknown. " + milcNoDataText(g) + " Its scheduled window has passed, so whether and how it finished is not known.\n")
		}
		return b.String()
	}
	if sit := g.Situation(now); sit != "" {
		b.WriteString(endSentence(sit) + "\n")
	}
	if table, ok := milcChances(g, v, now); ok {
		b.WriteString(table + "\n")
	} else {
		b.WriteString(milcNoChances(g, v, now) + "\n")
	}
	return b.String()
}

// milcStateText is the game's state in words, as the site's chat block
// states it.
func milcStateText(g milclive.Game, now time.Time) string {
	switch g.State(now) {
	case "post":
		if !g.Finished() {
			return "unknown"
		}
		return "finished"
	case "in":
		if ph := g.Phase(now); ph != "" {
			if strings.Contains(strings.ToLower(ph), "delay") {
				return "delayed: " + strings.ToLower(ph)
			}
			return "in progress: " + strings.ToLower(ph)
		}
		return "in progress"
	}
	if !now.Before(g.DisplayStart()) {
		if g.StartUnconfirmed() {
			return "not started (start time unconfirmed)"
		}
		return "not started (start delayed)"
	}
	return "not started"
}

// milcChances is the chance-of-winning table, built by the site's rule: the
// market's midpoints when the book is priced, our model beside them only in
// a chase it may price. A finished game has none, and neither does a game
// in play whose live data is too old to state: a price beside no score,
// with the game going on, reads as a picture of now that nothing backs.
func milcChances(g milclive.Game, v milclive.MarketView, now time.Time) (string, bool) {
	state := g.State(now)
	if state == "post" || (state == "in" && !g.Usable(now)) {
		return "", false
	}
	priced := v.Status == "priced" && v.Set != nil
	homeModel, awayModel := -1, -1
	if st, ok := g.ModelAllowed(now); ok {
		bat := wholePct(explainer.WinProbability(st).BattingTeamWinProb)
		if st.BattingTeam == g.Home {
			homeModel, awayModel = bat, 100-bat
		} else {
			homeModel, awayModel = 100-bat, bat
		}
	}
	if !priced && homeModel < 0 {
		return "", false
	}
	var b strings.Builder
	if state == "pre" || !g.Live.HadScoring {
		b.WriteString("Chance of winning, before a ball is bowled:")
	} else {
		b.WriteString("Chance of winning, right now:")
	}
	row := func(team string, market, model int) {
		var cols []string
		if priced {
			cols = append(cols, fmt.Sprintf("%d%% (market)", market))
		}
		if model >= 0 {
			cols = append(cols, fmt.Sprintf("%d%% (model)", model))
		}
		fmt.Fprintf(&b, "\n• %s wins: %s", team, strings.Join(cols, ", "))
	}
	row(g.Home, v.HomePct, homeModel)
	row(g.Away, v.AwayPct, awayModel)
	return b.String(), true
}

// milcNoChances says why a game that is not over has no numbers, in the
// reader's terms, instead of leaving the question open.
func milcNoChances(g milclive.Game, v milclive.MarketView, now time.Time) string {
	clause := milcMarketClause(v.Status)
	laterModel := "our model only prices Minor League Cricket once the chase starts, because these teams have no ratings"
	state := g.State(now)
	switch {
	case state == "in" && !g.Usable(now):
		return "Win chances: none while Kalshi's live data for this game is out of date."
	case state == "pre" || !g.Scored(now) || g.Live.Innings <= 1:
		return "Win chances: none yet. " + upperFirst(clause) + ", and " + laterModel + "."
	}
	why := "Kalshi's live data doesn't give a consistent target for this chase yet"
	switch st, ok := g.ChaseState(now); {
	case ok && st.TotalOvers < 15:
		why = "games shorter than 15 overs are outside what it was fitted on"
	case strings.Contains(strings.ToLower(g.Phase(now)), "delay"):
		why = "play is stopped, and the overs may yet be cut"
	case !g.Fresh(now):
		why = "the score is more than a minute old"
	}
	return "Win chances: none. " + upperFirst(clause) + ", and our model doesn't price this chase because " + why + "."
}

// milcMarketClause names which absence of a price this is.
func milcMarketClause(status string) string {
	switch status {
	case "unpriced":
		return "Kalshi's market hasn't formed a real price yet (its book is too wide or untraded)"
	case "onesided":
		return "Kalshi's market is one-sided right now (one side bid near certainty, the other not bid), so there is no two-sided price"
	case "closed":
		return "its Kalshi market has closed"
	case "unavailable":
		return "Kalshi's data for it is unavailable right now"
	case "priced":
		return "Kalshi's market is priced, but there is no score to set it beside"
	}
	return "no open Kalshi market was found for it"
}

// milcScore is the score line, dated when it is between one and ten
// minutes old. A final score cannot go stale and is never dated.
func milcScore(g milclive.Game, now time.Time) string {
	line := g.ScoreLine(now)
	if line == "" {
		return ""
	}
	if g.Live != nil && !g.Fresh(now) && !g.Finished() {
		line += " (as of " + etClock(g.Live.FetchedAt, now) + ")"
	}
	return line
}

// milcNoDataText is the line for a game with no live data to state.
func milcNoDataText(g milclive.Game) string {
	if g.Live == nil || g.Live.FetchedAt.IsZero() {
		return "Kalshi's live data for this game is unavailable right now."
	}
	return "Kalshi's live data for this game hasn't updated since " + etStamp(g.Live.FetchedAt) + "."
}

// ------------------------------------------------ the two general tools

// milcOddsNote closes a Minor League cricket_market_odds answer.
const milcOddsNote = "A market figure is the midpoint of Kalshi's order book, given only when the book is tight or well traded on both sides. " +
	"Most Minor League books are wide placeholders nobody has traded, and their midpoint is not a price. " +
	"cricket_minor_league has each game's score and situation, and during the chase this server's model beside the market.\n" +
	"Informational only, not advice. Event contracts are legal in some US states and not others."

// milcPairGames returns the listed Minor League games between the two teams
// named, in start order. Exact names under Canon, the way MarketSide binds
// a market to a side, never tokens: a derby's two sides share a word, and
// "Chicago" is two teams.
func milcPairGames(all []milclive.Game, a, b string) []milclive.Game {
	ca, cb := milclive.Canon(a), milclive.Canon(b)
	if ca == "" || cb == "" || ca == cb {
		return nil
	}
	var out []milclive.Game
	for _, g := range all {
		h, w := milclive.Canon(g.Home), milclive.Canon(g.Away)
		if (ca == h && cb == w) || (ca == w && cb == h) {
			out = append(out, g)
		}
	}
	return out
}

// milcEventGames is the backstop behind the exact names. BestMatch finds
// "Lashings" v "Thunderbolts" by a word, and the market it lands on is a
// Minor League market all the same; when any of ms belongs to a listed
// game's event, the answer is that pairing's games.
func milcEventGames(all []milclive.Game, ms []kalshi.Market) []milclive.Game {
	for _, m := range ms {
		ev := kalshi.EventTickerOf(m.Ticker)
		if ev == "" {
			continue
		}
		for _, g := range all {
			if g.EventTicker != "" && strings.EqualFold(g.EventTicker, ev) {
				return milcPairGames(all, g.Home, g.Away)
			}
		}
	}
	return nil
}

// milcOddsText is cricket_market_odds for a Minor League pairing: each game
// not yet over, by its own event, with a number only when milclive.Markets
// calls the book priced, and otherwise the reason there is none. When every
// game between the two is over, the latest one says its market has closed.
//
// The same two teams play on consecutive days, so games are listed apart,
// each under its start: one answer used to mix Saturday's traded book with
// Sunday's placeholder, because the scan grouped both by their shared
// title.
func milcOddsText(gs []milclive.Game, now time.Time) string {
	var show []milclive.Game
	for _, g := range gs {
		if g.State(now) != "post" {
			show = append(show, g)
		}
	}
	if len(show) == 0 {
		show = gs[len(gs)-1:]
	}
	views := milcViews(show, now)
	var b strings.Builder
	fmt.Fprintf(&b, "Kalshi's %s markets for %s v %s:\n", milclive.League, show[0].Home, show[0].Away)
	for i, g := range show {
		state := milcStateText(g, now)
		if state == "unknown" {
			state = "state unknown"
		}
		fmt.Fprintf(&b, "%s, %s:", milcStart(g), state)
		if v := views[i]; v.Status == "priced" && v.Set != nil {
			fmt.Fprintf(&b, "\n• %s wins: %d%% (market)\n• %s wins: %d%% (market)\n", g.Home, v.HomePct, g.Away, v.AwayPct)
			continue
		}
		b.WriteString(" no price. " + upperFirst(milcMarketClause(views[i].Status)) + ".\n")
	}
	b.WriteString(milcOddsNote)
	return b.String()
}

// milcModelRefusal is why our model will not price a state that names a
// Minor League Cricket team, or nil when it may.
//
// cricket_win_probability prices any state it is handed. The site does
// not: it prices a typed Minor League score only in a chase with a target,
// of 15 overs or more, because these teams have no ratings and a
// first-innings number is the global T20 par under a Minor League name.
// An assistant that copied the Score line out of cricket_minor_league into
// this calculator got that number anyway, from the same server that had
// just told it the league is priced only in the chase. The team is matched
// by full name under Canon, as NamedIn does: "Titans" alone is also
// Gujarat's.
func milcModelRefusal(batting, bowling string, st explainer.MatchState, now time.Time) error {
	if batting == "" && bowling == "" {
		return nil
	}
	team := milcTeamNamed(milclive.AllGames(now), batting, bowling)
	if team == "" {
		return nil
	}
	var why string
	switch {
	case st.Innings < 2:
		why = "our model only prices Minor League Cricket once the chase starts, because these teams have no ratings"
	case st.Target == nil:
		why = "our model prices a Minor League chase only against its target, and none was given"
	case st.TotalOvers < 15:
		why = "our model doesn't price a Minor League chase shorter than 15 overs, because games that short are outside what it was fitted on"
	default:
		return nil
	}
	return fmt.Errorf("no number: %s is a Minor League Cricket team, and %s. "+
		"cricket_minor_league has the game's score, and Kalshi's market when its book is a real price", team, why)
}

// milcTeamNamed is the listed spelling of the first of names that is a
// Minor League team's full name, or "".
func milcTeamNamed(all []milclive.Game, names ...string) string {
	for _, n := range names {
		c := milclive.Canon(n)
		if c == "" {
			continue
		}
		for _, g := range all {
			for _, side := range []string{g.Home, g.Away} {
				if milclive.Canon(side) == c {
					return side
				}
			}
		}
	}
	return ""
}

// milcStart is the start in Eastern time, always with the day: an MCP
// client has no "today" to read a bare clock against. When Kalshi's two
// starts disagree by more than an hour the earlier is shown, flagged, with
// the other beside it.
func milcStart(g milclive.Game) string {
	s := etStamp(g.DisplayStart())
	if other, fromTicker := g.OtherStart(); !other.IsZero() {
		src := "Kalshi's schedule also lists"
		if fromTicker {
			src = "Kalshi's market lists"
		}
		s += " (time unconfirmed: " + src + " " + etClock(other, g.DisplayStart()) + ")"
	}
	return s
}

// etStamp is "Sat Sep 26, 3:30 PM ET".
func etStamp(t time.Time) string {
	return t.In(milclive.Eastern()).Format("Mon Jan 2, 3:04 PM") + " ET"
}

// etClock is "3:30 PM ET" when t falls on the same Eastern day as ref, and
// the full stamp otherwise.
func etClock(t, ref time.Time) string {
	et, re := t.In(milclive.Eastern()), ref.In(milclive.Eastern())
	if et.Year() == re.Year() && et.YearDay() == re.YearDay() {
		return et.Format("3:04 PM") + " ET"
	}
	return etStamp(t)
}

// wholePct rounds a probability to a whole percentage the way the site's
// table does.
func wholePct(p float64) int { return int(p*100 + 0.5) }

// endSentence ends s with a period unless it already ends a sentence.
func endSentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasSuffix(s, ".") || strings.HasSuffix(s, "!") || strings.HasSuffix(s, "?") {
		return s
	}
	return s + "."
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
