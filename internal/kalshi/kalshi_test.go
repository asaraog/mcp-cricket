package kalshi

import (
	"math"
	"strings"
	"testing"
	"time"
)

const sample = `{"market":{"ticker":"KXTEST-26X","title":"Will the Unicorns win?","status":"active","yes_bid":60,"yes_ask":64,"last_price":61}}`

func TestParseMarket(t *testing.T) {
	m, err := ParseMarket([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if m.Ticker != "KXTEST-26X" || m.Title != "Will the Unicorns win?" {
		t.Errorf("parsed %+v", m)
	}
	// mid of 60/64 = 62¢ -> 0.62
	if math.Abs(m.ImpliedProb-0.62) > 1e-9 {
		t.Errorf("implied prob = %v, want 0.62", m.ImpliedProb)
	}
}

func TestImpliedProbFallsBackToLast(t *testing.T) {
	m, err := ParseMarket([]byte(`{"market":{"ticker":"T","title":"x","yes_bid":0,"yes_ask":0,"last_price":45}}`))
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(m.ImpliedProb-0.45) > 1e-9 {
		t.Errorf("implied prob = %v, want 0.45", m.ImpliedProb)
	}
}

func TestParseMarketErrors(t *testing.T) {
	if _, err := ParseMarket([]byte(`{}`)); err == nil {
		t.Error("empty response should error")
	}
	if _, err := ParseMarket([]byte(`not json`)); err == nil {
		t.Error("bad json should error")
	}
}

const eventsSample = `{"cursor":"","events":[
  {"title":"MLC Final: Washington Freedom vs LA Knight Riders","markets":[
    {"ticker":"KXMLC-FREEDOM","title":"Will the Freedom win the final?","status":"active","yes_bid":60,"yes_ask":64,"last_price":61},
    {"ticker":"KXMLC-CLOSED","title":"Freedom season wins","status":"settled","yes_bid":0,"yes_ask":0,"last_price":99}]},
  {"title":"Fed rate decision","markets":[
    {"ticker":"KXFED-1","title":"Rate cut in September?","status":"active","yes_bid":30,"yes_ask":34,"last_price":31}]}]}`

func TestParseEventsPage(t *testing.T) {
	cands, cursor, err := ParseEventsPage([]byte(eventsSample))
	if err != nil {
		t.Fatal(err)
	}
	if cursor != "" {
		t.Errorf("cursor = %q", cursor)
	}
	// settled market filtered out
	if len(cands) != 2 {
		t.Fatalf("candidates = %d, want 2 (settled filtered): %+v", len(cands), cands)
	}
}

func TestBestMatch(t *testing.T) {
	cands, _, _ := ParseEventsPage([]byte(eventsSample))

	m, team, ok := BestMatch(cands, "Washington Freedom", "LA Knight Riders")
	if !ok || m.Ticker != "KXMLC-FREEDOM" || team != "Washington Freedom" {
		t.Errorf("match: %+v team=%q ok=%v", m, team, ok)
	}

	// One team alone must NOT match: a single shared word once matched
	// tomorrow's fixture (and a Supreme Court market) to a live game.
	_, _, ok = BestMatch(cands, "Some Freedom", "Nobody FC")
	if ok {
		t.Error("single-team overlap must not match")
	}

	_, _, ok = BestMatch(cands, "Mumbai Indians", "Chennai Super Kings")
	if ok {
		t.Error("unrelated teams should not match")
	}

	// Generic words ("Women") never identify a team.
	if toks := teamTokens("Sri Lanka Women"); len(toks) != 2 || toks[1] != "lanka" {
		t.Errorf("distinctive token should skip generic words: %v", toks)
	}
}

func TestParseTradesJSON(t *testing.T) {
	body := []byte(`{"trades":[{"yes_price_dollars":"0.4800","no_price_dollars":"0.5200"},{"yes_price_dollars":"0.4700"}]}`)
	cents, ok := ParseTradesJSON(body)
	if !ok || cents != 48 {
		t.Errorf("cents=%d ok=%v, want 48 true", cents, ok)
	}
	if _, ok := ParseTradesJSON([]byte(`{"trades":[]}`)); ok {
		t.Error("no trades should not produce a price")
	}
	if _, ok := ParseTradesJSON([]byte("junk")); ok {
		t.Error("bad json should not produce a price")
	}
}

func TestNormalizeTicker(t *testing.T) {
	cases := map[string]string{
		"KXTEST-26X":   "KXTEST-26X",
		" kxtest-26x ": "KXTEST-26X",
		"https://kalshi.com/markets/kxtestmatch/mens-test-cricket-match/kxtestmatch-26jul251100pakwi": "KXTESTMATCH-26JUL251100PAKWI",
		"https://kalshi.com/markets/x/y/kxfoo-1?ref=abc":                                              "KXFOO-1",
	}
	for in, want := range cases {
		if got := NormalizeTicker(in); got != want {
			t.Errorf("NormalizeTicker(%q) = %q, want %q", in, got, want)
		}
	}
}

const eventSample = `{"event":{"title":"West Indies vs Pakistan","markets":[
  {"ticker":"KXTESTMATCH-26JUL251100PAKWI-WI","yes_sub_title":"West Indies","status":"active","yes_bid":null,"yes_ask":null,"last_price":null},
  {"ticker":"KXTESTMATCH-26JUL251100PAKWI-PAK","yes_sub_title":"Pakistan","status":"active","yes_bid":null,"yes_ask":null,"last_price":null},
  {"ticker":"KXTESTMATCH-26JUL251100PAKWI-TIE","yes_sub_title":"Draw/Tie","status":"active","yes_bid":null,"yes_ask":null,"last_price":null}]}}`

func TestParseEventJSONAndUnquotedCompare(t *testing.T) {
	cands, err := ParseEventJSON([]byte(eventSample))
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 3 {
		t.Fatalf("candidates = %d, want 3", len(cands))
	}
	m, team, ok := BestMatch(cands, "West Indies", "Pakistan")
	if !ok || team != "West Indies" {
		t.Errorf("match: %+v %q %v", m, team, ok)
	}
	if m.HasQuotes() {
		t.Error("null-priced market should have no quotes")
	}
	c := Compare(m, team, 0.8)
	if !strings.Contains(c.Read, "NO quotes") {
		t.Errorf("unquoted market must say so, got: %s", c.Read)
	}
}

func TestEventTickerOf(t *testing.T) {
	cases := map[string]string{
		"KXTESTMATCH-26JUL251100PAKWI-WI": "KXTESTMATCH-26JUL251100PAKWI",
		"kxtestmatch-26jul251100pakwi":    "KXTESTMATCH-26JUL251100PAKWI", // already an event: loses only the last segment if any
	}
	// Market ticker strips outcome suffix
	if got := EventTickerOf("KXTESTMATCH-26JUL251100PAKWI-WI"); got != cases["KXTESTMATCH-26JUL251100PAKWI-WI"] {
		t.Errorf("EventTickerOf market = %q", got)
	}
}

func TestMarketSetFromCandidates(t *testing.T) {
	cands, err := ParseEventJSON([]byte(eventSample))
	if err != nil {
		t.Fatal(err)
	}
	ms := MarketSetFromCandidates(cands, "West Indies vs Pakistan")
	if ms.EventTitle != "West Indies vs Pakistan" || len(ms.Markets) != 3 {
		t.Fatalf("set: %+v", ms)
	}
	titles := map[string]bool{}
	for _, m := range ms.Markets {
		titles[m.Title] = true
	}
	for _, want := range []string{"West Indies", "Pakistan", "Draw/Tie"} {
		if !titles[want] {
			t.Errorf("set missing sibling market %q", want)
		}
	}
	if got := MarketSetFromCandidates(cands, "Other Event"); len(got.Markets) != 0 {
		t.Errorf("wrong-title grouping: %+v", got)
	}
}

func TestCompare(t *testing.T) {
	m, _ := ParseMarket([]byte(sample)) // 62¢
	c := Compare(m, "Unicorns", 0.78)
	if math.Abs(c.Edge-0.16) > 1e-9 {
		t.Errorf("edge = %v, want 0.16", c.Edge)
	}
	if c.Read == "" || c.ModelTeam != "Unicorns" {
		t.Errorf("comparison: %+v", c)
	}

	noModel := Compare(m, "", -1)
	if noModel.Edge != 0 || noModel.Read == "" {
		t.Errorf("no-model comparison: %+v", noModel)
	}
}

func TestPulseFromTrades(t *testing.T) {
	now := time.Date(2026, 7, 27, 20, 0, 0, 0, time.UTC)
	at := func(secAgo int) time.Time { return now.Add(-time.Duration(secAgo) * time.Second) }
	window, threshold := 2*time.Minute, 4

	// Wicket falls: 42 -> 51 inside the window, baseline 3 min old.
	moved := []Trade{{51, at(5)}, {48, at(30)}, {44, at(70)}, {42, at(180)}}
	if from, to, _, ok := PulseFromTrades(moved, now, window, threshold); !ok || from != 42 || to != 51 {
		t.Errorf("want pulse 42->51, got %d->%d ok=%v", from, to, ok)
	}
	// Calm drift of 2 cents: no pulse.
	calm := []Trade{{43, at(10)}, {42, at(60)}, {41, at(200)}}
	if _, _, _, ok := PulseFromTrades(calm, now, window, threshold); ok {
		t.Error("2-cent drift should not pulse")
	}
	// Big old move but latest trade outside the window: quiet market, no pulse.
	stale := []Trade{{60, at(300)}, {40, at(600)}}
	if _, _, _, ok := PulseFromTrades(stale, now, window, threshold); ok {
		t.Error("stale trades should not pulse")
	}
}

func TestParseTradeHistoryJSON(t *testing.T) {
	body := []byte(`{"trades":[
		{"yes_price_dollars":"0.4200","created_time":"2026-07-28T00:45:45.548904Z"},
		{"yes_price_dollars":"bogus","created_time":"2026-07-28T00:45:40Z"},
		{"yes_price_dollars":"0.3800","created_time":"2026-07-28T00:44:00Z"}]}`)
	trades := ParseTradeHistoryJSON(body)
	if len(trades) != 2 || trades[0].Cents != 42 || trades[1].Cents != 38 {
		t.Fatalf("got %+v", trades)
	}
	if !trades[0].At.After(trades[1].At) {
		t.Error("order should follow the feed (newest first)")
	}
}
