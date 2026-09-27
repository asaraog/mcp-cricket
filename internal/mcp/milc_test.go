package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/asaraog/mcp-cricket/internal/cricinfo"
	"github.com/asaraog/mcp-cricket/internal/kalshi"
	"github.com/asaraog/mcp-cricket/internal/milclive"
)

// Minor League Cricket is off for every test here unless a test turns it
// on with its own capture: nothing in this package may reach Kalshi, or
// depend on what Kalshi happens to list today.
func init() { os.Setenv("MILC_LIVE", "off") }

const (
	milcTestdata  = "../milclive/testdata/"
	kalshiCapture = "../kalshi/testdata/kxt20match_milc.json"
)

// A capture is a milestones listing and a live batch taken at one moment.
type milcCapture struct {
	milestones, live string
	at               time.Time
}

var (
	// 2026-09-26 12:36 PM PT: a toss, a rain delay, an innings break, two
	// games mid-innings, two finished and two to come.
	capture1936 = milcCapture{"milestones.json", "live_batch.json", time.Date(2026, 9, 26, 19, 36, 0, 0, time.UTC)}
	// 1:04 PM PT the same day: Atlanta Lightning chasing 206.
	capture2004 = milcCapture{"milestones_2004z.json", "live_batch_2004z.json", time.Date(2026, 9, 26, 20, 4, 0, 0, time.UTC)}
)

