package history

import (
	"testing"
)

// The archive numbers overs from 1, and the phase bounds counted from 0:
// a powerplay split was overs 1 to 5 and an over 0 no row has, the middle
// split began at the 6th, and the 20th over was in no phase. In the
// fixture's 2024 T20 World Cup final Kohli's 62 rows (three wides, then
// 59 balls, two runs a ball for the first 17) run from the 1st over into
// the 11th: the powerplay is his first 36 rows, 33 balls for 50, and the
// middle overs the other 26 balls for 26, where he was out. Pant's two
// balls are the 5th and 6th of the 20th over, Rohit's five the 19th, all
// off Maharaj.
func TestPhaseSplitsCountTheOversAsStored(t *testing.T) {
	if !Enabled() {
		t.Fatal("precondition: the fixture enables history")
	}
	bat, bowl, ok := PhaseStats("V Kohli", 20)
	if !ok || len(bowl) != 0 {
		t.Fatalf("Kohli: ok=%v, bowling %+v", ok, bowl)
	}
	want := []PhaseSplit{
		{Phase: "powerplay (1-6)", Balls: 33, Runs: 50},
		{Phase: "middle (7-15)", Balls: 26, Runs: 26, Outs: 1},
	}
	if len(bat) != len(want) {
		t.Fatalf("Kohli batting: %+v, want %+v", bat, want)
	}
	for i := range want {
		if bat[i] != want[i] {
			t.Errorf("Kohli %s: %+v, want %+v", want[i].Phase, bat[i], want[i])
		}
	}
	bat, _, ok = PhaseStats("RR Pant", 20)
	if !ok || len(bat) != 1 || bat[0] != (PhaseSplit{Phase: "death (16-20)", Balls: 2, Outs: 1}) {
		t.Errorf("Pant's 20th over: ok=%v, %+v", ok, bat)
	}
	_, bowl, ok = PhaseStats("K Maharaj", 20)
	if !ok {
		t.Fatal("Maharaj has no phases")
	}
	var death *PhaseSplit
	for i := range bowl {
		if bowl[i].Phase == "death (16-20)" {
			death = &bowl[i]
		}
	}
	if death == nil || death.Wickets != 2 {
		t.Errorf("Maharaj at the death, Rohit in the 19th and Pant in the 20th: %+v", bowl)
	}
}

// No phase queries over 0, and together they cover every over of the
// innings, the last one included.
func TestPhaseBoundsAreTheOversNumbered(t *testing.T) {
	for _, total := range []int{20, 50} {
		next := 1
		for _, p := range phaseBounds(total) {
			if p.from != next || p.to < p.from {
				t.Errorf("%d overs, %s: %d-%d, want from %d", total, p.name, p.from, p.to, next)
			}
			next = p.to + 1
		}
		if next != total+1 {
			t.Errorf("%d overs: the phases end at over %d", total, next-1)
		}
	}
}
