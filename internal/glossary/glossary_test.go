package glossary

import (
	"strings"
	"testing"
)

func TestLookup(t *testing.T) {
	e := Lookup("wicket")
	if e == nil || e.Term != "wicket" || !strings.Contains(e.Baseball, "out") {
		t.Errorf("wicket lookup: %+v", e)
	}
	if Lookup("  Yorker ") == nil {
		t.Error("case/whitespace-insensitive lookup failed")
	}
	if Lookup("required run") == nil {
		t.Error("substring lookup failed")
	}
	if Lookup("touchdown") != nil {
		t.Error("miss should return nil")
	}
}

func TestCheatSheetCoversAllTerms(t *testing.T) {
	sheet := CheatSheet()
	for term := range Terms {
		if !strings.Contains(sheet, "- "+term+" —") {
			t.Errorf("cheat sheet missing %q", term)
		}
	}
}

func TestCompactSheetCoversAllTerms(t *testing.T) {
	sheet := CompactSheet()
	for term := range Terms {
		if !strings.Contains(sheet, term+"=") {
			t.Errorf("compact sheet missing %q", term)
		}
	}
	if len(sheet) > len(CheatSheet())/2 {
		t.Error("compact sheet should be much smaller than the full sheet")
	}
}

func TestEveryEntryComplete(t *testing.T) {
	for term, e := range Terms {
		if e.Baseball == "" || e.Plain == "" {
			t.Errorf("%q has empty fields", term)
		}
	}
}
