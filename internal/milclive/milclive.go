// Package milclive reads Minor League Cricket fixtures and live scores from
// Kalshi.
//
// Minor League Cricket is on no feed a server can reach: ESPN does not carry
// it, nobody sells it, and CricClubs puts every server behind a bot
// challenge. What changed is that Kalshi lists the games, and alongside the
// markets it publishes two public endpoints nobody documents:
//
//   - /milestones?competition=Minor League Cricket — the fixture list, one
//     call for the whole weekend (21 games, 16 KB on 2026-09-26);
//   - /live_data/batch?milestone_ids=... — the score of every one of them,
//     one call, licensed by Kalshi from a scoring provider.
//
// This package owns fetching, caching and decoding those, and the pure
// derivation of what state a game is in. It deliberately never builds a
// cricinfo.Match: a Minor League game going through ToMatchState would get
// an ESPN league id, a win table priced with Elo it does not have, and a
// par fitted on professional cricket. The MCP tools turn a Game into their
// own answer instead (internal/mcp/milc.go).
//
// The endpoints are undocumented, so every decode is lenient per item and
// every failure degrades to "no score", never to a wrong one. MILC_LIVE=off
// turns the whole package into a no-op.
package milclive

import (
	// The runtime image carries no tzdata. Kalshi's event tickers encode
	// the start in New York time, and without this import LoadLocation
	// fails on the server while passing on a Mac.
	_ "time/tzdata"

	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// League is the name every row, card and chat line uses. Never "MiLC":
	// a first-time reader has no idea what that is.
	League = "Minor League Cricket"

	// ScorecardsURL is the league's results page. Per-match scorecard links
	// exist only once a match has started, so they cannot be built for the
	// games a reader is being pointed at; this lands them one click away
	// for every match instead of being right for a third of them. It
	// carries a season id and changes every year.
	ScorecardsURL = "https://cricclubs.com/MiLC/results?leagueId=4gjH8PVcUeCazKJUkOOrOA&year=2026&series=Blc41vvV_UlHFvY3oOajUg&division=all&seriesName=MiLC+2026"

	// WatchURL is the broadcaster's channel streams page. Per-match video
	// links needed a weekly manual YouTube OAuth run, which lapsed on Sep 22
	// and left production with no rows at all; the channel page never goes
	// stale.
	WatchURL = "https://www.youtube.com/channel/UC3vvdOvioadhn-ZNjOa-tqg/streams"
)

var (
	apiBase = "https://api.elections.kalshi.com/trade-api/v2"
	client  = &http.Client{Timeout: 8 * time.Second}
)

// Fixture is one scheduled game, from the milestones listing.
type Fixture struct {
	ID, Home, Away, EventTicker, Status string
	// Start is the milestone's start. TickerStart is the one encoded in the
	// event ticker, in New York time. They disagree by four hours on at
	// least one listed game, and neither is proven right.
	Start, TickerStart time.Time
	// MaxOv and StatusTarget come from the status text of a reduced game:
	// "(MaxOv:16, Target: 99)". Zero when absent.
	MaxOv, StatusTarget int
}

// Side is one team's innings as the live data reports it.
type Side struct {
	Name       string
	Runs, Wkts int
	Overs      string // cricket notation, as sent: "13.1"
	Balls      int
	OversOK    bool // Overs parsed as legal notation
}

// Live is one game's live data. Only the fields below are decoded: the
// payload also carries a bookmaker's win probabilities and prices, and
// those are never read into memory, so they cannot leak into a card or a
// prompt. TestNoBookmakerFieldsDecoded pins that.
type Live struct {
	Widget, Status, StatusText, Batting, LastPlay, MatchComment, Winner, Orientation string
	// The scoreboard block's own names: SBHome/SBAway can differ from the
	// milestone's (Kings XI Dallas is "All Stars" there). SBBatting is the
	// "Current Batting Team", SBBatting1 the side that batted first.
	SBHome, SBAway, SBBatting, SBBatting1 string
	HadScoring                            bool
	Innings                               int
	Home, Away                            Side
	FetchedAt                             time.Time
}

