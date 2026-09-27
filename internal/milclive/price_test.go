package milclive

// The priced-book rule, tested where it lives. The MCP tools and the
// hosted website share this one implementation, so these tests pin what
// both of them print.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/asaraog/mcp-cricket/internal/kalshi"
)

// kalshiCapture is the Minor League events as they stood at 19:36Z on
// 2026-09-26, each with its nested markets.
const kalshiCapture = "../kalshi/testdata/kxt20match_milc.json"

// captureMarkets returns one event's markets from the Kalshi capture.
func captureMarkets(t *testing.T, ticker string) []kalshi.Market {
	t.Helper()
	body, err := os.ReadFile(kalshiCapture)
	if err != nil {
		t.Fatal(err)
	}
	cands, _, err := kalshi.ParseEventsPage(body)
	if err != nil {
		t.Fatal(err)
	}
	var out []kalshi.Market
	for _, c := range cands {
		if c.EventTicker == ticker {
			out = append(out, c.Market)
		}
	}
	if len(out) == 0 {
		t.Fatalf("no markets for %s", ticker)
	}
	return out
}

func book(team string, bid, ask int, vol float64) kalshi.Market {
	return kalshi.Market{Ticker: team, Title: team + " wins", YesBid: bid, YesAsk: ask, Volume: vol, LastPrice: 97}
}

// Every book in the capture, judged. The Sunday placeholders (23/73 or
// 24/72, nothing traded) are the reason the rule exists: priced, each would
// have read as a real coin flip.
func TestPriceViewOnTheCapture(t *testing.T) {
	cases := []struct {
		name, ticker, home, away, status string
		homePct, awayPct                 int
	}{
		// 69/82 is a 13¢ spread, priced only because 1,606 contracts traded.
		{"Atlanta", "KXT20MATCH-26SEP261400ATLLTNATLF", "Atlanta Fire", "Atlanta Lightning", "priced", 76, 25},
		{"Chicago", "KXT20MATCH-26SEP261500CHITIGCHKI", "Chicago Kingsmen", "Chicago Tigers", "priced", 71, 29},
		{"LA Saturday", "KXT20MATCH-26SEP261700SETHBLOANLA", "Los Angeles Lashings", "Seattle Thunderbolts", "priced", 56, 48},
		// 50/54 and 46/51: tight on both sides, the midpoints sum to 100.5,
		// and 48.5 rounds away from zero as the table rounds it.
		{"Lone Star", "KXT20MATCH-26SEP261600METTRALOSTAT", "Lone Star Athletics", "MetroPlex Tracers", "priced", 52, 49},
		// 57/69 with 45 contracts: too wide for its volume.
		{"Philadelphia", "KXT20MATCH-26SEP261400NYCTTNPHL", "The Philadelphians", "NYC Titans", "unpriced", 0, 0},
		{"New England", "KXT20MATCH-26SEP261400NJSMCAVNENGEAG", "New England Eagles", "New Jersey Somerset Cavaliers", "unpriced", 0, 0},
		{"East Bay", "KXT20MATCH-26SEP261400SAROGREBB", "East Bay Blazers", "San Ramon Grizzlies", "unpriced", 0, 0},
		// The Sunday placeholders.
		{"NYC Sunday", "KXT20MATCH-26SEP271000MYNYCTTN", "NYC Titans", "Manhattan Yorkers", "unpriced", 0, 0},
		{"LA Sunday", "KXT20MATCH-26SEP271600SETHBLOANLA", "Los Angeles Lashings", "Seattle Thunderbolts", "unpriced", 0, 0},
		// 43/63 is exactly the 20¢ limit, but nothing has traded.
		{"Chicago v St Louis", "KXT20MATCH-26SEP271500STLOAMCHKI", "Chicago Kingsmen", "St Louis Americans", "unpriced", 0, 0},
	}
	for _, c := range cases {
		v := PriceView(c.home, c.away, captureMarkets(t, c.ticker))
		if v.Status != c.status {
			t.Errorf("%s: %s, want %s", c.name, v.Status, c.status)
			continue
		}
		if c.status != "priced" {
			if v.Set != nil || v.HomePct != 0 || v.AwayPct != 0 {
				t.Errorf("%s: unpriced but carries numbers: %+v", c.name, v)
			}
			continue
		}
		if v.HomePct != c.homePct || v.AwayPct != c.awayPct {
			t.Errorf("%s: %d/%d, want %d/%d", c.name, v.HomePct, v.AwayPct, c.homePct, c.awayPct)
		}
		for _, m := range v.Set.Markets {
			if m.LastPrice != 0 || m.PriceSource != "" {
				t.Errorf("%s: a shown market keeps its last trade: %+v", c.name, m)
			}
		}
	}
}

func TestPriceViewRefusesWhatIsNotAPrice(t *testing.T) {
	for name, ms := range map[string][]kalshi.Market{
		"one side unbid":    {book("A", 0, 5, 5000), book("B", 90, 96, 5000)},
		"mids sum to 115":   {book("A", 60, 62, 5000), book("B", 53, 55, 5000)},
		"one side only":     {book("A", 60, 62, 5000)},
		"crossed book":      {book("A", 62, 60, 5000), book("B", 38, 40, 5000)},
		"1/99 placeholders": {book("A", 1, 99, 0), book("B", 1, 99, 0)},
		"21¢ with volume":   {book("A", 40, 61, 50000), book("B", 40, 50, 50000)},
		"two markets a side": {book("A", 60, 62, 5000), book("A", 60, 62, 5000),
			book("B", 38, 40, 5000)},
	} {
		if v := PriceView("A", "B", ms); v.Status != "unpriced" || v.Set != nil {
			t.Errorf("%s: %+v", name, v)
		}
	}
	if v := PriceView("A", "B", nil); v.Status != "none" {
		t.Errorf("no markets: %s", v.Status)
	}

	// A settling book is one-sided, not unformed: Atlanta Fire bid 99¢ on
	// 14,261 contracts at 20:52Z with Atlanta Lightning unbid.
	settling := []kalshi.Market{book("Atlanta Fire", 99, 100, 14261.70), book("Atlanta Lightning", 0, 1, 6361.78)}
	if v := PriceView("Atlanta Fire", "Atlanta Lightning", settling); v.Status != "onesided" || v.Set != nil {
		t.Errorf("settling book: %+v", v)
	}
}

