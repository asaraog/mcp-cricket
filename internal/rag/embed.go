package rag

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Semantic layer: optional embeddings via any OpenAI-compatible /embeddings
// endpoint (Gemini's compat layer, Ollama, OpenAI, Jina...). Configured by
// env; when absent, retrieval gracefully degrades to pure BM25 — the server
// never depends on an embedding provider being up.
//
//	EMBED_BASE_URL  e.g. https://generativelanguage.googleapis.com/v1beta/openai
//	EMBED_API_KEY
//	EMBED_MODEL     e.g. text-embedding-004 | nomic-embed-text

// embedFn is swappable in tests.
var embedFn = apiEmbed

type embedConfig struct {
	baseURL, apiKey, model string
}

func embedCfg() (embedConfig, bool) {
	base := os.Getenv("EMBED_BASE_URL")
	if base == "" {
		return embedConfig{}, false
	}
	return embedConfig{
		baseURL: base,
		apiKey:  os.Getenv("EMBED_API_KEY"),
		model:   os.Getenv("EMBED_MODEL"),
	}, true
}

func apiEmbed(texts []string) ([][]float64, error) {
	cfg, ok := embedCfg()
	if !ok {
		return nil, fmt.Errorf("no embedding endpoint configured")
	}
	payload, err := json.Marshal(map[string]any{"model": cfg.model, "input": texts})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost,
		strings.TrimRight(cfg.baseURL, "/")+"/embeddings", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.apiKey)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embeddings: HTTP %s", resp.Status)
	}
	var d struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return nil, err
	}
	if len(d.Data) != len(texts) {
		return nil, fmt.Errorf("embeddings: got %d vectors for %d texts", len(d.Data), len(texts))
	}
	out := make([][]float64, len(d.Data))
	for i := range d.Data {
		out[i] = d.Data[i].Embedding
	}
	return out, nil
}

var (
	vecMu      sync.RWMutex
	docVecs    [][]float64 // parallel to idx.docs; nil until built
	semanticOn bool
	semMode    = "bm25" // "transformer" | "wordvec" | "bm25"
	semFloor   = 0.35   // cosine relevance floor; embedder-dependent
)

// Prewarm activates the best available retrieval tier in the background:
// transformer endpoint (if EMBED_* configured) > in-process word vectors
// (compiled into the binary) > BM25 keyword search (always on).
func Prewarm() {
	go buildVectors()
}

func buildVectors() {
	texts := make([]string, len(idx.docs))
	for i, d := range idx.docs {
		texts[i] = d.doc.Title + ". " + d.doc.Text
	}

	if _, ok := embedCfg(); ok {
		vecs := make([][]float64, 0, len(texts))
		const batch = 50
		failed := false
		for start := 0; start < len(texts) && !failed; start += batch {
			end := start + batch
			if end > len(texts) {
				end = len(texts)
			}
			part, err := apiEmbed(texts[start:end])
			if err != nil {
				failed = true
				break
			}
			vecs = append(vecs, part...)
		}
		if !failed {
			vecMu.Lock()
			embedFn, docVecs, semanticOn, semMode, semFloor = apiEmbed, vecs, true, "transformer", 0.35
			vecMu.Unlock()
			return
		}
	}

	// In-process fallback: GloVe word vectors from the embedded data file.
	if loadWordVecs() {
		vecs, err := wordvecEmbed(texts)
		if err != nil {
			return
		}
		vecMu.Lock()
		// Averaged word vectors run higher cosine baselines than
		// transformer embeddings; raise the floor accordingly.
		embedFn, docVecs, semanticOn, semMode, semFloor = wordvecEmbed, vecs, true, "wordvec", 0.55
		vecMu.Unlock()
	}
}

// SemanticReady reports whether a vector tier is active.
func SemanticReady() bool {
	vecMu.RLock()
	defer vecMu.RUnlock()
	return semanticOn
}

// Mode names the active retrieval tier: transformer | wordvec | bm25.
func Mode() string {
	vecMu.RLock()
	defer vecMu.RUnlock()
	return semMode
}

func cosine(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
