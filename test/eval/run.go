//go:build ignore

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"time"
)

// --- Corpus types ---

type document struct {
	ID      string `json:"id"`
	Content string `json:"content"`
}

type query struct {
	ID          string   `json:"id"`
	Text        string   `json:"text"`
	RelevantIDs []string `json:"relevant_ids"`
}

type corpus struct {
	Documents []document `json:"documents"`
	Queries   []query    `json:"queries"`
}

// --- HTTP request/response types ---

type ingestRequest struct {
	Content string `json:"content"`
	Source  string `json:"source"`
}

// ingestResponse mirrors StoreResult (no json tags on the struct — PascalCase).
type ingestResponse struct {
	MemoryID      string `json:"MemoryID"`
	ChunksStored  int    `json:"ChunksStored"`
	ChunksDeduped int    `json:"ChunksDeduped"`
	Stored        bool   `json:"Stored"`
}

type retrieveRequest struct {
	Query string `json:"query"`
	K     int    `json:"k"`
}

// retrieveResult mirrors RetrieveResult (no json tags — PascalCase).
type retrieveResult struct {
	MemoryID  string  `json:"MemoryID"`
	ChunkID   string  `json:"ChunkID"`
	Content   string  `json:"Content"`
	Score     float64 `json:"Score"`
	Source    string  `json:"Source"`
	CreatedAt string  `json:"CreatedAt"`
}

// retrieveResponse mirrors RetrieveResponse (no json tags — PascalCase).
type retrieveResponse struct {
	Results []retrieveResult `json:"Results"`
}

func main() {
	baseURL := os.Getenv("ENGRAM_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}

	// 1. Load corpus.
	f, err := os.Open("test/eval/corpus.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "open corpus: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	var c corpus
	if err := json.NewDecoder(f).Decode(&c); err != nil {
		fmt.Fprintf(os.Stderr, "decode corpus: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Loaded corpus: %d documents, %d queries\n", len(c.Documents), len(c.Queries))

	client := &http.Client{Timeout: 30 * time.Second}

	// 2. Ingest all documents.
	fmt.Println("\nIngesting documents...")
	for i, doc := range c.Documents {
		reqBody, _ := json.Marshal(ingestRequest{
			Content: doc.Content,
			Source:  doc.ID,
		})
		resp, err := client.Post(baseURL+"/v1/memories", "application/json", bytes.NewReader(reqBody))
		if err != nil {
			fmt.Fprintf(os.Stderr, "ingest %s: %v\n", doc.ID, err)
			os.Exit(1)
		}
		var ir ingestResponse
		json.NewDecoder(resp.Body).Decode(&ir)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			fmt.Fprintf(os.Stderr, "ingest %s: HTTP %d\n", doc.ID, resp.StatusCode)
			os.Exit(1)
		}
		fmt.Printf("  [%d/%d] %s -> memory_id=%s chunks=%d\n", i+1, len(c.Documents), doc.ID, ir.MemoryID, ir.ChunksStored)
	}

	// Allow a brief moment for indexing to settle.
	time.Sleep(500 * time.Millisecond)

	// 3. Run queries and compute recall@5.
	const k = 5
	fmt.Printf("\nRunning %d queries (k=%d)...\n", len(c.Queries), k)

	type queryResult struct {
		id      string
		recall  float64
		latency time.Duration
	}
	results := make([]queryResult, 0, len(c.Queries))
	var latencies []time.Duration

	for _, q := range c.Queries {
		reqBody, _ := json.Marshal(retrieveRequest{Query: q.Text, K: k})

		start := time.Now()
		resp, err := client.Post(baseURL+"/v1/retrieve", "application/json", bytes.NewReader(reqBody))
		latency := time.Since(start)
		if err != nil {
			fmt.Fprintf(os.Stderr, "retrieve %s: %v\n", q.ID, err)
			os.Exit(1)
		}
		var rr retrieveResponse
		json.NewDecoder(resp.Body).Decode(&rr)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			fmt.Fprintf(os.Stderr, "retrieve %s: HTTP %d\n", q.ID, resp.StatusCode)
			os.Exit(1)
		}

		// Build set of returned sources.
		returnedSources := make(map[string]struct{}, len(rr.Results))
		for _, r := range rr.Results {
			if r.Source != "" {
				returnedSources[r.Source] = struct{}{}
			}
		}

		// recall = |relevant ∩ top-k| / |relevant|
		var hits int
		for _, relID := range q.RelevantIDs {
			if _, ok := returnedSources[relID]; ok {
				hits++
			}
		}
		recall := float64(hits) / float64(len(q.RelevantIDs))

		results = append(results, queryResult{id: q.ID, recall: recall, latency: latency})
		latencies = append(latencies, latency)
		fmt.Printf("  %s  recall=%.2f  latency=%dms  returned_sources=%v\n",
			q.ID, recall, latency.Milliseconds(), sourcesSlice(rr.Results))
	}

	// 4. Aggregate metrics.
	var totalRecall float64
	for _, r := range results {
		totalRecall += r.recall
	}
	overallRecall := totalRecall / float64(len(results))

	// Percentile latencies.
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p50 := percentile(latencies, 50)
	p95 := percentile(latencies, 95)

	fmt.Printf("\n--- Results ---\n")
	fmt.Printf("recall@%d : %.4f\n", k, overallRecall)
	fmt.Printf("p50 latency : %dms\n", p50.Milliseconds())
	fmt.Printf("p95 latency : %dms\n", p95.Milliseconds())
	fmt.Printf("threshold   : 0.85\n")

	if overallRecall >= 0.85 {
		fmt.Println("\nPASS: recall@5 >= 0.85")
		os.Exit(0)
	}
	fmt.Println("\nFAIL: recall@5 < 0.85")
	os.Exit(1)
}

// sourcesSlice returns a slice of non-empty Source values for display.
func sourcesSlice(results []retrieveResult) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		if r.Source != "" {
			out = append(out, r.Source)
		}
	}
	return out
}

// percentile returns the p-th percentile of a sorted duration slice.
func percentile(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := (p * len(sorted)) / 100
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