// The shown market is the midpoint, rounded the way the table rounds it,
// with the last trade erased. A half-cent midpoint (63/66 is 64.5) printed
// 64 in the comparison and 65 in the table.
func TestPriceViewShowsMidpointsOnly(t *testing.T) {
	v := PriceView("A", "B", []kalshi.Market{book("A", 63, 66, 5000), book("B", 34, 37, 5000)})
	if v.Status != "priced" || v.HomePct != 65 || v.AwayPct != 36 {
		t.Fatalf("view: %+v", v)
	}
	m, ok := v.SideMarket("A")
	if !ok || m.ImpliedProb != 0.65 || m.LastPrice != 0 || m.Title != "A wins" {
		t.Errorf("A's shown market: %+v ok=%v", m, ok)
	}
	if cmp := kalshi.Compare(m, "A", 0.5); !strings.Contains(cmp.Read, "(65%)") {
		t.Errorf("comparison %q disagrees with the table's 65%%", cmp.Read)
	}
	if _, ok := v.SideMarket("C"); ok {
		t.Error("a team not in the game has a market")
	}
	if _, ok := (MarketView{Status: "unpriced"}).SideMarket("A"); ok {
		t.Error("an unpriced view handed out a market")
	}
}

// Exact names only: a derby's sides share a word.
func TestMarketSideIsExact(t *testing.T) {
	for _, c := range []struct {
		title string
		want  int
	}{
		{"Chicago Tigers wins", 1},
		{"Chicago Kingsmen", 0},
		{"Chicago wins", -1},
		{"", -1},
	} {
		if got := MarketSide(c.title, "Chicago Kingsmen", "Chicago Tigers"); got != c.want {
			t.Errorf("MarketSide(%q) = %d, want %d", c.title, got, c.want)
		}
	}
	if MarketSide("St. Louis Americans wins", "Chicago Kingsmen", "St Louis Americans") != 1 {
		t.Error("a period in the exchange's spelling lost the market")
	}
}

// A finished game shows no price: its book is settling. A game over only
// by the clock is "unavailable", never "closed": nothing said it ended.
// Neither reaches Kalshi.
func TestMarketsAfterTheGame(t *testing.T) {
	gs := captureGames(t)
	if v := Markets(gs["99cf27e2"], capAt); v.Status != "closed" || v.Set != nil {
		t.Errorf("finished game market: %+v", v)
	}
	g := gs["b564fe71"]
	g.Live = nil
	now := g.DisplayStart().Add(4 * time.Hour)
	if g.State(now) != "post" || g.Finished() {
		t.Fatalf("precondition: state %q finished %v", g.State(now), g.Finished())
	}
	if v := Markets(g, now); v.Status != "unavailable" {
		t.Errorf("clock-only end: %s, want unavailable", v.Status)
	}
	noTicker := gs["b564fe71"]
	noTicker.EventTicker = ""
	if v := Markets(noTicker, capAt); v.Status != "none" {
		t.Errorf("no event ticker: %s, want none", v.Status)
	}
}

// Before a game the market is the 2-minute scan's, and a process whose
// first scan is still filling holds an empty one. A one-shot stdio client
// asking in those seconds was told "no open Kalshi market was found" about
// Saturday's Los Angeles book, tight on both sides. A scan miss now reads
// the event itself; a read that fails is "unavailable", never "none", and
// the next call does not ask again.
func TestMarketsBeforeTheScanFills(t *testing.T) {
	body, err := os.ReadFile(kalshiCapture)
	if err != nil {
		t.Fatal(err)
	}
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
	var misses atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := events[strings.ToUpper(strings.TrimPrefix(r.URL.Path, "/events/"))]
		if !strings.HasPrefix(r.URL.Path, "/events/") || !ok {
			misses.Add(1)
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"event":%s}`, raw)
	}))
	restore := kalshi.SetBaseURL(srv.URL)
	kalshi.SeedScan(nil) // what a process holds while its first fill runs
	t.Cleanup(func() {
		restore()
		srv.Close()
		kalshi.SeedScan(nil)
	})

	g := captureGames(t)["b564fe71"] // Los Angeles Lashings v Seattle Thunderbolts, Saturday
	if g.State(capAt) != "pre" {
		t.Fatalf("precondition: state %q", g.State(capAt))
	}
	if v := Markets(g, capAt); v.Status != "priced" || v.HomePct != 56 || v.AwayPct != 48 {
		t.Errorf("a priced book behind an empty scan: %+v, want priced 56/48", v)
	}

	unlisted := g
	unlisted.EventTicker = "KXT20MATCH-26SEP261700NOTLISTED"
	for i := 0; i < 3; i++ {
		if v := Markets(unlisted, capAt); v.Status != "unavailable" || v.Set != nil {
			t.Errorf("call %d: a failed read gave %+v, want unavailable", i, v)
		}
	}
	if n := misses.Load(); n != 1 {
		t.Errorf("%d requests for an event whose read failed, want 1: the miss must be remembered", n)
	}
}
