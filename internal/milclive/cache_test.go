package milclive

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeKalshi serves the capture and counts what it is asked. fail, when
// set, answers the live batch with a 500; competition400 refuses the
// competition filter the way an undocumented parameter one day might.
type fakeKalshi struct {
	t              *testing.T
	milestones     int32
	batches        int32
	fail           atomic.Bool
	competition400 atomic.Bool
	mu             sync.Mutex
	queries        []string
}

func (f *fakeKalshi) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.queries = append(f.queries, r.URL.Path+"?"+r.URL.RawQuery)
	f.mu.Unlock()
	switch {
	case strings.HasSuffix(r.URL.Path, "/milestones"):
		atomic.AddInt32(&f.milestones, 1)
		q := r.URL.Query()
		if f.competition400.Load() && q.Get("competition") != "" {
			http.Error(w, "bad filter", http.StatusBadRequest)
			return
		}
		if q.Get("competition") == "" && q.Get("type") != "cricket_match" {
			f.t.Errorf("unfiltered milestones listing requested: %s", r.URL)
		}
		_, _ = w.Write(readFile(f.t, "milestones.json"))
	case strings.HasSuffix(r.URL.Path, "/live_data/batch"):
		atomic.AddInt32(&f.batches, 1)
		if f.fail.Load() {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		if len(r.URL.Query()["milestone_ids"]) == 0 {
			f.t.Errorf("batch asked with no ids: %s", r.URL)
		}
		_, _ = w.Write(readFile(f.t, "live_batch.json"))
	default:
		f.t.Errorf("unexpected request %s", r.URL)
		http.NotFound(w, r)
	}
}

func serveCapture(t *testing.T) *fakeKalshi {
	t.Helper()
	t.Setenv("MILC_LIVE", "")
	f := &fakeKalshi{t: t}
	srv := httptest.NewServer(f)
	restore := SetBaseURL(srv.URL)
	t.Cleanup(func() {
		waitIdle(t)
		restore()
		srv.Close()
	})
	return f
}

// waitIdle waits for a background refresh to land, so the counts a test
// reads are final.
func waitIdle(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st.mu.Lock()
		busy := st.inFlight
		st.mu.Unlock()
		if !busy {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("a refresh never finished")
}

// Every reader shares one fetch: fifty page loads arriving on a cold cache
// cost Kalshi one listing and one batch.
func TestCacheServesConcurrentReadersFromOneFetch(t *testing.T) {
	f := serveCapture(t)
	var wg sync.WaitGroup
	counts := make([]int, 50)
	for i := range counts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			counts[i] = len(Games(capAt))
		}(i)
	}
	wg.Wait()
	waitIdle(t)
	if m, b := atomic.LoadInt32(&f.milestones), atomic.LoadInt32(&f.batches); m != 1 || b != 1 {
		t.Errorf("50 concurrent reads made %d listing and %d batch requests, want 1 and 1", m, b)
	}
	for i, n := range counts {
		if n != 9 {
			t.Errorf("reader %d saw %d games, want the 9 in the window at 19:36Z", i, n)
			break
		}
	}
	// In play first, then upcoming, then finished.
	gs := Games(capAt)
	order := make([]string, len(gs))
	for i, g := range gs {
		order[i] = g.State(capAt)
	}
	if got := strings.Join(order, ","); got != "in,in,in,in,in,pre,pre,post,post" {
		t.Errorf("order = %s", got)
	}
	if g, ok := Lookup("8f1efc49-9d8e-4c4b-a1a2-6b5f0f4b4a0b", capAt); ok {
		t.Errorf("a made-up id resolved to %s v %s", g.Home, g.Away)
	}
	if up := Upcoming(capAt, 3); len(up) != 3 || up[0].Home != "Lone Star Athletics" {
		t.Errorf("upcoming = %d games, first %+v", len(up), up)
	}
	if named := NamedIn("how are the Chicago Kingsmen doing?", capAt); len(named) != 1 || named[0].ID[:8] != "bfd5348c" {
		t.Errorf("NamedIn found %d games", len(named))
	}
	for _, msg := range []string{"what are yorkers?", "is cricket big with Americans?", "how are the Titans doing?"} {
		if named := NamedIn(msg, capAt); len(named) != 0 {
			t.Errorf("%q named %s v %s by a partial name", msg, named[0].Home, named[0].Away)
		}
	}
	if h := Health(); h["fixtures"] != 21 || h["last_error"] != "" || h["enabled"] != true {
		t.Errorf("health = %v", h)
	}
}