// Game is a fixture plus whatever live data exists for it.
type Game struct {
	Fixture
	Live *Live
	// FinishedAt is when this process first saw the game finished.
	FinishedAt time.Time
	// FinishedOnFirstSight marks a game that was already over the first
	// time this process read it — after a deploy, say — so FinishedAt says
	// nothing about when it actually ended.
	FinishedOnFirstSight bool
	// Interrupted marks a game this process has seen stopped by rain,
	// weather or a delay at or after its start. Its overs may have been cut
	// without Kalshi saying to how many, so TotalOvers stops assuming 20.
	Interrupted bool
}

// ------------------------------------------------------------------ decode

// numText keeps a number's raw text. It never errors, so one odd field
// cannot fail the decode of the item around it; a value that does not
// parse as a number is caught afterwards and drops that item alone.
type numText string

func (n *numText) UnmarshalJSON(b []byte) error {
	t := strings.TrimSpace(string(b))
	switch {
	case t == "null":
		*n = ""
	case strings.HasPrefix(t, `"`):
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			*n = numText(t)
			return nil
		}
		*n = numText(strings.TrimSpace(s))
	default:
		*n = numText(t)
	}
	return nil
}

// whole reads the text as a whole number. Empty counts as zero; anything
// else that is not a number is reported, so the caller can drop the item
// rather than show a zero that was never sent.
func (n numText) whole() (int, bool) {
	if n == "" {
		return 0, true
	}
	f, err := strconv.ParseFloat(string(n), 64)
	if err != nil || f < 0 || f > 1000 {
		return 0, false
	}
	return int(f), true
}

// flexBool accepts true, "true" or 1.
type flexBool bool

func (f *flexBool) UnmarshalJSON(b []byte) error {
	t := strings.ToLower(strings.Trim(strings.TrimSpace(string(b)), `"`))
	*f = t == "true" || t == "1"
	return nil
}

// rawMilestone is the whitelist of milestone fields read.
type rawMilestone struct {
	ID                  string   `json:"id"`
	Type                string   `json:"type"`
	Title               string   `json:"title"`
	StartDate           string   `json:"start_date"`
	PrimaryEventTickers []string `json:"primary_event_tickers"`
	Details             struct {
		League              string `json:"league"`
		Status              string `json:"status"`
		MainGameEventTicker string `json:"main_game_event_ticker"`
	} `json:"details"`
}

// rawLive is the whitelist of live_data fields read. There is no field
// here for *_win_probability, *_price, *_back_price, *_lay_price,
// odds_status or odds_market_id, and there must never be one: the
// provider's bookmaker probability is neither the market nor our model.
type rawLive struct {
	MilestoneID string `json:"milestone_id"`
	Details     struct {
		WidgetStatus    string  `json:"widget_status"`
		Status          string  `json:"status"`
		Batting         string  `json:"batting"`
		CurrentInning   numText `json:"current_inning"`
		HomeScore       numText `json:"home_score"`
		HomeWickets     numText `json:"home_wickets"`
		HomeOvers       numText `json:"home_overs"`
		AwayScore       numText `json:"away_score"`
		AwayWickets     numText `json:"away_wickets"`
		AwayOvers       numText `json:"away_overs"`
		LastPlay        string  `json:"last_play"`
		Winner          string  `json:"winner"`
		SideOrientation string  `json:"side_orientation"`
		CricketResult   struct {
			HadScoring flexBool `json:"had_scoring"`
			StatusText string   `json:"status_text"`
			Scoreboard struct {
				Home         string `json:"home"`
				Away         string `json:"away"`
				Batting      string `json:"Current Batting Team"`
				Batting1     string `json:"batting.1"`
				MatchComment string `json:"match_comment"`
			} `json:"scoreboard"`
		} `json:"cricket_result"`
	} `json:"details"`
}

