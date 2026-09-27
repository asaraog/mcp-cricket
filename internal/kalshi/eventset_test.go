package kalshi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// failOnRequest points the package at a server that fails the test on any
// hit. The pre-game Minor League path must read the scan and nothing else;
// a request here is a request per viewer against the real exchange.
func failOnRequest(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected Kalshi request: %s", r.URL)
		http.Error(w, "no", http.StatusTeapot)
	}))
	restore := SetBaseURL(srv.URL)
	t.Cleanup(func() { restore(); srv.Close() })
}

func milcCapture(t *testing.T) []Candidate {
	t.Helper()
	body, err := os.ReadFile("testdata/kxt20match_milc.json")
	if err != nil {
		t.Fatal(err)
	}
	cands, _, err := ParseEventsPage(body)
	if err != nil {
		t.Fatal(err)
	}
	return cands
}

// The event ticker is the binding key for a Minor League game, so it has to
// survive decoding — upper-cased, because the milestone and the scan do not
// agree on case in every payload.
func TestParseEventsPageFillsEventTicker(t *testing.T) {
	body := []byte(`{"events":[{"event_ticker":"kxt20match-26sep261500chitigchki","title":"Chicago Kingsmen vs Chicago Tigers",
	  "markets":[{"ticker":"KXT20MATCH-26SEP261500CHITIGCHKI-CHKI","title":"Chicago Kingsmen wins","status":"active",
	    "yes_bid_dollars":"0.6600","yes_ask_dollars":"0.7600"}]}]}`)
	cands, _, err := ParseEventsPage(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].EventTicker != "KXT20MATCH-26SEP261500CHITIGCHKI" {
		t.Fatalf("event ticker not filled in upper case: %+v", cands)
	}
}

// A malformed event_ticker must cost that one field. The same shape of
// failure as volume_fp: one mistyped field used to empty the whole scan.
func TestEventTickerParseNeverKillsThePage(t *testing.T) {
	for _, bad := range []string{`null`, `123`, `{}`} {
		body := []byte(`{"events":[
		  {"event_ticker":` + bad + `,"title":"A vs B","markets":[{"ticker":"A-1","title":"A wins","status":"active","last_price_dollars":"0.5000"}]},
		  {"event_ticker":"KXT20MATCH-X","title":"C vs D","markets":[{"ticker":"C-1","title":"C wins","status":"active","last_price_dollars":"0.4000"}]}]}`)
		cands, _, err := ParseEventsPage(body)
		if err != nil {
			t.Errorf("event_ticker %s failed the page: %v", bad, err)
			continue
		}
		if len(cands) != 2 {
			t.Errorf("event_ticker %s: want both markets to survive, got %d", bad, len(cands))
			continue
		}
		if cands[1].EventTicker != "KXT20MATCH-X" {
			t.Errorf("event_ticker %s: the good event lost its ticker: %q", bad, cands[1].EventTicker)
		}
		if bad == `{}` && cands[0].EventTicker != "" {
			t.Errorf("an object is not a ticker, got %q", cands[0].EventTicker)
		}
	}
}

// The live path reads one event at a time; it needs the ticker and the
// traded volume from that endpoint too, since volume is what decides
// whether a 20¢ spread is a price.
func TestParseEventJSONFillsTickerAndVolume(t *testing.T) {
	body := []byte(`{"event":{"event_ticker":"KXT20MATCH-26SEP261400ATLLTNATLF","title":"Atlanta Fire vs Atlanta Lightning",
	  "markets":[
	    {"ticker":"KXT20MATCH-26SEP261400ATLLTNATLF-ATLF","title":"Atlanta Fire wins","yes_sub_title":"Atlanta Fire","status":"active",
	     "yes_bid_dollars":"0.6900","yes_ask_dollars":"0.8200","volume_fp":"1605.87"},
	    {"ticker":"KXT20MATCH-26SEP261400ATLLTNATLF-ATLLTN","title":"Atlanta Lightning wins","yes_sub_title":"Atlanta Lightning","status":"active",
	     "yes_bid_dollars":"0.2000","yes_ask_dollars":"0.3000","volume_fp":1834.61}]}}`)
	cands, err := ParseEventJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 2 {
		t.Fatalf("got %d markets", len(cands))
	}
	for _, c := range cands {
		if c.EventTicker != "KXT20MATCH-26SEP261400ATLLTNATLF" {
			t.Errorf("event ticker = %q", c.EventTicker)
		}
	}
	if cands[0].Market.Volume != 1605.87 || cands[1].Market.Volume != 1834.61 {
		t.Errorf("volumes = %v, %v", cands[0].Market.Volume, cands[1].Market.Volume)
	}
	if cands[0].Market.Title != "Atlanta Fire" {
		t.Errorf("title = %q, want the yes_sub_title", cands[0].Market.Title)
	}
}