// A failed refresh keeps serving the last good score and waits 30 seconds
// before asking again, rather than hammering an endpoint that is down.
func TestCacheKeepsLastGoodDataThroughAnError(t *testing.T) {
	f := serveCapture(t)
	if n := len(Games(capAt)); n != 9 {
		t.Fatalf("first fill: %d games", n)
	}
	waitIdle(t)
	f.fail.Store(true)

	later := capAt.Add(20 * time.Second) // the 15s live TTL has passed
	gs := Games(later)
	waitIdle(t)
	if len(gs) != 9 {
		t.Fatalf("the failing refresh dropped the list: %d games", len(gs))
	}
	if b := atomic.LoadInt32(&f.batches); b != 2 {
		t.Fatalf("batches = %d, want the fill and one failed refresh", b)
	}
	atl, _ := Lookup("e720b1fd-8081-4a11-a44f-6518e94a2e3c", later)
	waitIdle(t)
	if atl.ScoreLine(later) == "" {
		t.Error("the last good score was dropped on a failed refresh")
	}
	for _, s := range []time.Duration{25 * time.Second, 45 * time.Second} {
		Games(capAt.Add(s))
		waitIdle(t)
	}
	if b := atomic.LoadInt32(&f.batches); b != 2 {
		t.Errorf("retried within 30 seconds of a failure: %d batch requests", b)
	}
	if h := Health(); h["last_error"] == "" {
		t.Error("health does not report the failure")
	}
	f.fail.Store(false)
	Games(capAt.Add(51 * time.Second))
	waitIdle(t)
	if b := atomic.LoadInt32(&f.batches); b != 3 {
		t.Errorf("no retry after the pause: %d batch requests", b)
	}
}

// If the competition filter is ever refused, the listing is asked for
// cricket matches and filtered here — once, never the all-sports crawl.
func TestCacheFallsBackWhenTheCompetitionFilterIsRefused(t *testing.T) {
	f := serveCapture(t)
	f.competition400.Store(true)
	if n := len(Games(capAt)); n != 9 {
		t.Errorf("fallback listing: %d games, want 9", n)
	}
	waitIdle(t)
	f.mu.Lock()
	defer f.mu.Unlock()
	var sawType bool
	for _, q := range f.queries {
		if strings.Contains(q, "type=cricket_match") {
			sawType = true
		}
	}
	if !sawType || atomic.LoadInt32(&f.milestones) != 2 {
		t.Errorf("fallback not taken exactly once: %d listing requests, queries %v", f.milestones, f.queries)
	}
}

// MILC_LIVE=off is the kill switch: nothing is fetched and nothing listed.
func TestKillSwitch(t *testing.T) {
	f := serveCapture(t)
	t.Setenv("MILC_LIVE", "off")
	if len(Games(capAt)) != 0 || len(Upcoming(capAt, 5)) != 0 || len(NamedIn("Chicago Kingsmen", capAt)) != 0 {
		t.Error("MILC_LIVE=off still listed games")
	}
	if _, ok := Lookup("bfd5348c-4804-477e-b6c9-406e7063eb2d", capAt); ok {
		t.Error("MILC_LIVE=off still resolved a game")
	}
	Warm(time.Second)
	if n := atomic.LoadInt32(&f.milestones) + atomic.LoadInt32(&f.batches); n != 0 {
		t.Errorf("MILC_LIVE=off made %d requests", n)
	}
	if Health()["enabled"] != false {
		t.Error("health does not report the switch")
	}
}