// ParseMilestones decodes one milestones page, one item at a time, so a
// single malformed item is dropped (and counted) while the rest survive.
// Items that are not a Minor League Cricket match are skipped silently: the
// competition filter is applied again here because the fallback query asks
// for every cricket match.
func ParseMilestones(body []byte) (fx []Fixture, cursor string, dropped int) {
	var page struct {
		Cursor     string            `json:"cursor"`
		Milestones []json.RawMessage `json:"milestones"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, "", 1
	}
	for _, raw := range page.Milestones {
		var m rawMilestone
		if err := json.Unmarshal(raw, &m); err != nil {
			dropped++
			continue
		}
		if m.Type != "cricket_match" || m.Details.League != League {
			continue
		}
		home, away, ok := strings.Cut(m.Title, " vs ")
		home, away = strings.TrimSpace(home), strings.TrimSpace(away)
		start, err := time.Parse(time.RFC3339, m.StartDate)
		if m.ID == "" || !ok || home == "" || away == "" || err != nil {
			dropped++
			continue
		}
		ticker := strings.ToUpper(strings.TrimSpace(m.Details.MainGameEventTicker))
		if ticker == "" && len(m.PrimaryEventTickers) > 0 {
			ticker = strings.ToUpper(strings.TrimSpace(m.PrimaryEventTickers[0]))
		}
		f := Fixture{
			ID: m.ID, Home: home, Away: away, EventTicker: ticker,
			Status: m.Details.Status, Start: start.UTC(),
		}
		if ts, ok := TickerStart(ticker); ok {
			f.TickerStart = ts
		}
		f.MaxOv, f.StatusTarget = reducedFrom(m.Details.Status)
		fx = append(fx, f)
	}
	return fx, page.Cursor, dropped
}

// ParseLiveBatch decodes a live_data batch, one item at a time. An item
// whose numbers are not numbers is dropped whole: a score of zero that was
// never sent is worse than no score.
func ParseLiveBatch(body []byte, fetchedAt time.Time) (lives map[string]*Live, dropped int) {
	var page struct {
		LiveDatas []json.RawMessage `json:"live_datas"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, 1
	}
	lives = make(map[string]*Live, len(page.LiveDatas))
	for _, raw := range page.LiveDatas {
		var r rawLive
		if err := json.Unmarshal(raw, &r); err != nil || r.MilestoneID == "" {
			dropped++
			continue
		}
		d := r.Details
		sb := d.CricketResult.Scoreboard
		l := &Live{
			Widget: strings.ToLower(strings.TrimSpace(d.WidgetStatus)), Status: d.Status,
			StatusText: d.CricketResult.StatusText, Batting: strings.ToLower(strings.TrimSpace(d.Batting)),
			LastPlay: strings.TrimSpace(d.LastPlay), MatchComment: strings.TrimSpace(sb.MatchComment),
			Winner: strings.ToLower(strings.TrimSpace(d.Winner)), Orientation: strings.ToLower(strings.TrimSpace(d.SideOrientation)),
			SBHome: sb.Home, SBAway: sb.Away, SBBatting: strings.TrimSpace(sb.Batting), SBBatting1: strings.TrimSpace(sb.Batting1),
			HadScoring: bool(d.CricketResult.HadScoring), FetchedAt: fetchedAt,
		}
		inn, ok1 := d.CurrentInning.whole()
		hr, ok2 := d.HomeScore.whole()
		hw, ok3 := d.HomeWickets.whole()
		ar, ok4 := d.AwayScore.whole()
		aw, ok5 := d.AwayWickets.whole()
		if !(ok1 && ok2 && ok3 && ok4 && ok5) || hw > 10 || aw > 10 {
			dropped++
			continue
		}
		// had_scoring false means the numbers are placeholders, and the
		// innings counter already reads 1 at the toss.
		if l.HadScoring {
			l.Innings = inn
			l.Home = Side{Runs: hr, Wkts: hw, Overs: string(d.HomeOvers)}
			l.Away = Side{Runs: ar, Wkts: aw, Overs: string(d.AwayOvers)}
			for _, s := range []*Side{&l.Home, &l.Away} {
				if s.Balls, s.OversOK = ParseOvers(s.Overs); !s.OversOK && s.Overs != "" {
					log.Printf("milc: overs %q rejected for %s", s.Overs, r.MilestoneID)
				}
			}
		}
		lives[r.MilestoneID] = l
	}
	return lives, dropped
}

