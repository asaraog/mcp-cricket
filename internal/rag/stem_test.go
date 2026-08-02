package rag

import "testing"

func TestNameVariantRetrieval(t *testing.T) {
	hits := Search("who is Sanjay Krishnamurthy?", 3)
	found := false
	for _, h := range hits {
		if h.Doc.Title == "SP Krishnamurthi (San Francisco Unicorns)" || len(h.Doc.Title) > 0 && h.Doc.Title[:4] == "SP K" {
			found = true
		}
	}
	if !found {
		for _, h := range hits {
			t.Logf("hit: %s", h.Doc.Title)
		}
		t.Error("Krishnamurthy query should retrieve SP Krishnamurthi")
	}
}
