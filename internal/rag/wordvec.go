package rag

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"embed"
	"strconv"
	"strings"
)

// In-process embeddings: pretrained GloVe word vectors (trimmed to our
// vocabulary + the 20K most common English words, gzipped, compiled into
// the binary). A text's embedding is the IDF-weighted average of its
// words' vectors — computed in microseconds with zero network calls.
//
// Quality ladder this slots into: transformer endpoint (best, needs an
// external service) > word vectors (synonyms & related words, in-process)
// > BM25 (exact keywords, always available). Prewarm picks the best
// available tier.

//go:embed data/vectors.gz
var vectorsFS embed.FS

var wordVecs map[string][]float64

func loadWordVecs() bool {
	raw, err := vectorsFS.ReadFile("data/vectors.gz")
	if err != nil || len(raw) == 0 {
		return false
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return false
	}
	defer gz.Close()
	vecs := map[string][]float64{}
	scanner := bufio.NewScanner(gz)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 {
			continue
		}
		v := make([]float64, len(fields)-1)
		ok := true
		for i, f := range fields[1:] {
			if v[i], err = strconv.ParseFloat(f, 64); err != nil {
				ok = false
				break
			}
		}
		if ok {
			vecs[fields[0]] = v
		}
	}
	if len(vecs) == 0 {
		return false
	}
	wordVecs = vecs
	return true
}

// wordvecEmbed embeds texts as IDF-weighted averages of word vectors.
// Unknown words are skipped; a text with no known words gets a zero
// vector (cosine 0 -> below floor -> BM25 fallback handles it).
func wordvecEmbed(texts []string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i, text := range texts {
		var acc []float64
		for _, tok := range Tokenize(text) {
			vec, ok := wordVecs[tok]
			if !ok {
				continue
			}
			// IDF from the BM25 index stats: rare corpus terms carry
			// more meaning than ubiquitous ones. Words unseen in the
			// corpus get a neutral weight of 1.
			weight := 1.0
			if df := idx.df[tok]; df > 0 {
				weight = 1.0 + float64(len(idx.docs))/float64(1+df)/10.0
			}
			if acc == nil {
				acc = make([]float64, len(vec))
			}
			for j := range vec {
				acc[j] += weight * vec[j]
			}
		}
		if acc == nil {
			acc = make([]float64, 50)
		}
		out[i] = acc
	}
	return out, nil
}
