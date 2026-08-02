package rag

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// Standard BM25 parameters.
const (
	k1 = 1.2
	b  = 0.75
)

var stopwords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true,
	"be": true, "by": true, "can": true, "do": true, "does": true, "for": true,
	"how": true, "i": true, "in": true, "is": true, "it": true, "its": true,
	"me": true, "my": true, "of": true, "on": true, "or": true, "that": true,
	"the": true, "this": true, "to": true, "was": true, "what": true,
	"when": true, "which": true, "why": true, "will": true, "with": true,
	"you": true, "your": true,
}

// compounds canonicalizes cricket terms people type as one word while
// the corpus spells them hyphenated/spaced (or vice versa) — "noball",
// "no ball" and "no-ball" must index and search as the same token.
// Applied inside Tokenize, so docs and queries stay symmetric.
var compounds = strings.NewReplacer(
	"no-ball", "noball", "no ball", "noball",
	"free-hit", "freehit", "free hit", "freehit",
	"run-out", "runout", "run out", "runout",
	"leg-bye", "legbye", "leg bye", "legbye",
	"power-play", "powerplay", "power play", "powerplay",
)

// Tokenize lowercases and splits on non-alphanumerics, dropping stopwords.
func Tokenize(s string) []string {
	fields := strings.FieldsFunc(compounds.Replace(strings.ToLower(s)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := fields[:0]
	for _, f := range fields {
		if len(f) > 1 && !stopwords[f] {
			// Light stem: long tokens ending in y fold to i, so name
			// transliteration variants meet ("Krishnamurthy" queries find
			// "SP Krishnamurthi"). Symmetric on docs and queries.
			if len(f) >= 6 && strings.HasSuffix(f, "y") {
				f = f[:len(f)-1] + "i"
			}
			out = append(out, f)
		}
	}
	return out
}

type indexedDoc struct {
	doc    Doc
	tf     map[string]int
	length int
}

type index struct {
	docs   []indexedDoc
	df     map[string]int
	avgLen float64
}

var idx = buildIndex(Corpus())

func buildIndex(docs []Doc) *index {
	ix := &index{df: map[string]int{}}
	total := 0
	for _, d := range docs {
		tokens := Tokenize(d.Title + " " + d.Title + " " + d.Text) // title counted twice
		id := indexedDoc{doc: d, tf: map[string]int{}, length: len(tokens)}
		for _, t := range tokens {
			id.tf[t]++
		}
		for t := range id.tf {
			ix.df[t]++
		}
		total += id.length
		ix.docs = append(ix.docs, id)
	}
	if len(ix.docs) > 0 {
		ix.avgLen = float64(total) / float64(len(ix.docs))
	}
	return ix
}

// Result is one retrieved chunk with its relevance score.
type Result struct {
	Doc   Doc     `json:"doc"`
	Score float64 `json:"score"`
}

// lexicalSearch is BM25 with a relevance floor — an irrelevant question
// retrieves nothing rather than noise.
func lexicalSearch(query string, k int) []Result {
	terms := Tokenize(query)
	if len(terms) == 0 {
		return nil
	}
	n := float64(len(idx.docs))
	var results []Result
	for _, d := range idx.docs {
		score := 0.0
		for _, t := range terms {
			tf := float64(d.tf[t])
			if tf == 0 {
				continue
			}
			df := float64(idx.df[t])
			idf := math.Log(1 + (n-df+0.5)/(df+0.5))
			norm := 1 - b + b*float64(d.length)/idx.avgLen
			score += idf * (tf * (k1 + 1)) / (tf + k1*norm)
		}
		if score > 1.0 {
			results = append(results, Result{Doc: d.doc, Score: score})
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if len(results) > k {
		results = results[:k]
	}
	return results
}

// semanticSearch ranks docs by cosine similarity to the embedded query.
func semanticSearch(query string, k int) []Result {
	vecMu.RLock()
	vecs := docVecs
	on := semanticOn
	embed := embedFn
	floor := semFloor
	vecMu.RUnlock()
	if !on {
		return nil
	}
	qv, err := embed([]string{query})
	if err != nil || len(qv) != 1 {
		return nil
	}
	var results []Result
	for i, d := range idx.docs {
		if sim := cosine(qv[0], vecs[i]); sim > floor {
			results = append(results, Result{Doc: d.doc, Score: sim})
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if len(results) > k {
		results = results[:k]
	}
	return results
}

// nameTokens: pre-tokenized player-name sets aligned with idx.docs (nil
// for non-player docs) — the corpus holds ~10k players, so tokenizing
// titles per query would be wasteful.
var (
	nameTokensOnce sync.Once
	nameTokens     []map[string]bool
)

func buildNameTokens() {
	nameTokens = make([]map[string]bool, len(idx.docs))
	for i, d := range idx.docs {
		if !strings.HasPrefix(d.doc.ID, "player-") {
			continue
		}
		set := map[string]bool{}
		name := strings.SplitN(d.doc.Title, "(", 2)[0]
		for _, t := range Tokenize(name) {
			set[t] = true // stemmed form
		}
		for _, t := range strings.Fields(strings.ToLower(name)) {
			set["="+strings.Trim(t, ".,")] = true // exact surface form
		}
		nameTokens[i] = set
	}
}

// titleMatches retrieves player docs whose NAME contains an exact query
// token (len >= 4): deterministic-before-fuzzy for proper nouns — player
// names are out-of-vocabulary for the word vectors, so fuzzy search alone
// would never surface them. Scoped to names only: matching on ordinary
// title words (or team names) would let common tokens hijack the ranking.
func titleMatches(query string, limit int) []Result {
	nameTokensOnce.Do(buildNameTokens)
	qt := Tokenize(query)
	var hits []Result
	for i, d := range idx.docs {
		name := nameTokens[i]
		if name == nil {
			continue
		}
		score := 0.0
		for _, t := range qt {
			if len(t) >= 4 && name[t] {
				score++
			}
		}
		// Exact-spelling matches outrank stem-only matches, so the real
		// "Krishnamurthi" beats a "Krishnamurthy" who stems to the same
		// token.
		for _, t := range strings.Fields(compounds.Replace(strings.ToLower(query))) {
			t = strings.Trim(t, "?!.,'s")
			if len(t) >= 4 && !stopwords[t] && name["="+t] {
				score += 2
			}
		}
		if score > 0 {
			hits = append(hits, Result{Doc: d.doc, Score: score})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if hits[i].Doc.Weight != hits[j].Doc.Weight {
			return hits[i].Doc.Weight > hits[j].Doc.Weight
		}
		return hits[i].Doc.ID < hits[j].Doc.ID
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

var (
	searchCacheMu sync.Mutex
	searchCache   = map[string][]Result{}
	searchOrder   []string
)

const searchCacheMax = 512

// cachedSearch memoizes retrieval by (query, k). The corpus is embedded
// and immutable, so a hit is always correct — and repeated questions are
// the norm ("what is a yorker?"), which keeps the cosine work off the
// CPU under concurrency.
func cachedSearch(query string, k int, compute func() []Result) []Result {
	key := strconv.Itoa(k) + "|" + strings.ToLower(strings.TrimSpace(query))
	searchCacheMu.Lock()
	if hits, ok := searchCache[key]; ok {
		searchCacheMu.Unlock()
		return hits
	}
	searchCacheMu.Unlock()

	hits := compute()

	searchCacheMu.Lock()
	if _, exists := searchCache[key]; !exists {
		searchCache[key] = hits
		searchOrder = append(searchOrder, key)
		if len(searchOrder) > searchCacheMax {
			delete(searchCache, searchOrder[0])
			searchOrder = searchOrder[1:]
		}
	}
	searchCacheMu.Unlock()
	return hits
}

// Search is standard RAG retrieval: embed the query, rank corpus chunks by
// cosine similarity, return top-k. Embeddings catch paraphrases that share
// no vocabulary with the corpus ("the batter blocked it with his pads, is
// that out?" -> the LBW doc). Exact title-token hits (player names, term
// titles) are ranked ahead of the fuzzy results, and BM25 keyword search
// fills in when no embedding tier is available — retrieval never has a
// hard dependency on an external service.
func Search(query string, k int) []Result {
	return cachedSearch(query, k, func() []Result { return search(query, k) })
}

func search(query string, k int) []Result {
	out := titleMatches(query, 2)
	seen := map[string]bool{}
	for _, r := range out {
		seen[r.Doc.ID] = true
	}
	fill := func(results []Result) {
		for _, r := range results {
			if len(out) >= k {
				return
			}
			if !seen[r.Doc.ID] {
				seen[r.Doc.ID] = true
				out = append(out, r)
			}
		}
	}
	if SemanticReady() {
		fill(semanticSearch(query, k))
	}
	if len(out) < k {
		fill(lexicalSearch(query, k))
	}
	return out
}
