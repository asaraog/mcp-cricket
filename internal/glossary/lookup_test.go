package glossary

import "testing"

func TestLookupWordBoundaries(t *testing.T) {
	cases := []struct{ q, wantTerm string }{
		{"cover", "cover"},
		{"deep midwicket", "midwicket"},
		{"net run rate", "net run rate"},
		{"yorker?", "yorker"},
	}
	for _, c := range cases {
		e := Lookup(c.q)
		if e == nil || e.Term != c.wantTerm {
			got := "<nil>"
			if e != nil {
				got = e.Term
			}
			t.Errorf("Lookup(%q) = %s, want %s", c.q, got, c.wantTerm)
		}
	}
	if e := Lookup("discover"); e != nil && e.Term == "over" {
		t.Errorf("substring leak: discover -> over")
	}
}

func TestFindInText(t *testing.T) {
	if e := FindInText("WICKET WICKET WICKET what does it mean"); e == nil || e.Term != "wicket" {
		t.Errorf("FindInText failed for shouted wicket")
	}
	if e := FindInText("what does net run rate mean for the table"); e == nil || e.Term != "net run rate" {
		t.Errorf("FindInText should prefer longest term")
	}
}
