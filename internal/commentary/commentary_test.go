package commentary

import "testing"

func terms(js []Jargon) map[string]bool {
	out := map[string]bool{}
	for _, j := range js {
		out[j.Term] = true
	}
	return out
}

func TestAnnotateRealLine(t *testing.T) {
	got := terms(Annotate("Full, on middle and leg, 78mph, worked into midwicket"))
	for _, want := range []string{"full", "middle and leg", "worked", "midwicket"} {
		if !got[want] {
			t.Errorf("missing %q in %v", want, got)
		}
	}
}

func TestLongestMatchWins(t *testing.T) {
	got := terms(Annotate("thick outside edge through to point"))
	if !got["outside edge"] {
		t.Error("expected 'outside edge'")
	}
	if got["edge"] {
		t.Error("bare 'edge' should not appear")
	}
	if !got["point"] {
		t.Error("expected 'point'")
	}

	got = terms(Annotate("Punched into the covers off the front foot"))
	if !got["the covers"] {
		t.Error("longest match 'the covers' should beat 'covers'")
	}
	if got["covers"] || got["cover"] {
		t.Error("shorter cover terms should be claimed by 'the covers'")
	}
}

func TestCaseInsensitive(t *testing.T) {
	if len(Annotate("YORKER at the death")) == 0 {
		t.Error("uppercase jargon should match")
	}
}

func TestNoJargon(t *testing.T) {
	if got := Annotate("what a great day for it"); len(got) != 0 {
		t.Errorf("expected no matches, got %v", got)
	}
	if got := Annotate(""); len(got) != 0 {
		t.Errorf("empty text: got %v", got)
	}
}
