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
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// apiBase is a var only so tests can point the package at a local server.
// Minor League Cricket binds a market by event ticker, and the tests for that
// path must prove it makes no request at all before the game (the 2-minute
// scan is the only source then) — which needs a server that fails the test
// on any hit, not the real exchange.
var apiBase = "https://api.elections.kalshi.com/trade-api/v2"

var client = &http.Client{Timeout: 10 * time.Second, Transport: tunedTransport()}

// SetBaseURL points every request at u and returns the function that puts
// the real host back. Tests only; production never calls it.
func SetBaseURL(u string) (restore func()) {
	prev := apiBase
	apiBase = strings.TrimRight(u, "/")
	return func() { apiBase = prev }
}

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
	// Volume is contracts traded over the market's life. It is what tells a
	// real price from a placeholder: a Minor League book 20¢ wide is a price
	// only with real volume behind it (milclive.PriceView).
	Volume float64 `json:"volume,omitempty"`
}

// dollars is Kalshi's 2026 price encoding: a decimal string ("0.5600").
// The integer-cent fields the API launched with stopped being populated —
// the market column silently vanished when they did — so every decode
// carries both and cents() folds them together.
type dollars string

func (d dollars) cents() int {
	if d == "" {
		return 0
	}
	v, err := strconv.ParseFloat(string(d), 64)
	if err != nil {
		return 0
	}
	return int(math.Round(v * 100))
}

func centsOf(old int, neu dollars) int {
	if old > 0 {
		return old
	}
	return neu.cents()
}

// flexNum decodes a JSON field that may arrive as a number OR a quoted
// string. Kalshi sends volume_fp as a string ("4360.94"); declaring it
// float64 made the whole page fail to unmarshal, and refreshScan skips any
// page that errors — so one mistyped field silently emptied the entire
// market scan and every match on the site lost its market column.
//
// It never returns an error. A volume that will not parse is worth zero,
// not worth destroying the page it arrived on.
type flexNum float64

func (f *flexNum) UnmarshalJSON(b []byte) error {
	*f = 0
	t := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if t == "" || t == "null" {
		return nil
	}
	if v, err := strconv.ParseFloat(t, 64); err == nil {
		*f = flexNum(v)
	}
	return nil
}

// flexStr decodes a field that should be a string but has no contract
// saying so. event_ticker is the one Minor League Cricket binds its market
// by, and the same lesson as flexNum applies: a null, a number or an object
// in it must cost that one field, never the page of events it arrived on.
// It never returns an error. A non-string keeps its trimmed raw text so a
// log line can still show what came back.
type flexStr string

func (f *flexStr) UnmarshalJSON(b []byte) error {
	*f = ""
	t := strings.TrimSpace(string(b))
	if t == "" || t == "null" {
		return nil
	}
	if strings.HasPrefix(t, `"`) {
		var s string
		if err := json.Unmarshal(b, &s); err == nil {
			*f = flexStr(s)
		}
		return nil
	}
	if strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
		return nil // an object is not a ticker, and its text is not either
	}
	*f = flexStr(t)
	return nil
}

