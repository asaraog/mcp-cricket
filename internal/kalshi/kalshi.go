// Package kalshi reads public market data from Kalshi's trade API (keyless
// for market data). Kalshi contract prices are literal probabilities
// (62¢ = 62%), so they compare against our win-probability heuristic with
// no odds conversion.
//
// Kalshi is a CFTC-regulated event-contract exchange; this module only
// READS public prices for context. Informational, never advice.
package kalshi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const apiBase = "https://api.elections.kalshi.com/trade-api/v2"

var client = &http.Client{Timeout: 10 * time.Second, Transport: tunedTransport()}

// Market is one Kalshi market with prices in cents (= implied percent).
type Market struct {
	Ticker      string  `json:"ticker"`
	Title       string  `json:"title"`
	Status      string  `json:"status"`
	YesBid      int     `json:"yes_bid"`
	YesAsk      int     `json:"yes_ask"`
	LastPrice   int     `json:"last_price"`
	ImpliedProb float64 `json:"implied_prob"`           // derived, 0..1
	PriceSource string  `json:"price_source,omitempty"` // "" (summary) | "last_trade"
}

type marketResp struct {
	Market struct {
		Ticker    string `json:"ticker"`
		Title     string `json:"title"`
		Status    string `json:"status"`
		YesBid    int    `json:"yes_bid"`
		YesAsk    int    `json:"yes_ask"`
		LastPrice int    `json:"last_price"`
	} `json:"market"`
}

// ParseMarket decodes a /markets/{ticker} response body.
func ParseMarket(body []byte) (Market, error) {
	var r marketResp
	if err := json.Unmarshal(body, &r); err != nil {
		return Market{}, fmt.Errorf("kalshi: bad response: %w", err)
	}
	if r.Market.Ticker == "" {
		return Market{}, fmt.Errorf("kalshi: no market in response")
	}
	m := Market{
		Ticker: r.Market.Ticker, Title: r.Market.Title, Status: r.Market.Status,
		YesBid: r.Market.YesBid, YesAsk: r.Market.YesAsk, LastPrice: r.Market.LastPrice,
	}
	m.ImpliedProb = impliedProb(m.YesBid, m.YesAsk, m.LastPrice)
	return m, nil
}

// impliedProb prefers the bid/ask mid (live view of the market); falls back
// to last trade.
func impliedProb(bid, ask, last int) float64 {
	if bid > 0 && ask > 0 && ask >= bid {
		return float64(bid+ask) / 2.0 / 100.0
	}
	return float64(last) / 100.0
}

// HasQuotes reports whether the market has any price signal at all —
// freshly listed markets (e.g. cricket) can be active but never traded.
func (m Market) HasQuotes() bool {
	return m.YesBid > 0 || m.YesAsk > 0 || m.LastPrice > 0
}

// ParseTradesJSON extracts the most recent yes price (in cents) from a
// /markets/trades response. Prices arrive as dollar strings ("0.4800").
func ParseTradesJSON(body []byte) (int, bool) {
	var d struct {
		Trades []struct {
			YesPriceDollars string `json:"yes_price_dollars"`
		} `json:"trades"`
	}
	if err := json.Unmarshal(body, &d); err != nil || len(d.Trades) == 0 {
		return 0, false
	}
	var dollars float64
	if _, err := fmt.Sscanf(d.Trades[0].YesPriceDollars, "%f", &dollars); err != nil || dollars <= 0 {
		return 0, false
	}
	return int(dollars*100 + 0.5), true
}

type cachedPrice struct {
	cents int
	exp   time.Time
}

var (
	priceMu    sync.Mutex
	priceCache = map[string]cachedPrice{}
)

