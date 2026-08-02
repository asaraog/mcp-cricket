package cricinfo

import (
	"sync"
	"testing"
	"time"
)

func TestFromCache(t *testing.T) {
	calls := 0
	fetch := func() (int, error) { calls++; return 42, nil }

	v, _ := fromCache("test:k1", time.Minute, fetch)
	if v != 42 {
		t.Fatalf("got %d", v)
	}
	fromCache("test:k1", time.Minute, fetch)
	if calls != 1 {
		t.Errorf("cached key refetched: %d calls", calls)
	}
	fromCache("test:k2", time.Minute, fetch)
	if calls != 2 {
		t.Errorf("distinct key should fetch: %d calls", calls)
	}
	// Stale-while-revalidate: an expired entry serves the OLD value
	// immediately and refreshes in the background.
	fromCache("test:k3", 0, fetch) // ttl 0 = immediate expiry (calls -> 3)
	fresh := 0
	fetch2 := func() (int, error) { fresh++; return 99, nil }
	v, _ = fromCache("test:k3", 0, fetch2)
	if v != 42 {
		t.Errorf("expired entry should serve stale value, got %d", v)
	}
	deadline := time.Now().Add(2 * time.Second)
	for fresh == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if fresh == 0 {
		t.Error("background refresh never ran")
	}
}

func TestFromCacheSingleflight(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	slow := func() (int, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		time.Sleep(150 * time.Millisecond)
		return 7, nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if v, err := fromCache("test:sf", time.Minute, slow); err != nil || v != 7 {
				t.Errorf("got %d %v", v, err)
			}
		}()
	}
	wg.Wait()
	if calls != 1 {
		t.Errorf("singleflight failed: %d upstream calls for 8 concurrent misses", calls)
	}
}

func checkParse(t *testing.T, score string, wantRuns, wantWkts int, wantOvers *float64) {
	t.Helper()
	runs, wkts, overs, ok := ParseScoreString(score)
	if !ok {
		t.Fatalf("ParseScoreString(%q) not ok", score)
	}
	if runs != wantRuns || wkts != wantWkts {
		t.Errorf("ParseScoreString(%q) = %d/%d, want %d/%d", score, runs, wkts, wantRuns, wantWkts)
	}
	switch {
	case wantOvers == nil && overs != nil:
		t.Errorf("ParseScoreString(%q) overs = %v, want nil", score, *overs)
	case wantOvers != nil && overs == nil:
		t.Errorf("ParseScoreString(%q) overs = nil, want %v", score, *wantOvers)
	case wantOvers != nil && *overs != *wantOvers:
		t.Errorf("ParseScoreString(%q) overs = %v, want %v", score, *overs, *wantOvers)
	}
}

func fp(f float64) *float64 { return &f }

func TestParseScoreString(t *testing.T) {
	checkParse(t, "311", 311, 10, nil)
	checkParse(t, "96/1 (19 ov)", 96, 1, fp(19.0))
	checkParse(t, "145/3 (16.2 ov)", 145, 3, fp(16.2))
	checkParse(t, "311 & 96/1 (19 ov)", 96, 1, fp(19.0))
	checkParse(t, "450/6d", 450, 6, nil)
	checkParse(t, "159/9 (96 balls)", 159, 9, fp(16.0))
	checkParse(t, "67/1 (8.5/20 ov, target 160)", 67, 1, fp(8.5))

	for _, bad := range []string{"TBD", "", "  "} {
		if _, _, _, ok := ParseScoreString(bad); ok {
			t.Errorf("ParseScoreString(%q) should not be ok", bad)
		}
	}
}

func TestToMatchStateChase(t *testing.T) {
	m := Match{
		StatusText: "Live",
		Teams: []TeamScore{
			{Name: "Washington Freedom", Score: "174"},
			{Name: "LA Knight Riders", Score: "140/4 (16.2 ov)"},
		},
	}
	s := ToMatchState(m)
	if s == nil {
		t.Fatal("expected state")
	}
	if s.BattingTeam != "LA Knight Riders" || s.Innings != 2 {
		t.Errorf("batting=%q innings=%d", s.BattingTeam, s.Innings)
	}
	if s.Target == nil || *s.Target != 175 {
		t.Errorf("target = %v, want 175", s.Target)
	}
	if s.Runs != 140 || s.Overs != 16.2 {
		t.Errorf("runs=%d overs=%v", s.Runs, s.Overs)
	}
}

func TestToMatchStateFirstInnings(t *testing.T) {
	m := Match{Teams: []TeamScore{{Name: "A", Score: "80/2 (10 ov)"}, {Name: "B"}}}
	s := ToMatchState(m)
	if s == nil {
		t.Fatal("expected state")
	}
	if s.Innings != 1 || s.Target != nil || s.BattingTeam != "A" {
		t.Errorf("innings=%d target=%v batting=%q", s.Innings, s.Target, s.BattingTeam)
	}
}

