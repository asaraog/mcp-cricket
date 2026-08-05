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