// WithQuotes back-fills a quoteless market from its public trades feed
// (cached 10s per ticker). Kalshi nulls the summary price fields on its
// sports markets, but the trades endpoint is public and carries the real
// prices (verified live: the site's displayed price = the latest trade).
func WithQuotes(m Market) Market {
	if m.Ticker == "" {
		return m
	}
	// Order-book quotes straight from the API payload are already live.
	// Trades-derived prices (all cricket markets) go stale the moment the
	// 2-minute scan that stamped them ages — refresh those through the
	// 10s cache instead of trusting the stamp forever.
	if m.HasQuotes() && m.PriceSource != "last_trade" {
		return m
	}
	priceMu.Lock()
	if p, ok := priceCache[m.Ticker]; ok && time.Now().Before(p.exp) {
		priceMu.Unlock()
		m.LastPrice = p.cents
		m.ImpliedProb = float64(p.cents) / 100.0
		m.PriceSource = "last_trade"
		return m
	}
	priceMu.Unlock()

	resp, err := client.Get(apiBase + "/markets/trades?ticker=" + url.QueryEscape(m.Ticker) + "&limit=1")
	if err != nil {
		return m
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return m
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return m
	}
	if cents, ok := ParseTradesJSON(body); ok {
		priceMu.Lock()
		priceCache[m.Ticker] = cachedPrice{cents: cents, exp: time.Now().Add(10 * time.Second)}
		priceMu.Unlock()
		m.LastPrice = cents
		m.ImpliedProb = float64(cents) / 100.0
		m.PriceSource = "last_trade"
	}
	return m
}

// ------------------------------------------------------------------ pulse

// Trade is one public trade: price in cents plus when it happened.
type Trade struct {
	Cents int
	At    time.Time
}

// ParseTradeHistoryJSON parses the trades feed (newest first).
func ParseTradeHistoryJSON(body []byte) []Trade {
	var d struct {
		Trades []struct {
			YesPriceDollars string    `json:"yes_price_dollars"`
			CreatedTime     time.Time `json:"created_time"`
		} `json:"trades"`
	}
	if err := json.Unmarshal(body, &d); err != nil {
		return nil
	}
	out := make([]Trade, 0, len(d.Trades))
	for _, t := range d.Trades {
		var dollars float64
		if _, err := fmt.Sscanf(t.YesPriceDollars, "%f", &dollars); err != nil || dollars <= 0 {
			continue
		}
		out = append(out, Trade{Cents: int(dollars*100 + 0.5), At: t.CreatedTime})
	}
	return out
}

type cachedTrades struct {
	trades []Trade
	exp    time.Time
}

var (
	tradesMu    sync.Mutex
	tradesCache = map[string]cachedTrades{}
)

func tradeHistory(ticker string) []Trade {
	tradesMu.Lock()
	if c, ok := tradesCache[ticker]; ok && time.Now().Before(c.exp) {
		tradesMu.Unlock()
		return c.trades
	}
	tradesMu.Unlock()
	resp, err := client.Get(apiBase + "/markets/trades?ticker=" + url.QueryEscape(ticker) + "&limit=50")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}
	trades := ParseTradeHistoryJSON(body)
	tradesMu.Lock()
	tradesCache[ticker] = cachedTrades{trades: trades, exp: time.Now().Add(3 * time.Second)}
	tradesMu.Unlock()
	return trades
}

// Pulse is a sharp recent price move on one market — traders repricing
// within seconds of on-field events, usually 20-40s before scoreboard
// APIs update. The market can't say WHAT happened, only that it has.
type Pulse struct {
	Ticker string    `json:"ticker"`
	Title  string    `json:"title"`
	From   int       `json:"from_cents"`
	To     int       `json:"to_cents"`
	At     time.Time `json:"at"`
}

// PulseFromTrades detects a move of >= threshold cents inside the window:
// latest price vs the last price seen before the window began. The latest
// trade itself must be inside the window (a quiet market never pulses).
func PulseFromTrades(trades []Trade, now time.Time, window time.Duration, threshold int) (from, to int, at time.Time, ok bool) {
	if len(trades) < 2 {
		return 0, 0, time.Time{}, false
	}
	latest := trades[0]
	cutoff := now.Add(-window)
	if !latest.At.After(cutoff) {
		return 0, 0, time.Time{}, false
	}
	baseline := trades[len(trades)-1]
	for _, t := range trades[1:] {
		if t.At.Before(cutoff) {
			baseline = t
			break
		}
	}
	delta := latest.Cents - baseline.Cents
	if delta < 0 {
		delta = -delta
	}
	if delta < threshold {
		return 0, 0, time.Time{}, false
	}
	return baseline.Cents, latest.Cents, latest.At, true
}

