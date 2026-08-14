package explainer

import (
	"math"
	"strings"
	"testing"
)

func intp(n int) *int { return &n }

func TestOversToBalls(t *testing.T) {
	if b, _ := OversToBalls(16.0); b != 96 {
		t.Errorf("16.0 overs = %d balls, want 96", b)
	}
	// 16.2 = 16 overs + 2 balls = 98, NOT 16.2 * 6
	if b, _ := OversToBalls(16.2); b != 98 {
		t.Errorf("16.2 overs = %d balls, want 98", b)
	}
	if b, _ := OversToBalls(6.6); b != 42 { // ESPN's just-completed over
		t.Errorf("6.6 ov should be 42 balls, got %d", b)
	}
	if _, err := OversToBalls(10.7); err == nil {
		t.Error("10.7 should be invalid (ball digit must be 0-5)")
	}
	if s := BallsToOversStr(98); s != "16.2" {
		t.Errorf("98 balls = %q, want 16.2", s)
	}
}

func TestRunRates(t *testing.T) {
	s := MatchState{BattingTeam: "Unicorns", BowlingTeam: "Freedom",
		Runs: 120, Wickets: 3, Overs: 12.0, TotalOvers: 20, Innings: 2, Target: intp(180)}
	if crr := s.CurrentRunRate(); crr != 10.0 {
		t.Errorf("CRR = %v, want 10.0", crr)
	}
	s.Runs = 100 // 80 needed off 48 balls -> 10/over
	if rrr, ok := s.RequiredRunRate(); !ok || rrr != 10.0 {
		t.Errorf("RRR = %v (%v), want 10.0", rrr, ok)
	}
}

func TestChaseProbability(t *testing.T) {
	won := MatchState{BattingTeam: "A", BowlingTeam: "B", Runs: 181, Wickets: 5, Overs: 19.0, TotalOvers: 20, Innings: 2, Target: intp(180)}
	if p := ChaseWinProbability(won); p != 1.0 {
		t.Errorf("target reached: p=%v, want 1", p)
	}
	outOfBalls := MatchState{BattingTeam: "A", BowlingTeam: "B", Runs: 150, Wickets: 5, Overs: 20.0, TotalOvers: 20, Innings: 2, Target: intp(180)}
	if p := ChaseWinProbability(outOfBalls); p != 0.0 {
		t.Errorf("out of balls: p=%v, want 0", p)
	}
	allOut := MatchState{BattingTeam: "A", BowlingTeam: "B", Runs: 150, Wickets: 10, Overs: 15.0, TotalOvers: 20, Innings: 2, Target: intp(180)}
	if p := ChaseWinProbability(allOut); p != 0.0 {
		t.Errorf("all out: p=%v, want 0", p)
	}
	easy := MatchState{BattingTeam: "A", BowlingTeam: "B", Runs: 150, Wickets: 2, Overs: 15.0, TotalOvers: 20, Innings: 2, Target: intp(170)}
	hard := MatchState{BattingTeam: "A", BowlingTeam: "B", Runs: 100, Wickets: 8, Overs: 15.0, TotalOvers: 20, Innings: 2, Target: intp(200)}
	if ChaseWinProbability(easy) <= ChaseWinProbability(hard) {
		t.Error("easier chase should be more likely")
	}
	mid := MatchState{BattingTeam: "A", BowlingTeam: "B", Runs: 100, Wickets: 5, Overs: 10.0, TotalOvers: 20, Innings: 2, Target: intp(180)}
	if p := ChaseWinProbability(mid); p < 0.02 || p > 0.98 {
		t.Errorf("probability %v out of bounds [0.02, 0.98]", p)
	}
}

func TestWinProbabilityFirstInnings(t *testing.T) {
	s := MatchState{BattingTeam: "A", BowlingTeam: "B", Runs: 90, Wickets: 1, Overs: 8.0, TotalOvers: 20, Innings: 1}
	wp := WinProbability(s)
	if wp.Leader != "A" && wp.Leader != "B" {
		t.Errorf("leader = %q", wp.Leader)
	}
	if wp.Confidence < 0.5 {
		t.Errorf("confidence %v < 0.5", wp.Confidence)
	}
}

func TestExplainScore(t *testing.T) {
	chase := MatchState{BattingTeam: "Unicorns", BowlingTeam: "Freedom",
		Runs: 140, Wickets: 4, Overs: 16.2, TotalOvers: 20, Innings: 2, Target: intp(175)}
	text := ExplainScore(chase)
	for _, want := range []string{"Unicorns", "need 35 more", "22 balls"} {
		if !strings.Contains(text, want) {
			t.Errorf("chase explanation missing %q:\n%s", want, text)
		}
	}

	first := MatchState{BattingTeam: "Unicorns", BowlingTeam: "Freedom",
		Runs: 80, Wickets: 2, Overs: 10.0, TotalOvers: 20, Innings: 1}
	if !strings.Contains(ExplainScore(first), "Projection") {
		t.Error("first innings should mention projection")
	}

	won := MatchState{BattingTeam: "Unicorns", BowlingTeam: "Freedom",
		Runs: 176, Wickets: 6, Overs: 19.3, TotalOvers: 20, Innings: 2, Target: intp(175)}
	if !strings.Contains(ExplainScore(won), "have won") {
		t.Error("completed chase should say the batting team won")
	}
}