// Two "Los Angeles Lashings vs Seattle Thunderbolts" events are listed at
// once, a day apart. Team-name matching cannot tell them apart; the event
// ticker can. The scan also lists every market twice, and without the
// de-duplication no event ever had exactly one market per side.
func TestEventSetBindsByTickerAndDedupes(t *testing.T) {
	failOnRequest(t)
	cands := milcCapture(t)
	SeedScan(append(append([]Candidate{}, cands...), cands...))
	t.Cleanup(func() { SeedScan(nil) })

	sat, ok := EventSet("KXT20MATCH-26SEP261700SETHBLOANLA")
	if !ok || len(sat.Markets) != 2 {
		t.Fatalf("Saturday's event: ok=%v markets=%d, want 2", ok, len(sat.Markets))
	}
	sun, ok := EventSet("kxt20match-26sep271600sethbloanla")
	if !ok || len(sun.Markets) != 2 {
		t.Fatalf("Sunday's event: ok=%v markets=%d, want 2", ok, len(sun.Markets))
	}
	for _, a := range sat.Markets {
		for _, b := range sun.Markets {
			if a.Ticker == b.Ticker {
				t.Errorf("the two days share market %s", a.Ticker)
			}
		}
	}
	if sat.EventTitle != "Los Angeles Lashings vs Seattle Thunderbolts" {
		t.Errorf("event title = %q", sat.EventTitle)
	}
	if _, ok := EventSet("KXT20MATCH-NOTREAL"); ok {
		t.Error("an unknown ticker returned markets")
	}
	if _, ok := EventSet(""); ok {
		t.Error("an empty ticker returned markets")
	}
}

// The series fetch follows the cursor, but never forever.
func TestFetchSeriesFollowsTheCursorAtMostThreePages(t *testing.T) {
	page := func(ticker, cursor string) []byte {
		return []byte(fmt.Sprintf(`{"cursor":%q,"events":[{"event_ticker":%q,"title":"E","markets":[{"ticker":%q,"title":"X wins","status":"active"}]}]}`,
			cursor, ticker, ticker+"-X"))
	}
	cases := []struct {
		name    string
		cursors []string // cursor returned by page i
		want    int
	}{
		{"one page, empty cursor", []string{""}, 1},
		{"two pages then empty", []string{"c1", ""}, 2},
		{"repeated cursor stops", []string{"c1", "c1", "c2"}, 2},
		{"never more than three", []string{"c1", "c2", "c3", "c4", "c5"}, 3},
	}
	for _, tc := range cases {
		var calls int32
		var urls []string
		get := func(u string) ([]byte, error) {
			i := atomic.AddInt32(&calls, 1) - 1
			urls = append(urls, u)
			return page(fmt.Sprintf("EV%d", i), tc.cursors[i]), nil
		}
		got := fetchSeries(get, "KXT20MATCH")
		if len(got) != tc.want || int(calls) != tc.want {
			t.Errorf("%s: %d candidates from %d calls, want %d", tc.name, len(got), calls, tc.want)
		}
		if len(urls) > 1 && !strings.Contains(urls[1], "cursor=c1") {
			t.Errorf("%s: the second page did not send the cursor: %s", tc.name, urls[1])
		}
	}
	// A failing page ends the walk and keeps what was read.
	n := 0
	got := fetchSeries(func(string) ([]byte, error) {
		n++
		if n == 2 {
			return nil, fmt.Errorf("boom")
		}
		return page("EV", "next"), nil
	}, "KXT20MATCH")
	if len(got) != 1 || n != 2 {
		t.Errorf("error mid-walk: %d candidates after %d calls", len(got), n)
	}
}

