package matchup

import "testing"

func TestLookupResolvesFullNames(t *testing.T) {
	// ESPN feeds say "Virat Kohli"/"Jasprit Bumrah"; the table stores
	// Cricsheet's "V Kohli"/"JJ Bumrah" (verified pair: 110 balls).
	r, ok := Lookup("t20", "Virat Kohli", "Jasprit Bumrah")
	if !ok {
		t.Fatal("famous pair should resolve from full names")
	}
	if r.Balls < 100 || r.Runs < 100 {
		t.Fatalf("implausible numbers: %+v", r)
	}
	if r.Format != "t20" {
		t.Fatalf("format = %q", r.Format)
	}
}

func TestLookupShortNamesToo(t *testing.T) {
	if _, ok := Lookup("t20", "V Kohli", "Rashid Khan"); !ok {
		t.Fatal("cricsheet-style names should resolve")
	}
}

func TestLookupUnknownFailsQuiet(t *testing.T) {
	if _, ok := Lookup("t20", "Nobody Realman", "Also Fake"); ok {
		t.Fatal("unknown pair must return ok=false")
	}
}

func TestLine(t *testing.T) {
	r := Result{Format: "t20", Balls: 21, Runs: 27, Outs: 2}
	if got, want := r.Line("Fletcher", "Pierre"), "Fletcher vs Pierre: 27 off 21, out 2x"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if got, want := r.Line("Virat Kohli", "Jasprit Bumrah"), "Kohli vs Bumrah: 27 off 21, out 2x"; got != want {
		t.Fatalf("full names should shorten: got %q want %q", got, want)
	}
}