// NewGame joins a fixture to its live data. A reduced game's MaxOv and
// Target reach the milestone's status only some of the time; the live
// status text carries them too, so the fixture's gaps are filled from it.
func NewGame(f Fixture, l *Live) Game {
	g := Game{Fixture: f}
	if l == nil {
		return g
	}
	// A copy: the sides are named here, and the caller's record is shared.
	cp := *l
	cp.Home.Name, cp.Away.Name = f.Home, f.Away
	g.Live = &cp
	for _, s := range []string{cp.StatusText, cp.Status} {
		mo, tg := reducedFrom(s)
		if g.MaxOv == 0 && mo > 0 {
			g.MaxOv = mo
		}
		if g.StatusTarget == 0 && tg > 0 {
			g.StatusTarget = tg
		}
	}
	return g
}

// reducedFrom pulls "MaxOv:16" and "Target: 99" out of a status text.
func reducedFrom(s string) (maxOv, target int) {
	if m := maxOvRe.FindStringSubmatch(s); m != nil {
		maxOv, _ = strconv.Atoi(m[1])
	}
	if m := targetRe.FindStringSubmatch(s); m != nil {
		target, _ = strconv.Atoi(m[1])
	}
	return maxOv, target
}

// ------------------------------------------------------------------- cache

// The cache follows cricinfo.fromCache's shape, copied rather than shared:
// serve whatever is held immediately, refresh behind it with at most one
// refresh in flight, and never let an error replace good data. Refresh is
// lazy — only Games, Lookup, Upcoming and Warm start one — so a process
// nobody is reading costs Kalshi nothing.
//
// Lazy has a cost the first reader after a quiet spell pays: what is held
// is as old as the quiet spell. So a reader waits, never more than
// firstWait, in exactly two cases — the process's first fill, and a refresh
// that will replace live data too old to price while a game is on. See
// ensure.
//
// Every time here is the caller's clock, never time.Now. That is what lets
// the tests run a whole Saturday from a capture without a clock bomb in
// them. The one exception is how long a reader waits, which is real time
// by nature.

const (
	fixturesTTL = 5 * time.Minute
	liveTTLHot  = 15 * time.Second
	liveTTLCold = 2 * time.Minute
	errorPause  = 30 * time.Second
	firstWait   = 1500 * time.Millisecond
	// freshWindow is Fresh's one minute: live data older than this is not
	// priced, so a refresh replacing it is worth a short wait.
	freshWindow = 60 * time.Second
	maxBatchIDs = 50
	maxPages    = 3
)

type cache struct {
	mu sync.Mutex

	fixtures      []Fixture
	fixturesAt    time.Time // when the fixtures were fetched; zero = never
	fixturesExp   time.Time
	fixturesPause time.Time

	live       map[string]*Live
	liveAt     time.Time
	liveExp    time.Time
	livePause  time.Time
	liveFailed bool // the last live refresh failed

	// finishedAt, firstSight and interrupted survive refreshes: they are
	// what this process has observed, not what the latest payload says.
	finishedAt  map[string]time.Time
	firstSight  map[string]bool // id -> first read was already finished
	interrupted map[string]bool // id -> delay text seen at or after the start
	seen        map[string]bool
	// attempted is set once the first refresh has ended, whatever its
	// outcome. The first-fill wait keys on it, not on fixturesAt: while the
	// listing kept failing, fixturesAt stayed zero and every request made
	// during a retry — every ESPN chat turn included, since chat switching
	// reads Games — waited again.
	attempted      bool
	inFlight       bool
	done           chan struct{} // closed when the in-flight refresh ends
	waitUntil      time.Time     // wall clock: when readers stop waiting on done; zero = don't
	lastErr        string
	lastPages      int
	fixtureDropped int
}

var st = newCache()

