package explainer

import "testing"

// Bangladesh led Australia by 153 in the second innings of a Test and the
// shipped model gave them 15% while Kalshi had 54%. The cause was the
// innings term: "pos" carried one flat elo, so innings 1 — which holds the
// most states and the least reason to discount a lead — set the rating
// penalty for all three innings. The archive holds 28 states of a weak side
// leading in the second innings, so that case was pure extrapolation.
func TestWeakSideWithALeadIsNotWrittenOff(t *testing.T) {
	e := MultiDayEloEdge("Bangladesh", "Australia")
	if e > -0.5 {
		t.Skipf("elo table changed; edge is %.2f, this test assumes a big gap", e)
	}
	s := MultiDayState{Innings: 2, Lead: 153, WicketsInHand: 4, FracLeft: 0.64,
		IsTest: true, EloEdge: e}
	bat, _, _ := MultiDayWinProb(s)
	if bat < 0.35 {
		t.Errorf("weak side leading by 153 in innings 2 priced at %.0f%%; "+
			"the market had 54%% and an even-rated side in that position wins 83%%", bat*100)
	}
	if bat > 0.80 {
		t.Errorf("priced at %.0f%%: the rating gap should still count for something", bat*100)
	}
}

// The rating discount must vary by innings, which is the whole point of the
// refit: the same position must not score identically in innings 2 and 3.
func TestRatingDiscountVariesByInnings(t *testing.T) {
	e := MultiDayEloEdge("Bangladesh", "Australia")
	base := MultiDayState{Lead: 153, WicketsInHand: 4, FracLeft: 0.64, IsTest: true, EloEdge: e}
	base.Innings = 2
	b2, _, _ := MultiDayWinProb(base)
	base.Innings = 3
	b3, _, _ := MultiDayWinProb(base)
	if d := b2 - b3; d < 0.05 && d > -0.05 {
		t.Errorf("innings 2 (%.0f%%) and 3 (%.0f%%) score the same; the "+
			"per-innings terms are not doing anything", b2*100, b3*100)
	}
}

// A strong side with a big lead must stay a heavy favourite, and the three
// outcomes must always sum to one.
func TestMultiDaySanity(t *testing.T) {
	s := MultiDayState{Innings: 2, Lead: 250, WicketsInHand: 6, FracLeft: 0.6,
		IsTest: true, EloEdge: MultiDayEloEdge("Australia", "Bangladesh")}
	b, f, d := MultiDayWinProb(s)
	if b < 0.6 {
		t.Errorf("strong side 250 ahead priced at %.0f%%", b*100)
	}
	for _, st := range []MultiDayState{
		{Innings: 1, Lead: 0, WicketsInHand: 10, FracLeft: 1, IsTest: true},
		{Innings: 3, Lead: -200, WicketsInHand: 2, FracLeft: 0.3, IsTest: true},
		{Innings: 4, Needed: 120, WicketsInHand: 6, FracLeft: 0.2, IsTest: true},
	} {
		b, f, d := MultiDayWinProb(st)
		if sum := b + f + d; sum < 0.99 || sum > 1.01 {
			t.Errorf("outcomes sum to %.3f for %+v", sum, st)
		}
	}
	_ = d
	_ = f
}