// The live path's cache: one request serves concurrent callers, an error is
// never cached, and the last good value keeps serving.
func TestEventMarketsCachedServesOneFetch(t *testing.T) {
	var hits int32
	fail := int32(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if atomic.LoadInt32(&fail) == 1 {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, `{"event":{"event_ticker":"KXT20MATCH-CACHE1","title":"A vs B","markets":[
		  {"ticker":"KXT20MATCH-CACHE1-A","yes_sub_title":"A","status":"active","yes_bid_dollars":"0.5000","yes_ask_dollars":"0.5200"}]}}`)
	}))
	restore := SetBaseURL(srv.URL)
	t.Cleanup(func() { restore(); srv.Close() })

	done := make(chan []Candidate, 20)
	for i := 0; i < 20; i++ {
		go func() {
			c, _ := EventMarketsCached("KXT20MATCH-CACHE1", time.Minute)
			done <- c
		}()
	}
	for i := 0; i < 20; i++ {
		if c := <-done; len(c) != 1 {
			t.Fatalf("a concurrent caller got %d markets", len(c))
		}
	}
	if h := atomic.LoadInt32(&hits); h != 1 {
		t.Errorf("20 concurrent first calls made %d requests, want 1", h)
	}

	// Errors are not cached: an unknown ticker fails, and fails again.
	atomic.StoreInt32(&fail, 1)
	for i := 0; i < 2; i++ {
		if _, err := EventMarketsCached("KXT20MATCH-CACHE2", time.Minute); err == nil {
			t.Errorf("attempt %d: a 500 on the first fill must be an error", i+1)
		}
	}
	// The good ticker, once stale, keeps serving its last value through the
	// outage while the refresh behind it fails.
	eventMu.Lock()
	eventCache["KXT20MATCH-CACHE1"].exp = time.Now().Add(-time.Second)
	eventMu.Unlock()
	if c, err := EventMarketsCached("KXT20MATCH-CACHE1", time.Minute); err != nil || len(c) != 1 {
		t.Errorf("last good value not served through an error: %v %d", err, len(c))
	}
	waitEventFlight(t, "KXT20MATCH-CACHE1")
	if c, err := EventMarketsCached("KXT20MATCH-CACHE1", time.Minute); err != nil || len(c) != 1 {
		t.Errorf("a failed refresh dropped the last good value: %v %d", err, len(c))
	}
	waitEventFlight(t, "KXT20MATCH-CACHE1")
}

// waitEventFlight waits for a ticker's background refresh to land.
func waitEventFlight(t *testing.T, key string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		eventMu.Lock()
		_, busy := eventFlights[key]
		eventMu.Unlock()
		if !busy {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the refresh for %s never landed", key)
}

// A book older than two minutes is never served as the live price. While
// the refresh kept failing, the last good book used to stay on the card and
// in chat under "Chance of winning, right now:" for the whole outage.
func TestEventMarketsCachedNeverServesAnOldBook(t *testing.T) {
	var fail, hang atomic.Bool
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hang.Load() {
			<-release
		}
		if fail.Load() {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, `{"event":{"event_ticker":"KXT20MATCH-AGED","title":"A vs B","markets":[
		  {"ticker":"KXT20MATCH-AGED-A","yes_sub_title":"A","status":"active","yes_bid_dollars":"0.5000","yes_ask_dollars":"0.5200"}]}}`)
	}))
	restore := SetBaseURL(srv.URL)
	t.Cleanup(func() {
		close(release)
		waitEventFlight(t, "KXT20MATCH-AGED")
		restore()
		srv.Close()
	})
	const key = "KXT20MATCH-AGED"
	if c, err := EventMarketsCached(key, 20*time.Second); err != nil || len(c) != 1 {
		t.Fatalf("first fill: %v %d", err, len(c))
	}
	age := func(d time.Duration) {
		eventMu.Lock()
		e := eventCache[key]
		e.exp, e.fetched = time.Now().Add(-time.Second), time.Now().Add(-d)
		eventMu.Unlock()
	}

	// Ninety seconds old: past its TTL, still served while it refreshes.
	fail.Store(true)
	age(90 * time.Second)
	if c, err := EventMarketsCached(key, 20*time.Second); err != nil || len(c) != 1 {
		t.Errorf("a 90-second-old book should still be served: %v %d", err, len(c))
	}
	waitEventFlight(t, key)

	// Three minutes old and the refresh fails: an error, never the old book.
	age(3 * time.Minute)
	if c, err := EventMarketsCached(key, 20*time.Second); err == nil {
		t.Errorf("a three-minute-old book was served as live: %d markets", len(c))
	}
	waitEventFlight(t, key)

	// Three minutes old and the exchange hangs: one bounded wait, then the
	// next reader fails at once instead of waiting again.
	hang.Store(true)
	age(3 * time.Minute)
	start := time.Now()
	if _, err := EventMarketsCached(key, 20*time.Second); err == nil {
		t.Error("a hanging refresh served the old book")
	}
	if waited := time.Since(start); waited > 3*time.Second {
		t.Errorf("waited %v on a hanging refresh", waited)
	}
	start = time.Now()
	if _, err := EventMarketsCached(key, 20*time.Second); err == nil || time.Since(start) > 200*time.Millisecond {
		t.Errorf("a second reader waited again on the same stuck refresh: %v after %v", err, time.Since(start))
	}
}