// After a quiet spell the first reader waits, briefly, for live data fresh
// enough to use. The cache has no ticker: without the wait, the first
// /matches after ten idle minutes was built from ten-minute-old live data,
// so a game in play had no usable score, lost the page's auto-pick, and
// kept it until the reader reloaded.
func TestCacheWaitsForLiveDataAfterAQuietSpell(t *testing.T) {
	serveCapture(t)
	if n := len(Games(capAt)); n != 9 {
		t.Fatalf("first fill: %d games", n)
	}
	waitIdle(t)
	later := capAt.Add(11 * time.Minute)
	var atl Game
	for _, g := range Games(later) {
		if g.ID[:8] == "e720b1fd" {
			atl = g
		}
	}
	if atl.Live == nil || !atl.Live.FetchedAt.Equal(later) || !atl.Scored(later) {
		t.Errorf("the first read after 11 quiet minutes served the old snapshot: fetched %v, scored %v",
			atl.Live != nil && atl.Live.FetchedAt.Equal(later), atl.Scored(later))
	}
}

// The first-fill wait happens once. While the listing kept failing it used
// to recur on every request made during a retry — every ESPN chat turn
// among them, since chat switching reads Games.
func TestCacheFirstFillWaitIsOneTime(t *testing.T) {
	t.Setenv("MILC_LIVE", "")
	first, rest := make(chan struct{}), make(chan struct{})
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			<-first
		} else {
			<-rest
		}
		http.Error(w, "down", http.StatusBadGateway)
	}))
	restore := SetBaseURL(srv.URL)
	t.Cleanup(func() {
		close(rest)
		waitIdle(t)
		restore()
		srv.Close()
	})

	start := time.Now()
	Games(capAt)
	if d := time.Since(start); d < firstWait-300*time.Millisecond || d > 5*time.Second {
		t.Errorf("the first fill waited %v, want about %v", d, firstWait)
	}
	start = time.Now()
	Games(capAt)
	if d := time.Since(start); d > 300*time.Millisecond {
		t.Errorf("a second reader of the same hanging fill waited %v", d)
	}
	close(first)
	waitIdle(t)

	// The fill failed. Its retry hangs too, and nobody waits on it.
	start = time.Now()
	Games(capAt.Add(errorPause + time.Second))
	if d := time.Since(start); d > 300*time.Millisecond {
		t.Errorf("a reader waited %v on a retry after the first fill had failed", d)
	}
	// The retry's request is sent from the refresh goroutine; give it a
	// moment to arrive before counting.
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&hits) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if h := atomic.LoadInt32(&hits); h != 2 {
		t.Errorf("listing requests = %d, want the fill and one retry", h)
	}
}

// Rain seen after the start marks a game for good: the overs it cost stay
// cut after the word leaves the status. Built from two real polls of the
// New England Eagles game, which Kalshi never gave a MaxOv.
func TestCacheRemembersAnInterruption(t *testing.T) {
	t.Setenv("MILC_LIVE", "")
	var batch atomic.Value
	batch.Store("live_batch_2004z.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/milestones"):
			_, _ = w.Write(readFile(t, "milestones_2004z.json"))
		case strings.HasSuffix(r.URL.Path, "/live_data/batch"):
			_, _ = w.Write(readFile(t, batch.Load().(string)))
		default:
			http.NotFound(w, r)
		}
	}))
	restore := SetBaseURL(srv.URL)
	t.Cleanup(func() {
		waitIdle(t)
		restore()
		srv.Close()
	})
	const ne, atl = "2263013a-8693-439e-8e50-a3e98b45c34b", "e720b1fd-8081-4a11-a44f-6518e94a2e3c"
	Games(chaseAt)
	waitIdle(t)
	if g, _ := Lookup(ne, chaseAt); !g.Interrupted {
		t.Error("rain at 20:04Z did not mark the game")
	}
	if g, _ := Lookup(atl, chaseAt); g.Interrupted {
		t.Error("a game that never saw rain was marked")
	}

	batch.Store("live_batch_2056z.json") // "Ball in Progress": no delay word
	Games(reducedAt)
	waitIdle(t)
	g, _ := Lookup(ne, reducedAt)
	if !g.Interrupted {
		t.Error("the mark was lost when the status stopped saying rain")
	}
	if _, ok := g.TotalOvers(); ok {
		t.Error("an interrupted game went back to assuming 20 overs")
	}
	if got := g.ScoreLine(reducedAt); !strings.Contains(got, "(2.3/7 ov, target 63)") {
		t.Errorf("reduced chase line = %q", got)
	}
}