// Parity with the Python fit: same states must produce the same
// probabilities to 1e-3, so a Go-side drift breaks loudly.
//
// The four T20 innings-one values moved when t20-1 was refitted against real
// per-ground par (held-out log-loss
// 0.6030 -> 0.5967). The chase and ODI segments are untouched and their
// numbers are unchanged, which is the useful signal that only the intended
// segment moved. A fresh innings now comes from the measured bat-first base
// rate rather than the fitted line, which had no data at zero balls and
// extrapolated to 73%.
func TestWinProbabilityFittedParity(t *testing.T) {
	tgt := func(n int) *int { return &n }
	cases := []struct {
		name string
		s    MatchState
		want float64
	}{
		{"t20 inn1 80/2 @10", MatchState{Runs: 80, Wickets: 2, Overs: 10.0, TotalOvers: 20, Innings: 1}, 0.53},
		{"t20 inn1 fresh", MatchState{Runs: 0, Wickets: 0, Overs: 0.0, TotalOvers: 20, Innings: 1}, 0.482},
		{"t20 chase 100/3 @12 tgt171", MatchState{Runs: 100, Wickets: 3, Overs: 12.0, TotalOvers: 20, Innings: 2, Target: tgt(171)}, 0.4904},
		{"t20 chase 160/8 @18 tgt171", MatchState{Runs: 160, Wickets: 8, Overs: 18.0, TotalOvers: 20, Innings: 2, Target: tgt(171)}, 0.4950},
		{"odi inn1 180/4 @30", MatchState{Runs: 180, Wickets: 4, Overs: 30.0, TotalOvers: 50, Innings: 1}, 0.6258},
		{"odi chase 200/5 @35 tgt288", MatchState{Runs: 200, Wickets: 5, Overs: 35.0, TotalOvers: 50, Innings: 2, Target: tgt(288)}, 0.4800},
		{"elo: India bat first v Nepal", MatchState{BattingTeam: "India", BowlingTeam: "Nepal", Runs: 0, Wickets: 0, Overs: 0.0, TotalOvers: 20, Innings: 1}, 0.61},
		{"elo: Nepal bat first v India", MatchState{BattingTeam: "Nepal", BowlingTeam: "India", Runs: 0, Wickets: 0, Overs: 0.0, TotalOvers: 20, Innings: 1}, 0.356},
	}
	for _, c := range cases {
		got := WinProbability(c.s).BattingTeamWinProb
		if diff := got - c.want; diff > 0.001 || diff < -0.001 {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestMultiDayWinProbShape(t *testing.T) {
	// The three outcomes must be a distribution, and the model has to agree
	// with the Python fit on the cases used to sanity-check it there.
	cases := []struct {
		name          string
		s             MultiDayState
		wantTopIsBat  bool
		wantTopIsDraw bool
	}{
		{"400 ahead on day 4 with 6 wickets", MultiDayState{Lead: 400, WicketsInHand: 6, Innings: 3, FracLeft: 0.30}, true, false},
		{"chasing 120 with 8 wickets", MultiDayState{Lead: -119, WicketsInHand: 8, Innings: 4, FracLeft: 0.15, Needed: 120}, true, false},
	}
	for _, c := range cases {
		bat, field, draw := MultiDayWinProb(c.s)
		if sum := bat + field + draw; math.Abs(sum-1) > 1e-9 {
			t.Errorf("%s: probabilities sum to %v, want 1", c.name, sum)
		}
		for _, p := range []float64{bat, field, draw} {
			if p < 0 || p > 1 {
				t.Errorf("%s: probability out of range: %v", c.name, p)
			}
		}
		if c.wantTopIsBat && !(bat > field && bat > draw) {
			t.Errorf("%s: batting side should lead, got bat=%.2f field=%.2f draw=%.2f", c.name, bat, field, draw)
		}
	}
	// A hopeless position must favour the fielding side.
	bat, field, _ := MultiDayWinProb(MultiDayState{Lead: -300, WicketsInHand: 3, Innings: 4, FracLeft: 0.10, Needed: 301})
	if field <= bat {
		t.Errorf("300 behind with 3 wickets: field=%.2f should beat bat=%.2f", field, bat)
	}
}