func newCache() *cache {
	return &cache{
		live:        map[string]*Live{},
		finishedAt:  map[string]time.Time{},
		firstSight:  map[string]bool{},
		interrupted: map[string]bool{},
		seen:        map[string]bool{},
	}
}

// SetBaseURL points the package at another host and empties the cache, and
// returns the function that restores both. Tests only. Switching hosts
// invalidates everything held, which is also what lets one test binary run
// two captures in turn.
func SetBaseURL(u string) (restore func()) {
	st.mu.Lock()
	prev := apiBase
	apiBase = strings.TrimRight(u, "/")
	st.mu.Unlock()
	reset()
	return func() {
		st.mu.Lock()
		apiBase = prev
		st.mu.Unlock()
		reset()
	}
}

func reset() {
	st.mu.Lock()
	done := st.done
	st.mu.Unlock()
	if done != nil {
		// Let a refresh against the old host land before the state it
		// would write into is replaced.
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
	}
	fresh := newCache()
	st.mu.Lock()
	st.fixtures, st.fixturesAt, st.fixturesExp, st.fixturesPause = nil, time.Time{}, time.Time{}, time.Time{}
	st.live, st.liveAt, st.liveExp, st.livePause, st.liveFailed = fresh.live, time.Time{}, time.Time{}, time.Time{}, false
	st.finishedAt, st.firstSight, st.interrupted, st.seen = fresh.finishedAt, fresh.firstSight, fresh.interrupted, fresh.seen
	st.attempted, st.inFlight, st.done, st.waitUntil = false, false, nil, time.Time{}
	st.lastErr, st.lastPages, st.fixtureDropped = "", 0, 0
	st.mu.Unlock()
}

func enabled() bool {
	return !strings.EqualFold(strings.TrimSpace(os.Getenv("MILC_LIVE")), "off")
}

// Enabled reports whether MILC_LIVE leaves the package on. Games returns
// nothing both when the league is switched off and when nothing is on, and
// an MCP answer has to say which: "no game right now" from a server that
// never looked is a false statement about the schedule.
func Enabled() bool { return enabled() }

// ensure starts a refresh when anything is due. It returns the channel that
// closes when the in-flight refresh ends (nil when none is running), and
// the wall-clock moment a reader should stop waiting for it (zero: do not
// wait at all).
//
// The moment belongs to the refresh, not the reader: everyone who starts or
// joins one refresh stops waiting at the same instant, so one chat turn that
// reads Games three times waits once, and a Kalshi that hangs for its whole
// 8-second timeout costs any reader at most firstWait.
//
// A refresh is worth waiting for in two cases only:
//
//   - The process's first one. Warm starts it at boot so the first visitor
//     after a deploy sees rows, not an empty list.
//   - One that replaces live data too old to price while a game is on. The
//     cache has no ticker, so after ten quiet minutes the next /matches was
//     built from ten-minute-old live data: Usable false, so a live game got
//     has_scores false and lost the page's auto-pick to an ESPN match, and
//     a game that finished in the gap still read "in" by the clock. The page
//     loads /matches once, so that visitor kept the wrong rows until reload.
//     Not after a failed refresh, though: an endpoint that is down would
//     make every reader wait on every retry.
func ensure(now time.Time) (done chan struct{}, waitUntil time.Time) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.inFlight {
		return st.done, st.waitUntil
	}
	fixDue := !now.Before(st.fixturesExp) && !now.Before(st.fixturesPause)
	liveDue := !now.Before(st.liveExp) && !now.Before(st.livePause)
	if !fixDue && !liveDue {
		return nil, time.Time{}
	}
	st.inFlight = true
	st.done = make(chan struct{})
	st.waitUntil = time.Time{}
	if !st.attempted || (liveDue && st.liveBehind(now)) {
		st.waitUntil = time.Now().Add(firstWait)
	}
	go refresh(now, fixDue, st.done, apiBase)
	return st.done, st.waitUntil
}

