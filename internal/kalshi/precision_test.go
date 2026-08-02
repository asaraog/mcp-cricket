package kalshi

import "testing"

func TestNoSharedWordFalseMatch(t *testing.T) {
	cands := []Candidate{{EventTitle: "South Delhi Superstarz vs East Delhi Riders", Market: Market{Ticker: "X", Title: "South Delhi Superstarz"}}}
	if _, _, ok := BestMatch(cands, "South Africa Emerging Players", "University Sport South Africa"); ok {
		t.Error("shared-word 'south' must not produce a market match")
	}
	lpl := []Candidate{{EventTitle: "Jaffna Kings vs Kandy Falcons", Market: Market{Ticker: "Y", Title: "Jaffna Kings"}}}
	if _, _, ok := BestMatch(lpl, "Kandy Royals", "Jaffna Kings"); !ok {
		t.Error("Kandy/Jaffna must still match on distinct city words")
	}
}
