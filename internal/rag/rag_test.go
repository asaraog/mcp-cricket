package rag

import (
	"strings"
	"testing"
)

func TestTokenize(t *testing.T) {
	got := Tokenize("What is the required run-rate?!")
	want := []string{"required", "run", "rate"}
	if len(got) != len(want) {
		t.Fatalf("tokens = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("token[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCorpusNonEmptyAndUnique(t *testing.T) {
	docs := Corpus()
	if len(docs) < 60 {
		t.Fatalf("corpus suspiciously small: %d docs", len(docs))
	}
	seen := map[string]bool{}
	for _, d := range docs {
		if d.ID == "" || d.Text == "" {
			t.Errorf("incomplete doc: %+v", d)
		}
		if seen[d.ID] {
			t.Errorf("duplicate doc id %q", d.ID)
		}
		seen[d.ID] = true
	}
}

func retrievedIDs(query string, k int) []string {
	var ids []string
	for _, r := range Search(query, k) {
		ids = append(ids, r.Doc.ID)
	}
	return ids
}

func TestSearchRelevance(t *testing.T) {
	cases := map[string]string{ // question -> doc id that must be in top 3
		"can a test match end in a draw?":          "test-draw",
		"how long does a test match last":          "test-length",
		"what is lbw":                              "gloss-lbw",
		"what is a yorker":                         "gloss-yorker",
		"how do kalshi contracts work":             "kalshi-mechanics",
		"what does the required run rate mean":     "rrr",
		"what happens if it rains":                 "dls",
		"what is a super over tie":                 "super-over",
		"what is major league cricket":             "mlc",
		"convert moneyline to implied probability": "moneyline",
	}
	for query, wantID := range cases {
		ids := retrievedIDs(query, 3)
		found := false
		for _, id := range ids {
			if id == wantID {
				found = true
			}
		}
		if !found {
			t.Errorf("query %q: want %q in top 3, got %v", query, wantID, ids)
		}
	}
}

func TestCosine(t *testing.T) {
	if c := cosine([]float64{1, 0}, []float64{1, 0}); c < 0.999 {
		t.Errorf("identical vectors: %v", c)
	}
	if c := cosine([]float64{1, 0}, []float64{0, 1}); c > 0.001 {
		t.Errorf("orthogonal vectors: %v", c)
	}
	if c := cosine([]float64{1}, []float64{1, 2}); c != 0 {
		t.Errorf("mismatched dims should be 0: %v", c)
	}
}

// fakeSemantic wires a deterministic embedder: the query vector matches one
// chosen doc exactly and nothing else.
func fakeSemantic(t *testing.T, favoredID string) func() {
	t.Helper()
	vecs := make([][]float64, len(idx.docs))
	favored := -1
	for i, d := range idx.docs {
		vecs[i] = []float64{0, 1}
		if d.doc.ID == favoredID {
			favored = i
			vecs[i] = []float64{1, 0}
		}
	}
	if favored == -1 {
		t.Fatalf("doc %q not in corpus", favoredID)
	}
	oldFn := embedFn
	embedFn = func(texts []string) ([][]float64, error) {
		out := make([][]float64, len(texts))
		for i := range texts {
			out[i] = []float64{1, 0} // every query "means" the favored doc
		}
		return out, nil
	}
	vecMu.Lock()
	oldVecs, oldOn := docVecs, semanticOn
	docVecs, semanticOn = vecs, true
	vecMu.Unlock()
	return func() {
		embedFn = oldFn
		vecMu.Lock()
		docVecs, semanticOn = oldVecs, oldOn
		vecMu.Unlock()
	}
}

func TestSemanticSearchSurfacesParaphrase(t *testing.T) {
	// A paraphrase with no lexical overlap with the LBW doc.
	query := "the batter blocked the delivery with his pads in front of the stumps"
	restore := fakeSemantic(t, "gloss-lbw")
	defer restore()

	ids := retrievedIDs(query, 3)
	if len(ids) == 0 || ids[0] != "gloss-lbw" {
		t.Errorf("semantic search should rank the lbw doc first, got %v", ids)
	}
}

func TestDegradesToBM25WithoutEmbedder(t *testing.T) {
	if SemanticReady() {
		t.Skip("embedder configured in this environment")
	}
	// Identical behavior to lexicalSearch when no embedder is set. Both
	// yorker docs (glossary + commentary term) are correct top answers.
	ids := retrievedIDs("what is a yorker", 3)
	if len(ids) == 0 || !strings.Contains(ids[0], "yorker") {
		t.Errorf("BM25 fallback broken: %v", ids)
	}
}

func TestWordVectorsInProcess(t *testing.T) {
	if !loadWordVecs() {
		t.Skip("vectors.gz not generated yet (run cmd/trimvec)")
	}
	vecs, err := wordvecEmbed([]string{"the bowler throws fast", "the pitcher throws fast", "banana smoothie recipe"})
	if err != nil {
		t.Fatal(err)
	}
	// GloVe should place bowler-text nearer pitcher-text than banana-text.
	simPitcher := cosine(vecs[0], vecs[1])
	simBanana := cosine(vecs[0], vecs[2])
	if simPitcher <= simBanana {
		t.Errorf("expected bowler~pitcher (%.3f) > bowler~banana (%.3f)", simPitcher, simBanana)
	}
	// Unknown-words-only text embeds to a zero vector (falls back to BM25).
	zv, _ := wordvecEmbed([]string{"qqqxyzzy zzzyxqq"})
	if cosine(zv[0], vecs[0]) != 0 {
		t.Error("unknown-word text should produce zero vector")
	}
}

func TestIrrelevantQueryRetrievesLittle(t *testing.T) {
	res := Search("purple elephant quantum blockchain", 4)
	if len(res) > 0 {
		var titles []string
		for _, r := range res {
			titles = append(titles, r.Doc.Title)
		}
		t.Errorf("irrelevant query retrieved: %s", strings.Join(titles, "; "))
	}
}

func TestPlayerStatsSearchable(t *testing.T) {
	// Player names are OOV for the word vectors; the exact-title-token
	// pass must surface them regardless of the active embedding tier.
	res := Search("how is Krishnamurthi doing?", 4)
	if len(res) == 0 || !strings.Contains(res[0].Doc.ID, "krishnamurthi") {
		t.Fatalf("player doc should rank first, got %+v", res)
	}
	if !strings.Contains(res[0].Doc.Text, "strike rate") {
		t.Errorf("player doc has no stats: %s", res[0].Doc.Text)
	}
}

func TestCompoundSpellings(t *testing.T) {
	// "noball", "no ball" and "no-ball" must retrieve the same docs.
	for _, q := range []string{"what is a noball", "what is a no-ball", "what is a no ball"} {
		res := Search(q, 3)
		if len(res) == 0 || !strings.Contains(res[0].Doc.ID, "no-ball") {
			t.Errorf("%q should retrieve the no-ball doc first, got %+v", q, res)
		}
	}
}

func TestSurnameCollisionPrefersProminence(t *testing.T) {
	// "kohli" matches several players; career volume must break the tie,
	// not alphabetical order (which picked a Germany-women "A Kohli").
	res := Search("how good is kohli in t20", 3)
	if len(res) == 0 || res[0].Doc.ID != "player-v-kohli" {
		ids := []string{}
		for _, r := range res {
			ids = append(ids, r.Doc.ID)
		}
		t.Fatalf("want player-v-kohli first, got %v", ids)
	}
}
