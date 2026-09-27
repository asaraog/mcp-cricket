package milclive

// When a Minor League Cricket market is a price.
//
// Kalshi lists every Minor League game, but most of its books are not
// prices. On 2026-09-26 every Sunday book sat at 23¢ bid / 73¢ ask with no
// trades — a placeholder a market maker leaves so the event is not empty.
// Its midpoint is 48%, and printed as "48% (market)" it reads as a real
// consensus that the game is a coin flip. So a price is shown only when the
// book is informative on BOTH sides: bid and ask present, a spread of 10¢
// or less, or up to 20¢ with at least 1,000 contracts traded, and the two
// midpoints summing to roughly 100. The owner's words were "a tight spread
// and/or real volume".
//
// The decision is per event, never per market. A table with one side
// priced and the other blank looks like a data error, and the one side is
// usually the placeholder anyway. And the market is bound by the
// milestone's own event ticker, never by team names: the same two teams
// play on consecutive days, and BestMatch would happily price Sunday's
// game with Saturday's book.
//
// Only midpoints are shown, never the last trade: in a book this thin the
// last trade can be hours old and on the other side of the spread.
//
// Two statuses beyond the spec's priced/unpriced/none/closed, each because
// the reader was otherwise told something false. "onesided" is a settling
// book late in a lopsided chase — Atlanta Fire bid 99¢ on 14,261 contracts,
// Atlanta Lightning bid nothing — which is not a price either, but is not
// "a market that hasn't formed a price" as unpriced says. "unavailable" is a
// game over only by the clock, with no live data to say it ended; calling
// it finished and its market closed was a guess. It is also a game whose
// market could not be read from anywhere, which "none" used to cover with
// the false claim that no market existed (see Markets).
//
// The rule lives here, not in the tools, because more than one surface
// prints Minor League prices: the MCP tools (internal/mcp) here, and the
// website at cricketfornoobs.com, which runs this same package. A copy in
// each would drift the first time one of them was tightened, and an
// assistant would quote a 23/73 placeholder as "48% (market)" while the
// site correctly showed nothing.

import (
	"math"
	"strings"
	"sync"
	"time"

	"github.com/asaraog/mcp-cricket/internal/kalshi"
)

// MarketView is one game's market as the card, chat and MCP tools use it.
type MarketView struct {
	Status           string // priced | unpriced | onesided | none | closed | unavailable
	Set              *kalshi.MarketSet
	HomePct, AwayPct int
}

const (
	tightSpread  = 10
	liquidSpread = 20
	liquidVolume = 1000.0
	midSumMin    = 90
	midSumMax    = 110
)

// Markets reads the game's market. Before the game it reads the 2-minute
// scan (no request per viewer); in play it reads the event through a
// 20-second cache, because two minutes is a whole over. A finished game
// shows no price at all: its book is settling at 99/1. A game over only by
// the clock is "unavailable", not closed: nothing says it ended, and its
// market may well still be trading.
//
// Before the game, a scan miss is read from the event itself. The scan is
// empty for as long as a process's first fill runs — the series pages, up
// to ten generic /events pages and the quote enrichment behind them — and
// nothing waits for it. A one-shot stdio client that asked within those
// seconds, and the website for the same window after every deploy, were
// told "no open Kalshi market was found" about a book that was sitting
// there tight on both sides. After the fill this fires only on a real scan
// miss: one cached request per event, and a failed one is not retried for
// eventMissTTL, so the site still makes no request per viewer.
//
// Whenever no source answers, the status is "unavailable", never "none":
// "none" says no market exists, and a read that failed says nothing of the
// kind. "none" is kept for a game with no event ticker, and for an event
// Kalshi served with no open market in it.
func Markets(g Game, now time.Time) MarketView {
	state := g.State(now)
	if state == "post" {
		if !g.Finished() {
			return MarketView{Status: "unavailable"}
		}
		return MarketView{Status: "closed"}
	}
	if g.EventTicker == "" {
		return MarketView{Status: "none"}
	}
	var markets []kalshi.Market
	if state == "in" {
		if cands, err := kalshi.EventMarketsCached(g.EventTicker, 20*time.Second); err == nil {
			for _, c := range cands {
				markets = append(markets, c.Market)
			}
		}
	}
	if len(markets) == 0 {
		if set, ok := kalshi.EventSet(g.EventTicker); ok {
			markets = set.Markets
		}
	}
	if len(markets) == 0 {
		if state == "in" {
			// The event read above failed and the scan has nothing.
			return MarketView{Status: "unavailable"}
		}
		var ok bool
		if markets, ok = eventFallback(g.EventTicker); !ok {
			return MarketView{Status: "unavailable"}
		}
	}
	return PriceView(g.Home, g.Away, markets)
}

// eventMissTTL is how long a failed direct read of an event is remembered.
// It matches the scan's own refresh: an event Kalshi has not opened yet
// 404s, EventMarketsCached never caches an error, and without this every
// card and chat turn on that game would ask again.
const eventMissTTL = 2 * time.Minute

var (
	eventMissMu sync.Mutex
	eventMiss   = map[string]time.Time{} // event ticker -> wall clock to retry after
)

