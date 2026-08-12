package cricinfo

import "testing"

// ESPN reports some innings as "(N balls)" rather than in overs. Converting
// that with a hardcoded six turns 30 balls of a Hundred innings into 5.0
// overs, when it is 6.0 five-ball sets — and every ball count derived from
// it downstream is then wrong.
func TestBallsScoreConvertsPerFormat(t *testing.T) {
	cases := []struct {
		score string
		bpo   int
		want  float64
	}{
		{"120/4 (30 balls)", 5, 6.0},  // The Hundred: 30 balls = 6 sets
		{"120/4 (30 balls)", 6, 5.0},  // elsewhere: 30 balls = 5 overs
		{"120/4 (32 balls)", 5, 6.2},  // 6 sets and 2 balls
		{"120/4 (32 balls)", 6, 5.2},  // 5 overs and 2 balls
	}
	for _, c := range cases {
		_, _, overs, ok := ParseScoreStringBPO(c.score, c.bpo)
		if !ok || overs == nil {
			t.Fatalf("%q bpo=%d: did not parse", c.score, c.bpo)
		}
		if *overs != c.want {
			t.Errorf("%q bpo=%d: got %.1f, want %.1f", c.score, c.bpo, *overs, c.want)
		}
	}
}

// The old exported name must keep its six-ball meaning for existing callers.
func TestParseScoreStringStillAssumesSix(t *testing.T) {
	_, _, overs, ok := ParseScoreString("120/4 (30 balls)")
	if !ok || overs == nil || *overs != 5.0 {
		t.Errorf("ParseScoreString changed behaviour: %v", overs)
	}
}