func TestToMatchStateExplicitTarget(t *testing.T) {
	m := Match{
		StatusText: "Live",
		Teams: []TeamScore{
			{Name: "Trent Rockets", Score: "67/1 (8.5/20 ov, target 160)"},
			{Name: "London Spirit", Score: "159/9"},
		},
	}
	s := ToMatchState(m)
	if s == nil {
		t.Fatal("expected state")
	}
	if s.BattingTeam != "Trent Rockets" || s.Innings != 2 {
		t.Errorf("batting=%q innings=%d", s.BattingTeam, s.Innings)
	}
	if s.Target == nil || *s.Target != 160 {
		t.Errorf("target=%v want 160", s.Target)
	}
	if s.Overs != 8.5 || s.TotalOvers != 20 {
		t.Errorf("overs=%v total=%d", s.Overs, s.TotalOvers)
	}
}

func TestTestMatchDetection(t *testing.T) {
	// ESPN class.eventType is authoritative even when status is just "Live".
	byType := Match{EventType: "Test", StatusText: "Live",
		Teams: []TeamScore{{Name: "WI", Score: "311"}, {Name: "PAK", Score: "96/1 (19 ov)"}}}
	if !IsTestMatch(byType) {
		t.Error("event_type=Test should flag as Test")
	}
	if ToMatchState(byType) != nil {
		t.Error("Test match should return nil state")
	}

	byScore := Match{StatusText: "Live",
		Teams: []TeamScore{{Name: "A", Score: "311 & 96/1 (19 ov)"}, {Name: "B", Score: "402"}}}
	if !IsTestMatch(byScore) {
		t.Error("ampersand score should flag as Test")
	}

	byText := Match{StatusText: "Day 2 - Session 3",
		Teams: []TeamScore{{Name: "WI", Score: "311"}, {Name: "PAK", Score: "96/1 (19 ov)"}}}
	if !IsTestMatch(byText) {
		t.Error("'Day N' status should flag as Test")
	}

	t20 := Match{StatusText: "Live",
		Teams: []TeamScore{{Name: "Freedom", Score: "174"}, {Name: "LAKR", Score: "140/4 (16.2 ov)"}}}
	if IsTestMatch(t20) {
		t.Error("T20 wrongly flagged as Test")
	}
}

func TestODIGets50Overs(t *testing.T) {
	m := Match{EventType: "ODI", StatusText: "Live",
		Teams: []TeamScore{{Name: "A", Score: "280"}, {Name: "B", Score: "150/3 (30 ov)"}}}
	s := ToMatchState(m)
	if s == nil {
		t.Fatal("expected state")
	}
	if s.TotalOvers != 50 {
		t.Errorf("total overs = %d, want 50", s.TotalOvers)
	}
	if s.Target == nil || *s.Target != 281 {
		t.Errorf("target = %v, want 281", s.Target)
	}
}

func TestNoScoresReturnsNil(t *testing.T) {
	m := Match{Teams: []TeamScore{{Name: "A"}, {Name: "B"}}}
	if ToMatchState(m) != nil {
		t.Error("no scores should return nil")
	}
}

func TestStringWinnerFlagParses(t *testing.T) {
	// Finished matches carry winner as the STRING "true"/"false" — this
	// broke whole-league parsing until flexBool.
	body := []byte(`{"events":[{"id":"9","name":"A v B","status":{"type":{"state":"post","detail":"Final"}},
		"competitions":[{"competitors":[
			{"team":{"displayName":"A"},"score":"152/8","winner":"true"},
			{"team":{"displayName":"B"},"score":"147/9 (20 ov, target 153)","winner":"false"}]}]}]}`)
	m, err := SimplifyScoreboardJSON(body, "23077", "9")
	if err != nil {
		t.Fatal(err)
	}
	if !m.Teams[0].Winner || m.Teams[1].Winner {
		t.Errorf("winner flags wrong: %+v", m.Teams)
	}
}

func TestSimplifyScoreboardJSON(t *testing.T) {
	body := []byte(`{"events":[{"id":"111","name":"A v B","status":{"type":{"state":"in","detail":"Live"}},
		"competitions":[{"class":{"eventType":"T20"},"competitors":[
			{"team":{"displayName":"A"},"score":"174"},
			{"team":{"displayName":"B"},"score":"140/4 (16.2 ov)"}]}]}]}`)
	m, err := SimplifyScoreboardJSON(body, "19601", "111")
	if err != nil {
		t.Fatal(err)
	}
	if m.EventID != "111" || m.EventType != "T20" || len(m.Teams) != 2 {
		t.Errorf("parsed %+v", m)
	}
	if s := ToMatchState(m); s == nil || s.BattingTeam != "B" {
		t.Errorf("state from client data: %+v", s)
	}
	if _, err := SimplifyScoreboardJSON(body, "19601", "999"); err == nil {
		t.Error("missing event should error")
	}
	// A Test at stumps: ESPN says detail="Live" but description="Stumps" —
	// the specific phase must win.
	stumps := []byte(`{"events":[{"id":"5","name":"C v D","status":{"type":{"state":"in","detail":"Live","description":"Stumps"}},
		"competitions":[{"class":{"eventType":"Test"},"competitors":[
			{"team":{"displayName":"C"},"score":"311"},
			{"team":{"displayName":"D"},"score":"282"}]}]}]}`)
	sm, err := SimplifyScoreboardJSON(stumps, "24436", "5")
	if err != nil || sm.StatusText != "Stumps" {
		t.Errorf("want StatusText Stumps, got %q (err %v)", sm.StatusText, err)
	}
	if _, err := SimplifyScoreboardJSON([]byte("not json"), "1", "1"); err == nil {
		t.Error("bad json should error")
	}
}