// liveBehind reports held live data too old to price while some listed game
// is in play or about to be. The caller holds st.mu.
func (c *cache) liveBehind(now time.Time) bool {
	if c.liveFailed || len(c.fixtures) == 0 {
		return false
	}
	if !c.liveAt.IsZero() && now.Sub(c.liveAt) <= freshWindow {
		return false
	}
	return liveTTL(c.fixtures, c.live, c.finishedAt, c.firstSight, c.interrupted, now) == liveTTLHot
}

// load makes sure data is there or on its way, waiting only when ensure
// says the refresh is worth it, and never past its deadline.
func load(now time.Time) {
	if !enabled() {
		return
	}
	done, until := ensure(now)
	if done == nil || until.IsZero() {
		return
	}
	d := time.Until(until)
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
	}
}

func refresh(now time.Time, fixDue bool, done chan struct{}, base string) {
	defer func() {
		st.mu.Lock()
		st.inFlight = false
		st.attempted = true
		st.mu.Unlock()
		close(done)
	}()
	if fixDue {
		fx, pages, dropped, err := fetchFixtures(base, now)
		st.mu.Lock()
		if err != nil {
			st.fixturesPause = now.Add(errorPause)
			st.lastErr = err.Error()
		} else {
			st.fixtures, st.fixturesAt, st.fixturesExp = fx, now, now.Add(fixturesTTL)
			st.lastPages, st.fixtureDropped, st.lastErr = pages, dropped, ""
		}
		st.mu.Unlock()
		if err != nil {
			log.Printf("milc: fixtures refresh failed: %v", err)
		} else {
			log.Printf("milc: fixtures=%d pages=%d dropped=%d", len(fx), pages, dropped)
		}
	}

	st.mu.Lock()
	liveDue := !now.Before(st.liveExp) && !now.Before(st.livePause)
	ids := batchIDs(st.fixtures, st.finishedAt, now)
	st.mu.Unlock()
	if !liveDue {
		return
	}
	if len(ids) == 0 {
		st.mu.Lock()
		st.liveExp = now.Add(liveTTLCold)
		st.mu.Unlock()
		return
	}
	lives, dropped, err := fetchLive(base, ids, now)
	st.mu.Lock()
	defer st.mu.Unlock()
	if err != nil {
		st.livePause = now.Add(errorPause)
		st.liveFailed = true
		st.lastErr = err.Error()
		log.Printf("milc: live refresh failed: %v", err)
		return
	}
	byID := make(map[string]Fixture, len(st.fixtures))
	for _, f := range st.fixtures {
		byID[f.ID] = f
	}
	for id, l := range lives {
		st.live[id] = l
		// Finished is judged on the joined game, not the raw record: a
		// chase that reached its target needs the fixture's reduced-game
		// target and overs to be read as over.
		fin := finished(l)
		if f, ok := byID[id]; ok {
			g := NewGame(f, l)
			if g.interruptionSeen(now) {
				st.interrupted[id] = true
			}
			g.Interrupted = st.interrupted[id]
			fin = g.Finished()
		}
		if !st.seen[id] {
			st.seen[id] = true
			st.firstSight[id] = fin
		}
		if fin && st.finishedAt[id].IsZero() {
			st.finishedAt[id] = now
		}
	}
	st.liveAt, st.liveFailed = now, false
	st.liveExp = now.Add(liveTTL(st.fixtures, st.live, st.finishedAt, st.firstSight, st.interrupted, now))
	log.Printf("milc: live=%d dropped=%d", len(lives), dropped)
}

// batchIDs picks the games worth asking about: listed soon, running, or
// just over. A game seen finished more than ten minutes ago keeps its last
// live data but is not asked about again — its score cannot change.
func batchIDs(fx []Fixture, finAt map[string]time.Time, now time.Time) []string {
	var ids []string
	for _, f := range fx {
		if now.Before(f.EarliestStart().Add(-3*time.Hour)) || now.After(f.Start.Add(8*time.Hour)) {
			continue
		}
		if at := finAt[f.ID]; !at.IsZero() && now.Sub(at) > 10*time.Minute {
			continue
		}
		ids = append(ids, f.ID)
		if len(ids) >= maxBatchIDs {
			break
		}
	}
	return ids
}