// MarketPulse checks one market's public trades for a sharp recent move.
func MarketPulse(m Market, window time.Duration, threshold int) (Pulse, bool) {
	if m.Ticker == "" {
		return Pulse{}, false
	}
	from, to, at, ok := PulseFromTrades(tradeHistory(m.Ticker), time.Now(), window, threshold)
	if !ok {
		return Pulse{}, false
	}
	return Pulse{Ticker: m.Ticker, Title: m.Title, From: from, To: to, At: at}, true
}

// MarketSet is ALL sibling outcome markets of one event (WI / PAK / Draw),
// so team-specific price questions are answerable regardless of which
// market matched first. Yes-price cents = implied probability percent.
type MarketSet struct {
	EventTitle string   `json:"event_title"`
	Markets    []Market `json:"markets"`
}

// MarketSetFromCandidates groups candidates sharing an event title.
func MarketSetFromCandidates(cands []Candidate, eventTitle string) MarketSet {
	ms := MarketSet{EventTitle: eventTitle}
	for _, c := range cands {
		if c.EventTitle == eventTitle {
			ms.Markets = append(ms.Markets, c.Market)
		}
	}
	return ms
}

// EnrichAll fills quotes for every market in the set — in parallel, so a
// 3-market event costs one round trip, not three.
func (ms MarketSet) EnrichAll() MarketSet {
	var wg sync.WaitGroup
	for i := range ms.Markets {
		if ms.Markets[i].HasQuotes() && ms.Markets[i].PriceSource != "last_trade" {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ms.Markets[i] = WithQuotes(ms.Markets[i])
		}(i)
	}
	wg.Wait()
	return ms
}

// EventTickerOf strips a market ticker's outcome suffix
// ("...PAKWI-WI" -> "...PAKWI"); event tickers pass through unchanged.
func EventTickerOf(marketTicker string) string {
	t := NormalizeTicker(marketTicker)
	if i := strings.LastIndex(t, "-"); i > 0 {
		return t[:i]
	}
	return t
}

// FindEventForTeams locates the event whose markets name either team and
// returns the full sibling set (quotes filled) plus the matched market.
func FindEventForTeams(teamA, teamB string) (MarketSet, Market, string, bool) {
	cands := openMarketScan()
	m, team, ok := BestMatch(cands, teamA, teamB)
	if !ok {
		return MarketSet{}, Market{}, "", false
	}
	title := ""
	for _, c := range cands {
		if c.Market.Ticker == m.Ticker {
			title = c.EventTitle
			break
		}
	}
	ms := MarketSetFromCandidates(cands, title).EnrichAll()
	return ms, WithQuotes(m), team, true
}

// NormalizeTicker accepts a bare ticker OR a pasted kalshi.com URL
// (".../kxtestmatch-26jul251100pakwi") and returns the uppercase ticker.
func NormalizeTicker(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "?"); i >= 0 {
		s = s[:i]
	}
	if strings.Contains(s, "/") {
		parts := strings.Split(strings.TrimRight(s, "/"), "/")
		s = parts[len(parts)-1]
	}
	return strings.ToUpper(s)
}