func TestBallsFromPlayByPlayJSON(t *testing.T) {
	// Pages are chronological (oldest->newest); we serve newest-first,
	// fall back to shortText (GSL/MLC games leave text empty), and drop
	// lines empty in both fields.
	body := []byte(`{"commentary":{"pageCount":29,"items":[
		{"text":" Full, worked into midwicket ","over":{"actual":8.4},"scoreValue":1,"innings":{"number":2}},
		{"text":"","over":{"actual":8.5}},
		{"text":"","shortText":"Lawes to Robinson, SIX","over":{"actual":8.5},"scoreValue":6,"sequence":100705,"innings":{"number":2,"runs":64,"wickets":1,"remainingBalls":69}},
		{"text":"Short, pulled for four","over":{"actual":8.6},"scoreValue":4,"innings":{"number":2}}]}}`)
	balls, err := BallsFromPlayByPlayJSON(body)
	if err != nil || len(balls) != 3 {
		t.Fatalf("balls=%v err=%v", balls, err)
	}
	if balls[0].Text != "Short, pulled for four" || balls[0].Over != 8.6 {
		t.Errorf("newest should be first: %+v", balls[0])
	}
	if balls[1].Text != "Lawes to Robinson, SIX" || balls[1].ScoreValue != 6 || balls[1].Sequence != 100705 {
		t.Errorf("shortText fallback + sequence passthrough: %+v", balls[1])
	}
	if balls[1].InningsRuns != 64 || balls[1].InningsWickets != 1 || balls[1].BallsRemaining != 69 {
		t.Errorf("innings position passthrough: %+v", balls[1])
	}
	if balls[2].Text != "Full, worked into midwicket" || balls[2].Over != 8.4 {
		t.Errorf("oldest last: %+v", balls[2])
	}
	if _, err := BallsFromPlayByPlayJSON([]byte("nope")); err == nil {
		t.Error("bad json should error")
	}
	// ESPN embeds markup in big moments — it must be stripped.
	tagged, _ := BallsFromPlayByPlayJSON([]byte(`{"commentary":{"pageCount":1,"items":[
		{"text":"<b>Baker gets Marsh!</b> Edged & taken","over":{"actual":11.3},"scoreValue":0,"innings":{"number":1}}]}}`))
	if len(tagged) != 1 || tagged[0].Text != "Baker gets Marsh! Edged & taken" {
		t.Errorf("html should be stripped: %+v", tagged)
	}
}

func TestPlayersFromSummaryJSON(t *testing.T) {
	// Batting-side players carry ballsFaced; bowling-side carry balls
	// (bowled). Active players get lines; unused batters go to yet-to-bat;
	// unused bowlers are skipped.
	wrap := func(stats string) string {
		return `"linescores":[{"linescores":[{"statistics":{"categories":[{"name":"general","stats":[` +
			stats + `]}]}}]}]`
	}
	body := []byte(`{"rosters":[
		{"team":{"displayName":"Unicorns"},"roster":[
			{"athlete":{"displayName":"Robinson"},` + wrap(
		`{"name":"ballsFaced","value":19},{"name":"runs","value":26},
		 {"name":"fours","value":2},{"name":"sixes","value":1},
		 {"name":"strikeRate","value":136.84,"displayValue":"136.84"}`) + `},
			{"athlete":{"displayName":"Krishnamurthi"},` + wrap(
		`{"name":"ballsFaced","value":0},{"name":"runs","value":0}`) + `}]},
		{"team":{"displayName":"Vipers"},"roster":[
			{"athlete":{"displayName":"Shadab Khan"},` + wrap(
		`{"name":"balls","value":6},{"name":"overs","value":1,"displayValue":"1"},
		 {"name":"conceded","value":4},{"name":"wickets","value":1},
		 {"name":"economyRate","value":4,"displayValue":"4"}`) + `},
			{"athlete":{"displayName":"Bench Bowler"},` + wrap(
		`{"name":"balls","value":0}`) + `}]}]}`)
	players, yetToBat, err := PlayersFromSummaryJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(players) != 2 {
		t.Fatalf("want 2 active players, got %+v", players)
	}
	if p := players[0]; !p.Batted || p.Runs != 26 || p.BallsFaced != 19 || p.StrikeRate != "136.84" || p.Team != "Unicorns" {
		t.Errorf("batter line wrong: %+v", p)
	}
	if p := players[1]; !p.Bowled || p.Wickets != 1 || p.Conceded != 4 || p.Overs != "1" {
		t.Errorf("bowler line wrong: %+v", p)
	}
	if len(yetToBat) != 1 || yetToBat[0] != "Krishnamurthi" {
		t.Errorf("yet-to-bat wrong: %v", yetToBat)
	}
}