// liveTTL is 15 seconds while anything listed is in play or about to be,
// two minutes otherwise.
func liveTTL(fx []Fixture, live map[string]*Live, finAt map[string]time.Time, first, interrupted map[string]bool, now time.Time) time.Duration {
	for _, f := range fx {
		g := assemble(f, live, finAt, first, interrupted)
		if !g.InWindow(now) {
			continue
		}
		if g.State(now) == "in" || absDur(f.EarliestStart().Sub(now)) <= 30*time.Minute {
			return liveTTLHot
		}
	}
	return liveTTLCold
}

func fetchFixtures(base string, now time.Time) ([]Fixture, int, int, error) {
	since := now.Add(-36 * time.Hour).UTC().Format(time.RFC3339)
	list := base + "/milestones?limit=100&minimum_start_date=" + url.QueryEscape(since)
	fx, pages, dropped, err := walkMilestones(list + "&competition=" + url.QueryEscape(League))
	if err != nil {
		var he httpError
		if asHTTPError(err, &he) && he.code >= 400 && he.code < 500 {
			// The competition filter is the undocumented part. If it is
			// ever refused, ask for cricket matches only and filter here —
			// never the unfiltered all-sports listing.
			return walkMilestones(list + "&type=cricket_match")
		}
	}
	return fx, pages, dropped, err
}

func walkMilestones(u string) ([]Fixture, int, int, error) {
	var all []Fixture
	dropped, pages := 0, 0
	cursor := ""
	for pages < maxPages {
		next := u
		if cursor != "" {
			next += "&cursor=" + url.QueryEscape(cursor)
		}
		body, err := get(next)
		if err != nil {
			if pages == 0 {
				return nil, 0, 0, err
			}
			break
		}
		pages++
		fx, c, d := ParseMilestones(body)
		all = append(all, fx...)
		dropped += d
		if c == "" || c == cursor {
			break
		}
		cursor = c
	}
	// The same milestone can appear on two pages if the listing shifts
	// under the cursor.
	seen := map[string]bool{}
	out := all[:0]
	for _, f := range all {
		if !seen[f.ID] {
			seen[f.ID] = true
			out = append(out, f)
		}
	}
	return out, pages, dropped, nil
}

func fetchLive(base string, ids []string, now time.Time) (map[string]*Live, int, error) {
	q := url.Values{}
	for _, id := range ids {
		q.Add("milestone_ids", id)
	}
	body, err := get(base + "/live_data/batch?" + q.Encode())
	if err != nil {
		return nil, 0, err
	}
	lives, dropped := ParseLiveBatch(body, now)
	if lives == nil {
		return nil, dropped, fmt.Errorf("milc: live batch did not decode")
	}
	return lives, dropped, nil
}

type httpError struct{ code int }

func (e httpError) Error() string { return "HTTP " + strconv.Itoa(e.code) }

func asHTTPError(err error, out *httpError) bool {
	he, ok := err.(httpError)
	if ok {
		*out = he
	}
	return ok
}

func get(u string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, httpError{resp.StatusCode}
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

// assemble builds one Game from the cache's maps. Callers hold st.mu or
// pass copies.
func assemble(f Fixture, live map[string]*Live, finAt map[string]time.Time, first, interrupted map[string]bool) Game {
	var l *Live
	if src := live[f.ID]; src != nil {
		cp := *src
		l = &cp
	}
	g := NewGame(f, l)
	g.FinishedAt = finAt[f.ID]
	g.FinishedOnFirstSight = first[f.ID]
	g.Interrupted = interrupted[f.ID]
	return g
}

// snapshot returns every cached game, window or not.
func snapshot() []Game {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]Game, 0, len(st.fixtures))
	for _, f := range st.fixtures {
		out = append(out, assemble(f, st.live, st.finishedAt, st.firstSight, st.interrupted))
	}
	return out
}

// ----------------------------------------------------------------- exports