// GetMarket fetches one market by ticker, e.g. "KXMLBGAME-26JUL26..."
func GetMarket(ticker string) (Market, error) {
	ticker = NormalizeTicker(ticker)
	if ticker == "" {
		return Market{}, fmt.Errorf("kalshi: empty ticker")
	}
	req, err := http.NewRequest(http.MethodGet, apiBase+"/markets/"+ticker, nil)
	if err != nil {
		return Market{}, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return Market{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Market{}, fmt.Errorf("kalshi: no market %q", ticker)
	}
	if resp.StatusCode != http.StatusOK {
		return Market{}, fmt.Errorf("kalshi: HTTP %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Market{}, err
	}
	m, err := ParseMarket(body)
	if err != nil {
		return Market{}, err
	}
	return WithQuotes(m), nil
}

// ----------------------------------------------------- auto ticker lookup

// Candidate is one open market seen during an events scan.
type Candidate struct {
	EventTitle string
	Market     Market
}

type eventsPage struct {
	Cursor string `json:"cursor"`
	Events []struct {
		Title   string `json:"title"`
		Markets []struct {
			Ticker      string `json:"ticker"`
			Title       string `json:"title"`
			YesSubTitle string `json:"yes_sub_title"`
			Status      string `json:"status"`
			YesBid      int    `json:"yes_bid"`
			YesAsk      int    `json:"yes_ask"`
			LastPrice   int    `json:"last_price"`
		} `json:"markets"`
	} `json:"events"`
}

// ParseEventsPage decodes one /events?with_nested_markets=true page.
func ParseEventsPage(body []byte) ([]Candidate, string, error) {
	var page eventsPage
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, "", fmt.Errorf("kalshi events: %w", err)
	}
	var out []Candidate
	for _, ev := range page.Events {
		for _, m := range ev.Markets {
			if m.Status != "" && m.Status != "active" && m.Status != "open" {
				continue
			}
			out = append(out, Candidate{
				EventTitle: ev.Title,
				Market: Market{
					Ticker: m.Ticker, Title: m.Title, Status: m.Status,
					YesBid: m.YesBid, YesAsk: m.YesAsk, LastPrice: m.LastPrice,
					ImpliedProb: impliedProb(m.YesBid, m.YesAsk, m.LastPrice),
				},
			})
		}
	}
	return out, page.Cursor, nil
}

// ParseEventJSON decodes a /events/{ticker}?with_nested_markets=true body
// (single-event shape) into candidates.
func ParseEventJSON(body []byte) ([]Candidate, error) {
	var d struct {
		Event struct {
			Title   string `json:"title"`
			Markets []struct {
				Ticker      string `json:"ticker"`
				Title       string `json:"title"`
				YesSubTitle string `json:"yes_sub_title"`
				Status      string `json:"status"`
				YesBid      int    `json:"yes_bid"`
				YesAsk      int    `json:"yes_ask"`
				LastPrice   int    `json:"last_price"`
			} `json:"markets"`
		} `json:"event"`
	}
	if err := json.Unmarshal(body, &d); err != nil {
		return nil, fmt.Errorf("kalshi event: %w", err)
	}
	var out []Candidate
	for _, m := range d.Event.Markets {
		title := m.Title
		if m.YesSubTitle != "" {
			title = m.YesSubTitle
		}
		out = append(out, Candidate{
			EventTitle: d.Event.Title,
			Market: Market{
				Ticker: m.Ticker, Title: title, Status: m.Status,
				YesBid: m.YesBid, YesAsk: m.YesAsk, LastPrice: m.LastPrice,
				ImpliedProb: impliedProb(m.YesBid, m.YesAsk, m.LastPrice),
			},
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("kalshi: event has no markets")
	}
	return out, nil
}

// GetEventMarkets fetches an EVENT ticker's markets (what a kalshi.com match
// URL points at — the per-team winner markets live underneath it).
func GetEventMarkets(eventTicker string) ([]Candidate, error) {
	eventTicker = NormalizeTicker(eventTicker)
	resp, err := client.Get(apiBase + "/events/" + eventTicker + "?with_nested_markets=true")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kalshi: HTTP %s for event %q", resp.Status, eventTicker)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return ParseEventJSON(body)
}

// teamTokens builds match tokens for a team name: the full name plus its
// last word ("San Francisco Unicorns" also matches just "Unicorns").
// genericWords never identify a team on their own: "Women" as a token
// matched a women's ODI to a Supreme Court market about women's sports.
var genericWords = map[string]bool{
	"women": true, "men": true, "team": true, "club": true, "cricket": true,
	// Compass/common words shared across unrelated teams ("South Africa"
	// must never match "South Delhi Superstarz").
	"south": true, "north": true, "east": true, "west": true, "united": true,
	"royal": true, "city": true, "state": true, "sport": true, "players": true,
}

func teamTokens(name string) []string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return nil
	}
	tokens := []string{name}
	// Every distinctive word, not just one: ESPN and Kalshi disagree on
	// nicknames ("Kandy Royals" vs "Kandy Falcons") but share the city.
	for _, w := range strings.Fields(name) {
		if len(w) >= 4 && !genericWords[w] && w != name {
			tokens = append(tokens, w)
		}
	}
	return tokens
}

func anyToken(text, team string) bool {
	return matchedToken(text, team) != ""
}

func matchedToken(text, team string) string {
	for _, tok := range teamTokens(team) {
		if strings.Contains(text, tok) {
			return tok
		}
	}
	return ""
}

// BestMatch finds the candidate whose event or market title names BOTH
// teams — one shared word has matched tomorrow's fixture for the same
// team, and no markets beats wrong markets. Returns (market,
// matchedTeamName, found), preferring live bid/ask liquidity.
func BestMatch(cands []Candidate, teamA, teamB string) (Market, string, bool) {
	var best *Candidate
	bestTeam := ""
	for i := range cands {
		text := strings.ToLower(cands[i].EventTitle + " " + cands[i].Market.Title)
		tokA, tokB := matchedToken(text, teamA), matchedToken(text, teamB)
		if tokA == "" || tokB == "" || tokA == tokB {
			continue // both teams matching on one shared word is no match
		}
		matched := teamA
		if mt := strings.ToLower(cands[i].Market.Title); !anyToken(mt, teamA) && anyToken(mt, teamB) {
			matched = teamB
		}
		hasLiquidity := cands[i].Market.YesBid > 0 && cands[i].Market.YesAsk > 0
		if best == nil || (hasLiquidity && !(best.Market.YesBid > 0 && best.Market.YesAsk > 0)) {
			best = &cands[i]
			bestTeam = matched
		}
	}
	if best == nil {
		return Market{}, "", false
	}
	return best.Market, bestTeam, true
}

var (
	scanMu       sync.Mutex
	scanCands    []Candidate
	scanExp      time.Time
	scanInFlight bool
)

// openMarketScan returns the cached scan IMMEDIATELY and refreshes it in a
// background goroutine when stale — a chat turn must never block for the
// multi-page Kalshi crawl (up to 10 sequential requests). First-ever call
// returns empty; the next one sees results.
func openMarketScan() []Candidate {
	scanMu.Lock()
	defer scanMu.Unlock()
	if !time.Now().Before(scanExp) && !scanInFlight {
		scanInFlight = true
		go refreshScan()
	}
	return scanCands
}

// Known Kalshi cricket series (verified live 2026-07): Tests, T20s, ODIs,
// and Major League Cricket. Extend via KALSHI_SERIES=comma,separated.
var cricketSeries = []string{"KXTESTMATCH", "KXT20MATCH", "KXODIMATCH", "KXMLC",
	// League-specific match series (empty out of season, cheap to poll):
	"KXLPLMATCH", "KXHUNDREDMATCH", "KXIPLMATCH", "KXBBLMATCH", "KXPSLMATCH", "KXCPLMATCH"}

func refreshScan() {
	// Targeted first: cricket series events land at the FRONT of the
	// candidate list, so team matching hits them before the generic pool.
	var all []Candidate
	series := cricketSeries
	if extra := os.Getenv("KALSHI_SERIES"); extra != "" {
		for _, s := range strings.Split(extra, ",") {
			if s = strings.TrimSpace(s); s != "" {
				series = append(series, s)
			}
		}
	}
	for _, s := range series {
		u := apiBase + "/events?status=open&with_nested_markets=true&limit=200&series_ticker=" + url.QueryEscape(s)
		resp, err := client.Get(u)
		if err != nil {
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			continue
		}
		if cands, _, err := ParseEventsPage(body); err == nil {
			all = append(all, cands...)
		}
	}
	all = append(all, fetchAllEventPages()...)

	// Pre-fill prices for cricket-series markets in the background so
	// request-time enrichment is a cache hit instead of live HTTP.
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i := range all {
		ticker := all[i].Market.Ticker
		for _, s := range series {
			if strings.HasPrefix(ticker, s+"-") && !all[i].Market.HasQuotes() {
				wg.Add(1)
				sem <- struct{}{}
				go func(i int) {
					defer wg.Done()
					all[i].Market = WithQuotes(all[i].Market)
					<-sem
				}(i)
				break
			}
		}
	}
	wg.Wait()

	scanMu.Lock()
	scanCands, scanExp, scanInFlight = all, time.Now().Add(2*time.Minute), false
	scanMu.Unlock()
}

func fetchAllEventPages() []Candidate {
	var all []Candidate
	cursor := ""
	for page := 0; page < 10; page++ {
		u := apiBase + "/events?status=open&with_nested_markets=true&limit=200"
		if cursor != "" {
			u += "&cursor=" + url.QueryEscape(cursor)
		}
		resp, err := client.Get(u)
		if err != nil {
			break
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			break
		}
		cands, next, err := ParseEventsPage(body)
		if err != nil {
			break
		}
		all = append(all, cands...)
		if next == "" || next == cursor {
			break
		}
		cursor = next
	}
	return all
}

// FindMarketForTeams auto-detects a Kalshi market naming either team.
func FindMarketForTeams(teamA, teamB string) (Market, string, bool) {
	return BestMatch(openMarketScan(), teamA, teamB)
}

// Prewarm kicks off the first market scan in the background so auto-detect
// has data by the time the first chat arrives. Call once at server boot.
func Prewarm() {
	go openMarketScan()
}

// Comparison lines a Kalshi market price up against a model probability.
type Comparison struct {
	Market     Market  `json:"market"`
	MarketProb float64 `json:"market_prob"`
	ModelProb  float64 `json:"model_prob,omitempty"`
	ModelTeam  string  `json:"model_team,omitempty"`
	Edge       float64 `json:"edge,omitempty"`
	Read       string  `json:"read"`
}

// Compare builds the comparison. modelProb may be <0 to mean "no model view"
// (e.g. no match selected or the market isn't about this match).
func Compare(m Market, modelTeam string, modelProb float64) Comparison {
	c := Comparison{Market: m, MarketProb: m.ImpliedProb}
	if !m.HasQuotes() {
		c.Read = fmt.Sprintf(
			"Kalshi lists %q (%s) but it has NO quotes or trades yet — there is no "+
				"market price to report or compare. Say exactly that if asked.",
			m.Title, m.Ticker)
		return c
	}
	if modelProb < 0 {
		c.Read = fmt.Sprintf("Kalshi prices %q at %.0f¢ (= %.0f%% implied). No model comparison available for this market.",
			m.Title, m.ImpliedProb*100, m.ImpliedProb*100)
		return c
	}
	c.ModelProb = modelProb
	c.ModelTeam = modelTeam
	c.Edge = modelProb - m.ImpliedProb
	switch {
	case c.Edge > 0.05:
		c.Read = "our heuristic likes this outcome more than the Kalshi market does"
	case c.Edge < -0.05:
		c.Read = "the Kalshi market likes this outcome more than our heuristic does"
	default:
		c.Read = "Kalshi market and our heuristic roughly agree"
	}
	c.Read = fmt.Sprintf("Kalshi: %.0f¢ (%.0f%%) vs model %.0f%% for %s — %s.",
		m.ImpliedProb*100, m.ImpliedProb*100, modelProb*100, modelTeam, c.Read)
	return c
}

// tunedTransport keeps enough idle connections for burst traffic. Go's
// default (MaxIdleConnsPerHost: 2) forces a fresh TLS handshake on nearly
// every request once concurrency rises, which shows up as latency spikes
// exactly when the site is busiest.
func tunedTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = 200
	t.MaxIdleConnsPerHost = 100
	t.MaxConnsPerHost = 0 // unlimited in-flight; idle pool is what matters
	t.IdleConnTimeout = 90 * time.Second
	return t
}