type marketResp struct {
	Market struct {
		Ticker     string  `json:"ticker"`
		Title      string  `json:"title"`
		Status     string  `json:"status"`
		YesBid     int     `json:"yes_bid"`
		YesAsk     int     `json:"yes_ask"`
		LastPrice  int     `json:"last_price"`
		YesBidD    dollars `json:"yes_bid_dollars"`
		YesAskD    dollars `json:"yes_ask_dollars"`
		LastPriceD dollars `json:"last_price_dollars"`
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
		YesBid:    centsOf(r.Market.YesBid, r.Market.YesBidD),
		YesAsk:    centsOf(r.Market.YesAsk, r.Market.YesAskD),
		LastPrice: centsOf(r.Market.LastPrice, r.Market.LastPriceD),
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
	// EventTicker is the event this market belongs to, upper-cased. It is
	// the only safe key for a Minor League Cricket game: team-name matching
	// cannot tell "Los Angeles Lashings vs Seattle Thunderbolts" on Saturday
	// from the same fixture on Sunday, and both are listed at once. Market
	// itself is unchanged, so nothing that serialises a Market (the LLM
	// payload, the market guard) sees a new field.
	EventTicker string
	Market      Market
}

type eventsPage struct {
	Cursor string `json:"cursor"`
	Events []struct {
		Title       string  `json:"title"`
		EventTicker flexStr `json:"event_ticker"`
		Markets     []struct {
			Ticker      string  `json:"ticker"`
			Title       string  `json:"title"`
			YesSubTitle string  `json:"yes_sub_title"`
			Status      string  `json:"status"`
			YesBid      int     `json:"yes_bid"`
			YesAsk      int     `json:"yes_ask"`
			LastPrice   int     `json:"last_price"`
			YesBidD     dollars `json:"yes_bid_dollars"`
			YesAskD     dollars `json:"yes_ask_dollars"`
			LastPriceD  dollars `json:"last_price_dollars"`
			VolumeFP    flexNum `json:"volume_fp"`
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
			bid := centsOf(m.YesBid, m.YesBidD)
			ask := centsOf(m.YesAsk, m.YesAskD)
			last := centsOf(m.LastPrice, m.LastPriceD)
			out = append(out, Candidate{
				EventTitle:  ev.Title,
				EventTicker: strings.ToUpper(strings.TrimSpace(string(ev.EventTicker))),
				Market: Market{
					Ticker: m.Ticker, Title: m.Title, Status: m.Status,
					YesBid: bid, YesAsk: ask, LastPrice: last,
					ImpliedProb: impliedProb(bid, ask, last),
					Volume:      float64(m.VolumeFP),
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
			Title       string  `json:"title"`
			EventTicker flexStr `json:"event_ticker"`
			// The nested-market shape needs the *_dollars fields too. The
			// integer cent fields stopped being populated in Kalshi's 2026
			// API and come back null here exactly as they do on the single
			// market endpoint, so an event's markets decoded to 0/0 and the
			// whole set priced at zero. Chat still showed numbers only
			// because fillPrice falls back to the last trade, which is a
			// staler figure than the live bid/ask mid this restores.
			Markets []struct {
				Ticker      string  `json:"ticker"`
				Title       string  `json:"title"`
				YesSubTitle string  `json:"yes_sub_title"`
				Status      string  `json:"status"`
				YesBid      int     `json:"yes_bid"`
				YesAsk      int     `json:"yes_ask"`
				LastPrice   int     `json:"last_price"`
				YesBidD     dollars `json:"yes_bid_dollars"`
				YesAskD     dollars `json:"yes_ask_dollars"`
				LastPriceD  dollars `json:"last_price_dollars"`
				// Volume decides whether a 20¢ spread is a real price or
				// an untraded placeholder, so the live Minor League path
				// needs it from this endpoint as much as from the scan.
				VolumeFP flexNum `json:"volume_fp"`
			} `json:"markets"`
		} `json:"event"`
	}
	if err := json.Unmarshal(body, &d); err != nil {
		return nil, fmt.Errorf("kalshi event: %w", err)
	}
	evTicker := strings.ToUpper(strings.TrimSpace(string(d.Event.EventTicker)))
	var out []Candidate
	for _, m := range d.Event.Markets {
		title := m.Title
		if m.YesSubTitle != "" {
			title = m.YesSubTitle
		}
		bid := centsOf(m.YesBid, m.YesBidD)
		ask := centsOf(m.YesAsk, m.YesAskD)
		last := centsOf(m.LastPrice, m.LastPriceD)
		out = append(out, Candidate{
			EventTitle:  d.Event.Title,
			EventTicker: evTicker,
			Market: Market{
				Ticker: m.Ticker, Title: title, Status: m.Status,
				YesBid: bid, YesAsk: ask, LastPrice: last,
				ImpliedProb: impliedProb(bid, ask, last),
				Volume:      float64(m.VolumeFP),
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

// ------------------------------------------------ event-ticker lookups

// A live Minor League Cricket game reads its market through
// EventMarketsCached rather than the 2-minute scan: two minutes is a whole
// over, and a price that old next to a score 15 seconds old reads as a
// market that ignored the last six balls. Every viewer of every Minor
// League row asks for the same few events, so one fetch per event per TTL
// serves them all.

type eventEntry struct {
	cands   []Candidate
	exp     time.Time
	fetched time.Time
}

type eventFlight struct {
	done  chan struct{}
	cands []Candidate
	err   error
	// gaveUp is set when a reader stopped waiting on this flight because it
	// ran past eventStaleWait. Later readers of the same flight then fail
	// at once instead of each waiting again on an exchange that is hanging.
	gaveUp bool
}

const (
	// eventMaxAge is the oldest book shown as "right now". Past it the
	// entry is never served: a refresh that keeps failing used to leave the
	// last good book on the card and in chat under "Chance of winning, right
	// now:" for as long as the outage lasted, with nothing to say how old
	// it was. The caller falls back to the 2-minute scan, or to no price.
	eventMaxAge = 2 * time.Minute
	// eventStaleWait bounds the wait for a refresh replacing a book older
	// than eventMaxAge.
	eventStaleWait = 1500 * time.Millisecond
)

var (
	eventMu      sync.Mutex
	eventCache   = map[string]*eventEntry{}
	eventFlights = map[string]*eventFlight{}
)

// EventMarketsCached is GetEventMarkets behind a stale-while-revalidate
// cache with one fetch in flight per ticker. An error is never cached. A
// book past its TTL but younger than eventMaxAge is served while the
// refresh runs behind it; an older one never is — the caller waits up to
// eventStaleWait for the refresh and otherwise gets an error. Only the
// first call for a ticker waits on the network without that bound.
func EventMarketsCached(eventTicker string, ttl time.Duration) ([]Candidate, error) {
	key := NormalizeTicker(eventTicker)
	if key == "" {
		return nil, fmt.Errorf("kalshi: empty event ticker")
	}
	now := time.Now()
	eventMu.Lock()
	e, have := eventCache[key]
	if have && now.Before(e.exp) {
		cands := e.cands
		eventMu.Unlock()
		return cands, nil
	}
	fl, flying := eventFlights[key]
	if !flying {
		fl = &eventFlight{done: make(chan struct{})}
		eventFlights[key] = fl
		// A bounded map: a season has a few dozen Minor League events, and
		// a process that somehow collects hundreds is holding dead ones.
		if len(eventCache) > 500 {
			eventCache = map[string]*eventEntry{}
		}
		go fetchEventFlight(key, fl, ttl)
	}
	if have && now.Sub(e.fetched) <= eventMaxAge {
		cands := e.cands
		eventMu.Unlock()
		return cands, nil
	}
	if have && fl.gaveUp {
		eventMu.Unlock()
		return nil, fmt.Errorf("kalshi: event %s book is older than %v and its refresh is stuck", key, eventMaxAge)
	}
	eventMu.Unlock()
	if !have {
		<-fl.done
		return fl.cands, fl.err
	}
	t := time.NewTimer(eventStaleWait)
	defer t.Stop()
	select {
	case <-fl.done:
		if fl.err != nil {
			return nil, fl.err
		}
		if len(fl.cands) == 0 {
			return nil, fmt.Errorf("kalshi: event %s returned no markets", key)
		}
		return fl.cands, nil
	case <-t.C:
		eventMu.Lock()
		fl.gaveUp = true
		eventMu.Unlock()
		return nil, fmt.Errorf("kalshi: event %s book is older than %v", key, eventMaxAge)
	}
}

// fetchEventFlight runs one flight: fetch, store a good result, publish the
// outcome to everyone waiting, and clear the flight.
func fetchEventFlight(key string, fl *eventFlight, ttl time.Duration) {
	cands, err := GetEventMarkets(key)
	eventMu.Lock()
	if err == nil && len(cands) > 0 {
		at := time.Now()
		eventCache[key] = &eventEntry{cands: cands, exp: at.Add(ttl), fetched: at}
	}
	fl.cands, fl.err = cands, err
	delete(eventFlights, key)
	eventMu.Unlock()
	close(fl.done)
}

// EventSet returns the markets of one event from the cached scan, bound by
// event ticker and nothing else. It makes no HTTP request and does not call
// EnrichAll, so a pre-game Minor League card costs nothing per viewer.
//
// The scan lists each KXT20MATCH market twice — once from the series fetch
// and once from the generic crawl — so markets are de-duplicated by ticker.
// Without that, "exactly one market per side" failed for every event and
// every book read as unpriced.
func EventSet(eventTicker string) (MarketSet, bool) {
	want := strings.TrimSpace(eventTicker)
	if want == "" {
		return MarketSet{}, false
	}
	var ms MarketSet
	seen := map[string]bool{}
	for _, c := range openMarketScan() {
		if !strings.EqualFold(c.EventTicker, want) || seen[c.Market.Ticker] {
			continue
		}
		seen[c.Market.Ticker] = true
		if ms.EventTitle == "" {
			ms.EventTitle = c.EventTitle
		}
		ms.Markets = append(ms.Markets, c.Market)
	}
	return ms, len(ms.Markets) > 0
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

// WordInText reports whether w appears in t as a whole word rather than as
// a run of letters inside a longer one — plain strings.Contains lets
// "kings" (from "St Lucia Kings") match inside "Kingsmen", a different
// team's name that merely starts with the same letters. Exported so
// cmd/server's own team-title matching (teamInTitle) uses the identical
// rule instead of a second, driftable copy.
func WordInText(t, w string) bool {
	re, err := regexp.Compile(`\b` + regexp.QuoteMeta(w) + `\b`)
	if err != nil {
		return strings.Contains(t, w) // pathological input: fall back rather than panic
	}
	return re.MatchString(t)
}

func matchedToken(text, team string) string {
	for _, tok := range teamTokens(team) {
		if WordInText(text, tok) {
			return tok
		}
	}
	return ""
}

// matchStrength scores how a team was found in text: 2 when the token that
// matched is the team's FULL name, 1 when it is only a single distinctive
// word. A traced incident showed why the distinction matters: "St Lucia
// Kings" matched a market titled "Hh Kingsmen Academy" through the single
// word "kings" (itself only found because "kings" is a substring of
// "kingsmen" — separately fixed), while the real St Lucia Kings vs Jamaica
// Kingsmen event, which matched on the full names, existed in the same
// candidate pool the whole time. Both used to count as an equally good
// match, tie-broken only by which had live liquidity — essentially
// arbitrary, since an unrelated live match can easily have tighter
// bid/ask than one that only just started. A full-name match is
// structurally much less likely to be a coincidence than one shared word,
// so it must outrank a weak match outright, not merely tiebreak against it.
func matchStrength(text, team string) int {
	tok := matchedToken(text, team)
	if tok == "" {
		return 0
	}
	if tok == strings.ToLower(strings.TrimSpace(team)) {
		return 2
	}
	return 1
}

// BestMatch finds the candidate whose event or market title names BOTH
// teams. Runs two passes: first only full-name matches on both sides, and
// only if that finds nothing does it fall back to single-word matches
// (needed for real ESPN/Kalshi nickname disagreements, e.g. "Kandy Royals"
// vs "Kandy Falcons" sharing just the city). Liquidity still tiebreaks
// within a pass, never across one — a weak match never outranks a strong
// one no matter its liquidity. Returns (market, matchedTeamName, found).
func BestMatch(cands []Candidate, teamA, teamB string) (Market, string, bool) {
	for _, minStrength := range []int{2, 1} {
		var best *Candidate
		bestTeam := ""
		for i := range cands {
			text := strings.ToLower(cands[i].EventTitle + " " + cands[i].Market.Title)
			tokA, tokB := matchedToken(text, teamA), matchedToken(text, teamB)
			if tokA == "" || tokB == "" || tokA == tokB {
				continue // both teams matching on one shared word is no match
			}
			sA, sB := matchStrength(text, teamA), matchStrength(text, teamB)
			if sA < minStrength || sB < minStrength {
				continue
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
		if best != nil {
			return best.Market, bestTeam, true
		}
	}
	return Market{}, "", false
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
// WarmScan populates the candidate cache before the first request needs it.
// openMarketScan returns whatever it has and refreshes behind the scenes, so
// a freshly started process answers with NO market for the first minute or
// so. Two things came out of that gap on 2026-08-12: the market column
// silently vanished from the win table after each deploy, and a betting
// answer with no market in context turned the model's own 52% heuristic into
// "the market prices them closely at -109".
func WarmScan() {
	scanMu.Lock()
	if scanInFlight {
		scanMu.Unlock()
		return
	}
	scanInFlight = true
	scanMu.Unlock()
	refreshScan()
}

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
		all = append(all, fetchSeries(getBody, s)...)
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

// fetchSeries reads one series' open events, following the page cursor for
// at most three pages. The series fetch used to read one page and throw the
// cursor away, which was harmless while KXT20MATCH listed 42 events and
// silently truncates the moment a busy weekend pushes it past 200 — the
// Minor League games are listed last and would be the ones cut. It stops on
// an empty cursor or one it has already seen, so a server echoing the same
// cursor cannot loop it.
func fetchSeries(get func(string) ([]byte, error), s string) []Candidate {
	var all []Candidate
	cursor := ""
	for page := 0; page < 3; page++ {
		u := apiBase + "/events?status=open&with_nested_markets=true&limit=200&series_ticker=" + url.QueryEscape(s)
		if cursor != "" {
			u += "&cursor=" + url.QueryEscape(cursor)
		}
		body, err := get(u)
		if err != nil {
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

// getBody is a GET that treats any non-200 as an error.
func getBody(u string) ([]byte, error) {
	resp, err := client.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kalshi: HTTP %s", resp.Status)
	}
	return body, nil
}

// SeedScan installs a scan result as though a refresh had just finished,
// good for an hour so nothing refreshes behind a test. Tests only.
func SeedScan(c []Candidate) {
	scanMu.Lock()
	scanCands, scanExp, scanInFlight = c, time.Now().Add(time.Hour), false
	scanMu.Unlock()
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