// Warm starts the first fill and waits for it, up to timeout. Called in a
// goroutine at boot so the first visitor after a deploy sees the rows.
func Warm(timeout time.Duration) {
	if !enabled() {
		return
	}
	done, _ := ensure(time.Now())
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

// Games returns the games worth listing now: in play first, then upcoming,
// then just finished, each soonest first.
func Games(now time.Time) []Game {
	if !enabled() {
		return nil
	}
	load(now)
	var out []Game
	for _, g := range snapshot() {
		if g.InWindow(now) {
			out = append(out, g)
		}
	}
	rank := map[string]int{"in": 0, "pre": 1, "post": 2}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank[out[i].State(now)], rank[out[j].State(now)]
		if ri != rj {
			return ri < rj
		}
		return out[i].DisplayStart().Before(out[j].DisplayStart())
	})
	return out
}

// Lookup finds any cached game by milestone id, listed or not, so a reader
// who selected a game keeps getting its final score after it leaves the
// list.
func Lookup(id string, now time.Time) (Game, bool) {
	if !enabled() || id == "" {
		return Game{}, false
	}
	load(now)
	for _, g := range snapshot() {
		if g.ID == id {
			return g, true
		}
	}
	return Game{}, false
}

// Upcoming returns up to n games that have not started and start within
// 48 hours, soonest first.
func Upcoming(now time.Time, n int) []Game {
	if !enabled() || n <= 0 {
		return nil
	}
	load(now)
	var out []Game
	for _, g := range snapshot() {
		ds := g.DisplayStart()
		if g.State(now) == "pre" && ds.After(now) && ds.Sub(now) <= 48*time.Hour {
			out = append(out, g)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].DisplayStart().Before(out[j].DisplayStart()) })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// AllGames returns every game the cached listing holds, in the window or
// not, earliest start first. It is for answers that reach past the window:
// "when is the next game" on a Monday, when the next one is Friday and
// Upcoming's 48 hours come back empty, and "does this team exist at all",
// which has to be answered from the whole listing rather than the few
// games on today.
func AllGames(now time.Time) []Game {
	if !enabled() {
		return nil
	}
	load(now)
	out := snapshot()
	sort.SliceStable(out, func(i, j int) bool { return out[i].DisplayStart().Before(out[j].DisplayStart()) })
	return out
}

// NamedIn returns the listed games whose full team name appears in msg as
// whole words. Full names only: "Titans" alone is also Gujarat's, and
// "Americans" and "Fire" are ordinary English.
func NamedIn(msg string, now time.Time) []Game {
	if !enabled() || strings.TrimSpace(msg) == "" {
		return nil
	}
	text := " " + Canon(msg) + " "
	var out []Game
	for _, g := range Games(now) {
		for _, team := range []string{g.Home, g.Away} {
			if c := Canon(team); c != "" && strings.Contains(text, " "+c+" ") {
				out = append(out, g)
				break
			}
		}
	}
	return out
}

// Canon is the comparison form of a team name: lower case, no periods,
// commas or apostrophes, "ft" spelled "fort", single spaces.
func Canon(name string) string {
	s := strings.ToLower(name)
	// Periods, commas and apostrophes are dropped ("St. Louis" is "St
	// Louis"); the rest of a sentence's punctuation separates words, so
	// "Kingsmen?" still ends on a word boundary.
	s = strings.NewReplacer(".", "", ",", "", "'", "", "’", "",
		"?", " ", "!", " ", "(", " ", ")", " ", ":", " ", ";", " ", "\"", " ", "/", " ").Replace(s)
	words := strings.Fields(s)
	for i, w := range words {
		if w == "ft" {
			words[i] = "fort"
		}
	}
	return strings.Join(words, " ")
}

// Health is the /health block: whether the package is on, how many
// fixtures it holds, how old the live data is, and the last error.
func Health() map[string]any {
	st.mu.Lock()
	defer st.mu.Unlock()
	age := -1
	if !st.liveAt.IsZero() {
		age = int(time.Since(st.liveAt).Seconds())
	}
	return map[string]any{
		"enabled":    enabled(),
		"fixtures":   len(st.fixtures),
		"live_age_s": age,
		"last_error": st.lastErr,
	}
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