// eventFallback reads one event's open markets directly, for a game the
// scan does not hold. ok is false when the read failed, now or within
// eventMissTTL. Only open markets count, as in the scan: an event Kalshi
// serves with every market closed has no market to price, and "none" is
// then the truth.
//
// The read is cached for two minutes, the scan's own age. If the game
// starts inside those two minutes, its first in-play read can be served
// that entry; it is no older than the scan book the in-play path already
// falls back to.
func eventFallback(ticker string) (markets []kalshi.Market, ok bool) {
	key := strings.ToUpper(strings.TrimSpace(ticker))
	eventMissMu.Lock()
	until, missed := eventMiss[key]
	eventMissMu.Unlock()
	if missed && time.Now().Before(until) {
		return nil, false
	}
	cands, err := kalshi.EventMarketsCached(key, 2*time.Minute)
	if err != nil {
		eventMissMu.Lock()
		// Bounded as the event cache is: a season has a few dozen events.
		if len(eventMiss) > 500 {
			eventMiss = map[string]time.Time{}
		}
		eventMiss[key] = time.Now().Add(eventMissTTL)
		eventMissMu.Unlock()
		return nil, false
	}
	for _, c := range cands {
		switch c.Market.Status {
		case "", "active", "open":
			markets = append(markets, c.Market)
		}
	}
	return markets, true
}

// PriceView applies the priced-book rule to one event's markets.
func PriceView(home, away string, markets []kalshi.Market) MarketView {
	if len(markets) == 0 {
		return MarketView{Status: "none"}
	}
	var homeMk, awayMk []kalshi.Market
	for _, m := range markets {
		switch MarketSide(m.Title, home, away) {
		case 0:
			homeMk = append(homeMk, m)
		case 1:
			awayMk = append(awayMk, m)
		}
	}
	if len(homeMk) != 1 || len(awayMk) != 1 {
		return MarketView{Status: "unpriced"}
	}
	h, a := homeMk[0], awayMk[0]
	if oneSided(h, a) || oneSided(a, h) {
		return MarketView{Status: "onesided"}
	}
	if !informative(h) || !informative(a) {
		return MarketView{Status: "unpriced"}
	}
	hMid := float64(h.YesBid+h.YesAsk) / 2
	aMid := float64(a.YesBid+a.YesAsk) / 2
	if sum := hMid + aMid; sum < midSumMin || sum > midSumMax {
		return MarketView{Status: "unpriced"}
	}
	return MarketView{
		Status: "priced",
		Set: &kalshi.MarketSet{
			EventTitle: home + " vs " + away,
			Markets:    []kalshi.Market{midCopy(h, home, hMid), midCopy(a, away, aMid)},
		},
		HomePct: int(math.Round(hMid)),
		AwayPct: int(math.Round(aMid)),
	}
}

// informative is one side of a book that says something: quoted on both
// sides, not crossed, and tight — or only moderately wide with real volume.
func informative(m kalshi.Market) bool {
	if m.YesBid <= 0 || m.YesAsk <= 0 || m.YesAsk < m.YesBid {
		return false
	}
	spread := m.YesAsk - m.YesBid
	return spread <= tightSpread || (spread <= liquidSpread && m.Volume >= liquidVolume)
}

// oneSided reports a settling book: the favourite bid at 95¢ or more and
// the other side not bid at all. A placeholder quoted 1/99 on both sides is
// not one — neither side is bid high — and stays unpriced.
func oneSided(fav, dog kalshi.Market) bool {
	return fav.YesBid >= 95 && dog.YesBid <= 0
}

// midCopy is the market as shown: its midpoint as the implied probability,
// and the last trade erased so nothing downstream can print it.
//
// The midpoint is rounded to a whole cent first, the way the table rounds
// it. An odd bid+ask sum gives a half (Sunday's Chicago Kingsmen v St Louis
// book was 50/61), and Compare prints the market with %.0f, which rounds a
// half to even: a 64.5 midpoint read 64 in kalshi_model_comparison beside a
// table that said 65, and table_owns_the_numbers pointed the model at both.
func midCopy(m kalshi.Market, team string, mid float64) kalshi.Market {
	m.Title = team + " wins"
	m.LastPrice = 0
	m.PriceSource = ""
	m.ImpliedProb = math.Round(mid) / 100
	return m
}

// MarketSide maps a market title to 0 (home), 1 (away) or -1. Exact names
// only: the scan titles a market "Chicago Kingsmen wins", the event
// endpoint "Chicago Kingsmen", and a derby's two sides share a word, so any
// token matching would give both rows to one team.
func MarketSide(title, home, away string) int {
	c := Canon(strings.TrimSuffix(strings.TrimSpace(title), " wins"))
	switch {
	case c == "":
		return -1
	case c == Canon(home):
		return 0
	case c == Canon(away):
		return 1
	}
	return -1
}

// SideMarket returns the shown market for one team, when priced.
func (v MarketView) SideMarket(team string) (kalshi.Market, bool) {
	if v.Status != "priced" || v.Set == nil {
		return kalshi.Market{}, false
	}
	for _, m := range v.Set.Markets {
		if strings.TrimSuffix(m.Title, " wins") == team {
			return m, true
		}
	}
	return kalshi.Market{}, false
}