func readTestdata(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// useMilcCapture serves a capture as Kalshi's Minor League endpoints and
// fixes the clock at at. The live batch answers only the games asked for,
// as Kalshi's does: the capture holds all 21, and handing a Sunday-morning
// request Saturday's in-play records would list Saturday's games as live.
func useMilcCapture(t *testing.T, c milcCapture, at time.Time) {
	t.Helper()
	t.Setenv("MILC_LIVE", "on")
	ms := readTestdata(t, milcTestdata+c.milestones)
	var batch struct {
		LiveDatas []json.RawMessage `json:"live_datas"`
	}
	if err := json.Unmarshal(readTestdata(t, milcTestdata+c.live), &batch); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/milestones"):
			_, _ = w.Write(ms)
		case strings.HasSuffix(r.URL.Path, "/live_data/batch"):
			want := map[string]bool{}
			for _, id := range r.URL.Query()["milestone_ids"] {
				want[id] = true
			}
			out := []json.RawMessage{}
			for _, raw := range batch.LiveDatas {
				var item struct {
					MilestoneID string `json:"milestone_id"`
				}
				_ = json.Unmarshal(raw, &item)
				if want[item.MilestoneID] {
					out = append(out, raw)
				}
			}
			b, _ := json.Marshal(map[string]any{"live_datas": out})
			_, _ = w.Write(b)
		default:
			t.Errorf("unexpected Minor League request %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	restore := milclive.SetBaseURL(srv.URL)
	prev := milcNow
	milcNow = func() time.Time { return at }
	t.Cleanup(func() {
		milcNow = prev
		restore()
		srv.Close()
	})
}

// useKalshiCapture seeds the market scan with the Minor League events as
// they stood at 19:36Z, each market twice as the real scan lists them, and
// serves those events for the in-play path. Any other Kalshi request fails.
func useKalshiCapture(t *testing.T) {
	t.Helper()
	body := readTestdata(t, kalshiCapture)
	cands, _, err := kalshi.ParseEventsPage(body)
	if err != nil {
		t.Fatal(err)
	}
	kalshi.SeedScan(append(append([]kalshi.Candidate{}, cands...), cands...))
	var page struct {
		Events []json.RawMessage `json:"events"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatal(err)
	}
	events := map[string]json.RawMessage{}
	for _, raw := range page.Events {
		var e struct {
			EventTicker string `json:"event_ticker"`
		}
		_ = json.Unmarshal(raw, &e)
		events[strings.ToUpper(e.EventTicker)] = raw
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/events/"):
			raw, ok := events[strings.ToUpper(strings.TrimPrefix(r.URL.Path, "/events/"))]
			if !ok {
				http.NotFound(w, r)
				return
			}
			fmt.Fprintf(w, `{"event":%s}`, raw)
		default:
			t.Errorf("unexpected Kalshi request %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	restore := kalshi.SetBaseURL(srv.URL)
	t.Cleanup(func() {
		restore()
		srv.Close()
		kalshi.SeedScan(nil)
	})
}

func stubESPN(t *testing.T, ms []cricinfo.ListedMatch) {
	t.Helper()
	prev := liveESPN
	liveESPN = func() []cricinfo.ListedMatch { return ms }
	t.Cleanup(func() { liveESPN = prev })
}

// callTool calls a tool through tools/call, as a client does.
func callTool(t *testing.T, name string, args map[string]any) (text string, isError bool) {
	t.Helper()
	params, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
	res := rpc(t, "tools/call", string(params))
	m, ok := res.Result.(map[string]any)
	if !ok {
		t.Fatalf("%s: no result: %+v", name, res)
	}
	content, _ := m["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("%s: no content: %+v", name, m)
	}
	text, _ = content[0].(map[string]any)["text"].(string)
	return text, m["isError"] == true
}

// labelledOnly fails on any line that carries a percentage without saying
// whose it is. A bare number beside a Minor League game is either the
// provider's bookmaker probability or a placeholder's midpoint, and both
// are exactly what these tools must never print.
func labelledOnly(t *testing.T, out string) {
	t.Helper()
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "%") && !strings.Contains(ln, "(market)") && !strings.Contains(ln, "(model)") {
			t.Errorf("an unlabelled percentage: %q", ln)
		}
	}
}

// ------------------------------------------------------------------ listing

func TestMinorLeagueToolIsListedAndVersionMoved(t *testing.T) {
	res := rpc(t, "tools/list", `{}`)
	m, _ := res.Result.(map[string]any)
	tools, _ := m["tools"].([]Tool)
	var tool *Tool
	for i := range tools {
		if tools[i].Name == "cricket_minor_league" {
			tool = &tools[i]
		}
	}
	if tool == nil {
		t.Fatal("cricket_minor_league is not in tools/list")
	}
	if tool.Annotations["readOnlyHint"] != true || tool.Annotations["destructiveHint"] != false || tool.Annotations["title"] == "" {
		t.Errorf("annotations: %v", tool.Annotations)
	}
	props, _ := tool.InputSchema["properties"].(map[string]any)
	if _, ok := props["team"]; !ok {
		t.Errorf("no team argument: %v", tool.InputSchema)
	}
	if req, ok := tool.InputSchema["required"]; ok {
		t.Errorf("team must be optional, schema requires %v", req)
	}
	for _, want := range []string{"Minor League Cricket", "Kalshi", "no ball-by-ball"} {
		if !strings.Contains(tool.Description, want) {
			t.Errorf("description lacks %q", want)
		}
	}

	hello := rpc(t, "initialize", `{"protocolVersion":"2025-06-18"}`)
	im, _ := hello.Result.(map[string]any)
	info, _ := im["serverInfo"].(map[string]any)
	if info["version"] != ServerVersion || ServerVersion == "0.1.0" {
		t.Errorf("serverInfo.version = %v; clients keep 0.1.0's tool list until it moves", info["version"])
	}
}

// ------------------------------------------------------------- live list

func TestLiveMatchesEndsWithMinorLeague(t *testing.T) {
	useMilcCapture(t, capture1936, capture1936.at)
	stubESPN(t, []cricinfo.ListedMatch{{Title: "India v Australia", State: "in", Summary: "India 120/3"}})
	out, isErr := callTool(t, "cricket_live_matches", map[string]any{})
	if isErr {
		t.Fatalf("error: %s", out)
	}
	espn := strings.Index(out, "India v Australia — LIVE (India 120/3)")
	atl := strings.Index(out, "Atlanta Fire v Atlanta Lightning — Minor League Cricket — LIVE, innings break: Atlanta Fire 205/5 (20 ov) · Atlanta Lightning yet to bat, target 206\n")
	if espn != 0 || atl < 0 {
		t.Fatalf("ESPN first, then the Minor League games:\n%s", out)
	}
	for _, want := range []string{
		"Los Angeles Lashings v Seattle Thunderbolts — Minor League Cricket — upcoming starts Sat Sep 26, 5:00 PM ET\n",
		"Manhattan Yorkers v New Jersey Stallions — Minor League Cricket — finished: New Jersey Stallions 98 all out (15.5 ov) · Manhattan Yorkers 102/4 (12 ov). Manhattan Yorkers won by 6 wickets (reduced to 16 overs).\n",
		milcListNote,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// The site's window at 19:36Z: five in play, two to come, two finished.
	if n := strings.Count(out, " — Minor League Cricket — "); n != 9 {
		t.Errorf("%d Minor League lines, want the 9 in the window:\n%s", n, out)
	}
	if strings.Contains(out, "%") {
		t.Errorf("the list carries a percentage:\n%s", out)
	}
}

// With the league off, the list is exactly what it was.
func TestLiveMatchesWithoutMinorLeagueIsUnchanged(t *testing.T) {
	stubESPN(t, []cricinfo.ListedMatch{
		{Title: "India v Australia", State: "in", Summary: "India 120/3"},
		{Title: "England v South Africa", State: "pre", Date: "2026-09-27T09:00Z"},
	})
	out, _ := callTool(t, "cricket_live_matches", map[string]any{})
	if want := "India v Australia — LIVE (India 120/3)\nEngland v South Africa — upcoming starts 2026-09-27T09:00Z\n"; out != want {
		t.Errorf("got %q, want %q", out, want)
	}
	stubESPN(t, nil)
	if out, _ := callTool(t, "cricket_live_matches", map[string]any{}); out != "No matches listed right now." {
		t.Errorf("empty list: %q", out)
	}
}

// ------------------------------------------------------------ one league

// In the chase: the score, what the chasing side needs, and both numbers,
// the market at the book's midpoint and our model beside it.
func TestMinorLeagueLiveChase(t *testing.T) {
	useMilcCapture(t, capture2004, capture2004.at)
	useKalshiCapture(t)
	out, isErr := callTool(t, "cricket_minor_league", map[string]any{"team": "Lightning"})
	if isErr {
		t.Fatalf("error: %s", out)
	}
	if n := strings.Count(out, " (Minor League Cricket)\n"); n != 1 {
		t.Errorf("%d games for one team's single fixture:\n%s", n, out)
	}
	for _, want := range []string{
		"Atlanta Fire v Atlanta Lightning (Minor League Cricket)\nState: in progress\nStart: Sat Sep 26, 2:00 PM ET\n",
		"Score: Atlanta Fire 205/5 (20 ov) · Atlanta Lightning 28/4 (4.5/20 ov, target 206)\n",
		"Atlanta Lightning need 178 from 91 balls.\n",
		"Chance of winning, right now:\n• Atlanta Fire wins: 76% (market), ",
		"\n• Atlanta Lightning wins: 25% (market), ",
		"Scores and prices come from Kalshi's live data",
		"no ball-by-ball commentary and names no batters or bowlers",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	models := regexp.MustCompile(`(\d+)% \(model\)`).FindAllStringSubmatch(out, -1)
	if len(models) != 2 {
		t.Fatalf("model numbers %v, want one per side:\n%s", models, out)
	}
	a, _ := strconv.Atoi(models[0][1])
	b, _ := strconv.Atoi(models[1][1])
	if a+b != 100 {
		t.Errorf("model %d + %d != 100", a, b)
	}
	labelledOnly(t, out)
	// The provider's bookmaker had Atlanta Fire at 0.965 and Lightning at
	// 0.035 in this very record. Neither may surface in any form.
	for _, bm := range []string{"0.965", "96.5", "0.035", "3.5%", "decimal_cricket"} {
		if strings.Contains(out, bm) {
			t.Errorf("the bookmaker's %q reached the output:\n%s", bm, out)
		}
	}
}

// Every game on at 20:04Z, one call: each number is labelled, and none is
// the bookmaker's.
func TestMinorLeagueEveryGameIsLabelled(t *testing.T) {
	useMilcCapture(t, capture2004, capture2004.at)
	useKalshiCapture(t)
	out, isErr := callTool(t, "cricket_minor_league", map[string]any{})
	if isErr {
		t.Fatalf("error: %s", out)
	}
	if n := strings.Count(out, " (Minor League Cricket)\n"); n < 5 {
		t.Errorf("%d games, want every game in the window:\n%s", n, out)
	}
	if !strings.HasSuffix(out, milclive.ScorecardsURL) || strings.Count(out, "Scores and prices come from Kalshi's live data") != 1 {
		t.Errorf("the source note should close the answer once:\n%s", out)
	}
	labelledOnly(t, out)
	for _, p := range []string{"0.965", "0.729", "0.884", "0.812", "0.597", "0.449", "0.551"} {
		if strings.Contains(out, p) {
			t.Errorf("a bookmaker probability %s reached the output", p)
		}
	}
}

// Before the first ball, a priced book: the market only, captioned as a
// forecast, and never our model, which has no ratings for these teams.
func TestMinorLeaguePricedBeforeTheGame(t *testing.T) {
	useMilcCapture(t, capture1936, capture1936.at)
	useKalshiCapture(t)
	out, _ := callTool(t, "cricket_minor_league", map[string]any{"team": "Lashings"})
	want := "Los Angeles Lashings v Seattle Thunderbolts (Minor League Cricket)\nState: not started\nStart: Sat Sep 26, 5:00 PM ET\n" +
		"Chance of winning, before a ball is bowled:\n• Los Angeles Lashings wins: 56% (market)\n• Seattle Thunderbolts wins: 48% (market)\n"
	if !strings.HasPrefix(out, want) {
		t.Errorf("got:\n%s\nwant it to start:\n%s", out, want)
	}
	if strings.Contains(out, "(model)") || strings.Contains(out, "Score:") {
		t.Errorf("a game that has not started got a model number or a score:\n%s", out)
	}
	labelledOnly(t, out)
}

// Before the first ball, the Sunday placeholders: 23/73 and 24/72 books
// with nothing traded. No number at all, and the reason in words.
func TestMinorLeagueUnpricedBeforeTheGame(t *testing.T) {
	sunday := time.Date(2026, 9, 27, 12, 30, 0, 0, time.UTC) // 8:30 AM ET
	useMilcCapture(t, capture1936, sunday)
	useKalshiCapture(t)
	out, _ := callTool(t, "cricket_minor_league", map[string]any{})
	nyc := "NYC Titans v Manhattan Yorkers (Minor League Cricket)\nState: not started\nStart: Sun Sep 27, 10:00 AM ET\n" +
		"Win chances: none yet. Kalshi's market hasn't formed a real price yet (its book is too wide or untraded), and our model only prices Minor League Cricket once the chase starts, because these teams have no ratings.\n"
	if !strings.Contains(out, nyc) {
		t.Errorf("missing\n%s\nin:\n%s", nyc, out)
	}
	if !strings.Contains(out, "Chicago Kingsmen v St Louis Americans (Minor League Cricket)\nState: not started\nStart: Sun Sep 27, 11:00 AM ET (time unconfirmed: Kalshi's market lists 3:00 PM ET)\n") {
		t.Errorf("the disagreeing starts are not both shown:\n%s", out)
	}
	if strings.Contains(out, "%") || strings.Contains(out, "Chance of winning") {
		t.Errorf("a placeholder book produced a number:\n%s", out)
	}
	if strings.Contains(out, "Atlanta") || strings.Contains(out, "Score:") {
		t.Errorf("Saturday's games leaked into Sunday morning:\n%s", out)
	}
}

// A finished game: the final score and the result, and no price at all,
// since its book is settling.
func TestMinorLeagueFinished(t *testing.T) {
	useMilcCapture(t, capture1936, capture1936.at)
	useKalshiCapture(t)
	out, _ := callTool(t, "cricket_minor_league", map[string]any{"team": "Manhattan Yorkers"})
	want := "Manhattan Yorkers v New Jersey Stallions (Minor League Cricket)\nState: finished\nStart: Sat Sep 26, 10:00 AM ET\n" +
		"Score: New Jersey Stallions 98 all out (15.5 ov) · Manhattan Yorkers 102/4 (12 ov)\n" +
		"Result: Manhattan Yorkers won by 6 wickets (reduced to 16 overs).\n"
	if !strings.HasPrefix(out, want) {
		t.Errorf("got:\n%s\nwant it to start:\n%s", out, want)
	}
	if strings.Count(out, " (Minor League Cricket)\n") != 1 {
		t.Errorf("Sunday's NYC Titans v Manhattan Yorkers is outside the window:\n%s", out)
	}
	if strings.Contains(out, "%") || strings.Contains(out, "Chance of winning") || strings.Contains(out, "Win chances") {
		t.Errorf("a finished game carries win chances:\n%s", out)
	}
}

// Nothing on: say when the next game is, for the league or the team.
func TestMinorLeagueNothingOnNamesTheNextGame(t *testing.T) {
	night := time.Date(2026, 9, 27, 6, 0, 0, 0, time.UTC) // 2 AM ET Sunday
	useMilcCapture(t, capture1936, night)
	useKalshiCapture(t)
	cases := map[string]string{
		"": "No Minor League Cricket game is on right now (nothing live, starting within 3 hours, or finished in the last 2 hours). " +
			"The next one is NYC Titans v Manhattan Yorkers, Sun Sep 27, 10:00 AM ET.",
		"Kingsmen": "No Minor League Cricket game for Chicago Kingsmen is on right now (nothing live, starting within 3 hours, or finished in the last 2 hours). " +
			"Their next game is Chicago Kingsmen v St Louis Americans, Sun Sep 27, 11:00 AM ET (time unconfirmed: Kalshi's market lists 3:00 PM ET).",
	}
	for team, want := range cases {
		args := map[string]any{}
		if team != "" {
			args["team"] = team
		}
		if out, isErr := callTool(t, "cricket_minor_league", args); isErr || out != want {
			t.Errorf("team %q:\n got %q\nwant %q", team, out, want)
		}
	}
	out, _ := callTool(t, "cricket_minor_league", map[string]any{"team": "Mumbai Indians"})
	if !strings.HasPrefix(out, `No Minor League Cricket team in Kalshi's listing matches "Mumbai Indians". Teams listed: `) ||
		!strings.Contains(out, "Atlanta Fire") {
		t.Errorf("an unknown team: %q", out)
	}
}

// Switched off, the tool says so rather than "nothing is on".
func TestMinorLeagueSwitchedOff(t *testing.T) {
	out, isErr := callTool(t, "cricket_minor_league", map[string]any{})
	if !isErr || !strings.Contains(out, "switched off") {
		t.Errorf("MILC_LIVE=off: %q (error %v)", out, isErr)
	}
}

// Whole words first, any part of a name as the fallback — decided on the
// whole listing, so "Star" on a day Lone Star Athletics are not playing
// does not turn into Michigan Cricket Stars.
func TestMinorLeagueTeamMatching(t *testing.T) {
	game := func(home, away string) milclive.Game {
		return milclive.Game{Fixture: milclive.Fixture{ID: home, Home: home, Away: away}}
	}
	michigan := game("Michigan Cricket Stars", "Chicago Tigers")
	loneStar := game("Lone Star Athletics", "MetroPlex Tracers")
	philly := game("The Philadelphians", "NYC Titans")
	kingsmen := game("Chicago Kingsmen", "St Louis Americans")
	window := []milclive.Game{michigan, philly, kingsmen}
	all := []milclive.Game{loneStar, michigan, philly, kingsmen}

	ids := func(gs []milclive.Game) string {
		var out []string
		for _, g := range gs {
			out = append(out, g.ID)
		}
		return strings.Join(out, ",")
	}
	for query, want := range map[string]string{
		"Star":             "",
		"Stars":            "Michigan Cricket Stars",
		"Philadelphia":     "The Philadelphians",
		"tigers":           "Michigan Cricket Stars",
		"Chicago":          "Michigan Cricket Stars,Chicago Kingsmen",
		"St. Louis":        "Chicago Kingsmen",
		"Chicago Kingsmen": "Chicago Kingsmen",
		// A fixture, as cricket_live_matches prints it: each half by the
		// same rule, and each half a different side.
		"Chicago Kingsmen v St. Louis": "Chicago Kingsmen",
		"Tigers vs Stars":              "Michigan Cricket Stars",
		"Philadelphia versus Titans":   "The Philadelphians",
		"Chicago v Chicago":            "",
	} {
		if got := ids(milcTeamGames(window, all, query)); got != want {
			t.Errorf("%q matched %q, want %q", query, got, want)
		}
	}
	if !everyTeam("") || !everyTeam("Minor League Cricket") || !everyTeam("all") || everyTeam("Atlanta") {
		t.Error("everyTeam misreads a request for the whole league")
	}
}

// A fixture copied out of cricket_live_matches finds its game, in either
// order. Read as one name it matched no side, and the answer listed both
// teams under "No Minor League Cricket team ... matches".
func TestMinorLeagueFixtureQuery(t *testing.T) {
	useMilcCapture(t, capture1936, capture1936.at)
	useKalshiCapture(t)
	for _, q := range []string{"Atlanta Fire v Atlanta Lightning", "Atlanta Lightning vs. Atlanta Fire"} {
		out, isErr := callTool(t, "cricket_minor_league", map[string]any{"team": q})
		if isErr || !strings.HasPrefix(out, "Atlanta Fire v Atlanta Lightning (Minor League Cricket)\n") ||
			strings.Count(out, " (Minor League Cricket)\n") != 1 {
			t.Errorf("%q:\n%s", q, out)
		}
	}
	out, _ := callTool(t, "cricket_minor_league", map[string]any{"team": "Atlanta Fire v Chicago Tigers"})
	if !strings.HasPrefix(out, `No Minor League Cricket game in Kalshi's listing matches "Atlanta Fire v Chicago Tigers". Teams listed: `) {
		t.Errorf("a fixture that is not listed: %q", out)
	}

	night := time.Date(2026, 9, 27, 6, 0, 0, 0, time.UTC)
	useMilcCapture(t, capture1936, night)
	want := "No Minor League Cricket game for Chicago Kingsmen v St Louis Americans is on right now (nothing live, starting within 3 hours, or finished in the last 2 hours). " +
		"Their next game is Chicago Kingsmen v St Louis Americans, Sun Sep 27, 11:00 AM ET (time unconfirmed: Kalshi's market lists 3:00 PM ET)."
	if out, _ := callTool(t, "cricket_minor_league", map[string]any{"team": "Chicago Kingsmen v St. Louis Americans"}); out != want {
		t.Errorf("a fixture with nothing on:\n got %q\nwant %q", out, want)
	}
}

// A team whose game left the window two hours after it finished still has
// its result in memory, and the answer gives it. At 5 PM ET the Manhattan
// Yorkers' Saturday game was out of the window, and "how did they do?" got
// only "not on right now" and Sunday's fixture. (A process launched after
// the eight hours batchIDs asks about a game never fetched it, and reports
// no result: the Kingsmen case in TestMinorLeagueNothingOnNamesTheNextGame.)
func TestMinorLeagueNothingOnGivesTheLastResult(t *testing.T) {
	useMilcCapture(t, capture1936, time.Date(2026, 9, 26, 21, 0, 0, 0, time.UTC))
	want := "No Minor League Cricket game for Manhattan Yorkers is on right now (nothing live, starting within 3 hours, or finished in the last 2 hours). " +
		"Their last game, Manhattan Yorkers v New Jersey Stallions on Sat Sep 26, finished: New Jersey Stallions 98 all out (15.5 ov) · Manhattan Yorkers 102/4 (12 ov). " +
		"Manhattan Yorkers won by 6 wickets (reduced to 16 overs). " +
		"Their next game is NYC Titans v Manhattan Yorkers, Sun Sep 27, 10:00 AM ET."
	if out, isErr := callTool(t, "cricket_minor_league", map[string]any{"team": "Manhattan Yorkers"}); isErr || out != want {
		t.Errorf("\n got %q\nwant %q", out, want)
	}
}

// ---------------------------------------------------------- market odds

// cricket_market_odds on a Minor League pairing answers by the site's
// priced-book rule, each game under its own event. It used to print the
// Sunday NYC Titans v Manhattan Yorkers placeholder (24/72, nothing traded)
// as "48% implied (48 cents)" for both sides, every line twice, and to
// merge Saturday's Los Angeles book with Sunday's, which share a title.
func TestMarketOddsMinorLeagueNeverQuotesAPlaceholder(t *testing.T) {
	useMilcCapture(t, capture1936, capture1936.at)
	useKalshiCapture(t)
	const unpriced = "no price. Kalshi's market hasn't formed a real price yet (its book is too wide or untraded).\n"

	out, isErr := callTool(t, "cricket_market_odds", map[string]any{"team_a": "NYC Titans", "team_b": "Manhattan Yorkers"})
	nyc := "Kalshi's Minor League Cricket markets for NYC Titans v Manhattan Yorkers:\n" +
		"Sun Sep 27, 10:00 AM ET, not started: " + unpriced
	if isErr || !strings.HasPrefix(out, nyc) {
		t.Errorf("NYC Titans v Manhattan Yorkers:\n%s\nwant it to start:\n%s", out, nyc)
	}
	for _, bad := range []string{"%", "implied", "cents"} {
		if strings.Contains(out, bad) {
			t.Errorf("a placeholder book printed %q:\n%s", bad, out)
		}
	}
	if !strings.Contains(out, "cricket_minor_league") {
		t.Errorf("no pointer to cricket_minor_league:\n%s", out)
	}

	la := "Kalshi's Minor League Cricket markets for Los Angeles Lashings v Seattle Thunderbolts:\n" +
		"Sat Sep 26, 5:00 PM ET, not started:\n• Los Angeles Lashings wins: 56% (market)\n• Seattle Thunderbolts wins: 48% (market)\n" +
		"Sun Sep 27, 4:00 PM ET, not started: " + unpriced
	// Exact names in either order, and the backstop: "Lashings" v
	// "Thunderbolts" is found by a word in the scan, and the market it
	// lands on belongs to a listed game.
	for _, pair := range [][2]string{
		{"Los Angeles Lashings", "Seattle Thunderbolts"},
		{"Seattle Thunderbolts", "Los Angeles Lashings"},
		{"Lashings", "Thunderbolts"},
	} {
		out, isErr := callTool(t, "cricket_market_odds", map[string]any{"team_a": pair[0], "team_b": pair[1]})
		if isErr || !strings.HasPrefix(out, la) {
			t.Errorf("%s v %s:\n%s\nwant it to start:\n%s", pair[0], pair[1], out, la)
			continue
		}
		if strings.Count(out, "%") != 2 || strings.Contains(out, "implied") {
			t.Errorf("%s v %s: Sunday's placeholder or a doubled line leaked in:\n%s", pair[0], pair[1], out)
		}
		labelledOnly(t, out)
	}
}

// In play, the book is the event's own, read through the 20-second cache.
func TestMarketOddsMinorLeagueInPlay(t *testing.T) {
	useMilcCapture(t, capture2004, capture2004.at)
	useKalshiCapture(t)
	out, isErr := callTool(t, "cricket_market_odds", map[string]any{"team_a": "Atlanta Lightning", "team_b": "Atlanta Fire"})
	want := "Kalshi's Minor League Cricket markets for Atlanta Fire v Atlanta Lightning:\n" +
		"Sat Sep 26, 2:00 PM ET, in progress:\n• Atlanta Fire wins: 76% (market)\n• Atlanta Lightning wins: 25% (market)\n"
	if isErr || !strings.HasPrefix(out, want) {
		t.Errorf("got:\n%s\nwant it to start:\n%s", out, want)
	}
	labelledOnly(t, out)
}

// Every other match prints one event, each market once: the scan lists
// each KXT20MATCH market twice, and two fixtures on consecutive days share
// a title.
func TestMarketOddsBindsOneEventOnce(t *testing.T) {
	event := func(ticker string, india, australia int) []kalshi.Candidate {
		mk := func(side, title string, bid int) kalshi.Candidate {
			return kalshi.Candidate{EventTitle: "India vs Australia", EventTicker: ticker, Market: kalshi.Market{
				Ticker: ticker + "-" + side, Title: title, Status: "active",
				YesBid: bid, YesAsk: bid + 2, ImpliedProb: float64(bid+1) / 100,
			}}
		}
		return []kalshi.Candidate{mk("IND", "India wins", india), mk("AUS", "Australia wins", australia)}
	}
	first := event("KXT20MATCH-26OCT011000AUSIND", 60, 38)
	second := event("KXT20MATCH-26OCT031000AUSIND", 30, 68)
	var scan []kalshi.Candidate
	for i := 0; i < 2; i++ {
		scan = append(scan, first...)
		scan = append(scan, second...)
	}
	kalshi.SeedScan(scan)
	t.Cleanup(func() { kalshi.SeedScan(nil) })

	out, isErr := callTool(t, "cricket_market_odds", map[string]any{"team_a": "India", "team_b": "Australia"})
	if isErr {
		t.Fatalf("error: %s", out)
	}
	if strings.Count(out, "India wins") != 1 || strings.Count(out, "Australia wins") != 1 {
		t.Errorf("each market once:\n%s", out)
	}
	if !strings.Contains(out, "India wins — 61% implied") || !strings.Contains(out, "Australia wins — 39% implied") ||
		strings.Contains(out, "31%") || strings.Contains(out, "69%") {
		t.Errorf("the second fixture's book was mixed into the first's:\n%s", out)
	}
}

// ----------------------------------------------------- win probability

// cricket_win_probability prices a Minor League state only where the site
// does: in a chase with a target, of 15 overs or more. East Bay Blazers
// 88/5 after 13.1 overs, copied out of cricket_minor_league, used to get a
// first-innings number from the global T20 par.
func TestWinProbRefusesWhatTheLeagueIsNeverShown(t *testing.T) {
	useMilcCapture(t, capture1936, capture1936.at)
	refused := map[string]struct {
		args map[string]any
		why  string
	}{
		"first innings": {map[string]any{"runs": 88, "wickets": 5, "overs": 13.1, "total_overs": 20, "innings": 1,
			"batting_team": "East Bay Blazers", "bowling_team": "San Ramon Grizzlies"}, "once the chase starts"},
		"bowling side named": {map[string]any{"runs": 88, "wickets": 5, "overs": 13.1, "total_overs": 20, "innings": 1,
			"bowling_team": "San Ramon Grizzlies"}, "once the chase starts"},
		"no target": {map[string]any{"runs": 28, "wickets": 4, "overs": 4.5, "total_overs": 20, "innings": 2,
			"batting_team": "Atlanta Lightning", "bowling_team": "Atlanta Fire"}, "target"},
		"7-over chase": {map[string]any{"runs": 26, "wickets": 3, "overs": 2.3, "total_overs": 7, "innings": 2, "target": 63,
			"batting_team": "New England Eagles", "bowling_team": "New Jersey Somerset Cavaliers"}, "15 overs"},
	}
	for name, c := range refused {
		out, isErr := callTool(t, "cricket_win_probability", c.args)
		if !isErr || strings.Contains(out, "%") || !strings.Contains(out, c.why) || !strings.Contains(out, "cricket_minor_league") {
			t.Errorf("%s: %q (error %v)", name, out, isErr)
		}
	}

	priced := map[string]map[string]any{
		"a 20-over chase": {"runs": 28, "wickets": 4, "overs": 4.5, "total_overs": 20, "innings": 2, "target": 206,
			"batting_team": "Atlanta Lightning", "bowling_team": "Atlanta Fire"},
		"another league's first innings": {"runs": 40, "wickets": 1, "overs": 5.0, "total_overs": 20, "innings": 1,
			"batting_team": "India", "bowling_team": "Namibia"},
	}
	for name, args := range priced {
		if out, isErr := callTool(t, "cricket_win_probability", args); isErr || !strings.Contains(out, "win probability") {
			t.Errorf("%s: %q (error %v)", name, out, isErr)
		}
	}
}
