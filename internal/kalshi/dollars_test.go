package kalshi

import "testing"

// Kalshi's 2026 payload dropped the integer-cent price fields for decimal
// *_dollars strings; the parser read zeros and the market column vanished
// from every win table. This is the real August 2026 shape.
func TestParseEventsPageDollarsFormat(t *testing.T) {
	body := []byte(`{"events":[{"event_ticker":"KXHUNDREDMATCH-26AUG061330MLOLON",
	 "title":"London Spirit vs MI London","markets":[
	  {"ticker":"T1","title":"London Spirit wins","yes_sub_title":"London Spirit",
	   "status":"active","yes_bid_dollars":"0.1800","yes_ask_dollars":"0.2000",
	   "last_price_dollars":"0.2000"}]}],"cursor":""}`)
	cs, _, err := ParseEventsPage(body)
	if err != nil || len(cs) != 1 {
		t.Fatalf("parse: %v, %d candidates", err, len(cs))
	}
	m := cs[0].Market
	if m.YesBid != 18 || m.YesAsk != 20 {
		t.Errorf("bid/ask = %d/%d, want 18/20", m.YesBid, m.YesAsk)
	}
	if m.ImpliedProb < 0.18 || m.ImpliedProb > 0.20 {
		t.Errorf("implied = %.3f, want ~0.19", m.ImpliedProb)
	}
}

// The old integer fields must still win when present, so a rollback on
// Kalshi's side changes nothing here.
func TestCentsPrefersLegacyFields(t *testing.T) {
	if got := centsOf(56, "0.9900"); got != 56 {
		t.Errorf("centsOf legacy = %d, want 56", got)
	}
	if got := centsOf(0, "0.5600"); got != 56 {
		t.Errorf("centsOf dollars = %d, want 56", got)
	}
}
