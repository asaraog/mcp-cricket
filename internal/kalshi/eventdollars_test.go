package kalshi

import "testing"

// Kalshi's 2026 API stopped populating the integer cent fields. ParseMarket
// and ParseEventsPage were migrated to the *_dollars strings; the nested
// event shape was missed, so every market under an event priced at 0 and the
// whole set looked untraded while two sides were quoting 55c and 44c.
func TestParseEventJSONReadsDollarFields(t *testing.T) {
	body := []byte(`{"event":{"title":"St. Lucia Kings vs St. Kitts and Nevis Patriots",
      "markets":[
        {"ticker":"KXCPLMATCH-X-SKN","yes_sub_title":"St. Kitts and Nevis Patriots",
         "status":"active","yes_bid":null,"yes_ask":null,"last_price":null,
         "yes_bid_dollars":"0.5500","yes_ask_dollars":"0.5600","last_price_dollars":"0.5500"},
        {"ticker":"KXCPLMATCH-X-STL","yes_sub_title":"St. Lucia Kings",
         "status":"active","yes_bid":null,"yes_ask":null,"last_price":null,
         "yes_bid_dollars":"0.4400","yes_ask_dollars":"0.4500","last_price_dollars":"0.4400"}]}}`)
	c, err := ParseEventJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(c) != 2 {
		t.Fatalf("got %d markets, want 2", len(c))
	}
	if c[0].Market.YesBid != 55 || c[0].Market.YesAsk != 56 {
		t.Errorf("SKN quotes = %d/%d, want 55/56", c[0].Market.YesBid, c[0].Market.YesAsk)
	}
	if c[1].Market.YesBid != 44 || c[1].Market.YesAsk != 45 {
		t.Errorf("STL quotes = %d/%d, want 44/45", c[1].Market.YesBid, c[1].Market.YesAsk)
	}
	// The mid, not zero, and not the last trade.
	if got := c[0].Market.ImpliedProb; got < 0.554 || got > 0.556 {
		t.Errorf("SKN implied = %v, want ~0.555 (the 55/56 mid)", got)
	}
	if c[0].Market.ImpliedProb == 0 || c[1].Market.ImpliedProb == 0 {
		t.Error("a market priced at zero: the dollars fields were not read")
	}
}

// The old integer form must still win when a deployment does populate it.
func TestParseEventJSONStillReadsIntegerCents(t *testing.T) {
	body := []byte(`{"event":{"title":"T","markets":[{"ticker":"A","status":"active",
	  "yes_bid":61,"yes_ask":62,"last_price":61}]}}`)
	c, err := ParseEventJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	if c[0].Market.YesBid != 61 || c[0].Market.YesAsk != 62 {
		t.Errorf("got %d/%d, want 61/62", c[0].Market.YesBid, c[0].Market.YesAsk)
	}
}
