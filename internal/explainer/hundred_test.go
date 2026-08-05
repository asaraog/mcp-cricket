package explainer

import "testing"

// The Hundred: five-ball sets, 100 deliveries. With six assumed, the state
// said 38 balls left when 31 remained and priced the chase accordingly.
func TestHundredBallArithmetic(t *testing.T) {
	tgt := 156
	st := MatchState{Runs: 108, Wickets: 5, Overs: 13.4, TotalOvers: 20,
		Innings: 2, Target: &tgt, BPO: 5}
	if got := st.BallsBowled(); got != 69 {
		t.Errorf("BallsBowled = %d, want 69 (13 sets + 4)", got)
	}
	if got := st.BallsLeft(); got != 31 {
		t.Errorf("BallsLeft = %d, want 31 of 100", got)
	}
	rr, ok := st.RequiredRunRate()
	if !ok || rr < 7.6 || rr > 7.8 {
		t.Errorf("RequiredRunRate = %.2f, want ~7.74 (48 off 31 at 5-ball overs)", rr)
	}
	// Same numbers read as a T20 must keep the six-ball world.
	st.BPO = 0
	if got := st.BallsLeft(); got != 38 {
		t.Errorf("six-ball BallsLeft = %d, want 38", got)
	}
}

// The fitted Hundred segments must actually be selected and produce sane
// prices. 48 needed off 31 with five wickets is a competitive chase — the
// probability should sit in the broad middle, not at an extreme; and the
// same state read with six-ball eyes must differ, or the routing is dead.
func TestHundredSegmentsRoute(t *testing.T) {
	tgt := 156
	st := MatchState{BattingTeam: "Trent Rockets", BowlingTeam: "Oval Invincibles",
		Runs: 108, Wickets: 5, Overs: 13.4, TotalOvers: 20,
		Innings: 2, Target: &tgt, BPO: 5}
	p := WinProbability(st).BattingTeamWinProb
	if p < 0.10 || p > 0.90 {
		t.Errorf("hundred chase prob = %.3f, want a live contest", p)
	}
	st6 := st
	st6.BPO = 0
	if p6 := WinProbability(st6).BattingTeamWinProb; p6 == p {
		t.Errorf("six-ball reading priced identically (%.3f) — hnd segments not routed", p)
	}
}

// Franchise Elo: both Hundred pools must resolve, and the men's and
// women's sides of one franchise must be allowed to differ.
func TestHundredEloPools(t *testing.T) {
	rm, gm := eloFor(20, "Trent Rockets (Men)", 5)
	rw, gw := eloFor(20, "Trent Rockets (Women)", 5)
	if gm == 0 || gw == 0 {
		t.Fatalf("hundred elo pools missing: men games=%v women games=%v", gm, gw)
	}
	if rm == rw {
		t.Logf("note: men's and women's ratings coincide (%.1f) — legal but unlikely", rm)
	}
	if r20, _ := eloFor(20, "Trent Rockets", 6); r20 != 1500 {
		t.Errorf("t20 pool has a Hundred franchise at %.1f; expected neutral 1500", r20)
	}
}
