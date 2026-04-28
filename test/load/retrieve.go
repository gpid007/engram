//go:build ignore

// Load test for the Engram retrieve endpoint.
//
// Usage:
//
//	go run test/load/retrieve.go [--url http://localhost:8080] [--concurrency 100] [--duration 30s] [--seed-first]
//
// Thresholds (no rerank):
//
//	p95 < 150ms  → PASS
//
// Notes on other modes (not tested here):
//
//	With crossenc rerank: p95 < 250ms
//	With LLM rerank:      p95 < 1200ms
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

var (
	flagURL         = flag.String("url", "http://localhost:8080", "Engram base URL")
	flagConcurrency = flag.Int("concurrency", 100, "Number of concurrent workers")
	flagDuration    = flag.Duration("duration", 30*time.Second, "Duration of the load test")
	flagSeedFirst   = flag.Bool("seed-first", true, "POST seed documents before load testing")
)

// seedDocuments are diverse documents posted before the load test.
var seedDocuments = []string{
	"The Go programming language was created at Google by Robert Griesemer, Rob Pike, and Ken Thompson.",
	"Machine learning is a subset of artificial intelligence that enables systems to learn from data.",
	"PostgreSQL is a powerful, open-source relational database management system.",
	"Kubernetes orchestrates containerized applications across clusters of machines.",
	"The Raft consensus algorithm is used to manage replicated state machines in distributed systems.",
	"Redis is an in-memory data structure store used as a database, cache, and message broker.",
	"Retrieval-Augmented Generation (RAG) combines search with large language models.",
	"Vector databases store high-dimensional embeddings for fast similarity search.",
	"Hybrid search combines dense vector retrieval with sparse keyword-based BM25 ranking.",
	"Prometheus is an open-source monitoring and alerting toolkit for cloud-native environments.",
	"gRPC is a high-performance RPC framework developed at Google using Protocol Buffers.",
	"The CAP theorem states that distributed systems can provide only two of consistency, availability, and partition tolerance.",
	"Cosine similarity measures the angle between two vectors and is commonly used in NLP.",
	"Transformer models use self-attention mechanisms to process sequential data in parallel.",
	"The Unix philosophy encourages building small, composable tools that do one thing well.",
	"OpenTelemetry provides vendor-neutral observability for distributed systems.",
	"Content-addressable storage deduplicates data by keying on its cryptographic hash.",
	"Reciprocal Rank Fusion (RRF) combines ranked lists from multiple retrieval systems.",
	"Embeddings are dense numerical representations of text, images, or other data.",
	"HNSW (Hierarchical Navigable Small World) is an efficient approximate nearest neighbour algorithm.",
}

// retrieveQueries are rotated across workers during the load test.
var retrieveQueries = []string{
	"golang distributed systems",
	"machine learning embeddings",
	"vector database similarity search",
	"PostgreSQL full text search",
	"Kubernetes container orchestration",
	"hybrid retrieval BM25 RRF",
	"RAG large language models",
	"Prometheus monitoring alerting",
	"consensus algorithm Raft",
	"transformer self-attention NLP",
}

func main() {
	flag.Parse()

	if *flagSeedFirst {
		if err := seed(*flagURL); err != nil {
			fmt.Fprintf(os.Stderr, "seed error: %v\n", err)
			os.Exit(1)
		}
	}

	latencies, total, errors := run(*flagURL, *flagConcurrency, *flagDuration)

	rps := float64(total) / flagDuration.Seconds()
	errRate := 0.0
	if total > 0 {
		errRate = float64(errors) / float64(total) * 100
	}

	p50, p95, p99 := percentiles(latencies)

	fmt.Printf("\n--- Load Test Results ---\n")
	fmt.Printf("Duration:        %v\n", *flagDuration)
	fmt.Printf("Concurrency:     %d\n", *flagConcurrency)
	fmt.Printf("Total requests:  %d\n", total)
	fmt.Printf("Requests/sec:    %.2f\n", rps)
	fmt.Printf("Error rate:      %.2f%%\n", errRate)
	fmt.Printf("Latency p50:     %.2f ms\n", float64(p50)/float64(time.Millisecond))
	fmt.Printf("Latency p95:     %.2f ms\n", float64(p95)/float64(time.Millisecond))
	fmt.Printf("Latency p99:     %.2f ms\n", float64(p99)/float64(time.Millisecond))
	fmt.Printf("\n--- Threshold Check (no rerank) ---\n")

	// Threshold: p95 < 150ms
	const thresholdP95 = 150 * time.Millisecond
	if p95 < thresholdP95 {
		fmt.Printf("p95 < 150ms: PASS (%.2f ms)\n", float64(p95)/float64(time.Millisecond))
		os.Exit(0)
	} else {
		fmt.Printf("p95 < 150ms: FAIL (%.2f ms >= 150ms)\n", float64(p95)/float64(time.Millisecond))
		os.Exit(1)
	}
}

// seed posts 20 diverse documents to the /v1/memories endpoint.
func seed(baseURL string) error {
	fmt.Printf("Seeding %d documents...\n", len(seedDocuments))
	client := &http.Client{Timeout: 15 * time.Second}
	for i, doc := range seedDocuments {
		body, _ := json.Marshal(map[string]any{
			"content": doc,
			"user_id": "loadtest",
			"source":  "load-test-seed",
		})
		resp, err := client.Post(baseURL+"/v1/memories", "application/json", bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("seed doc %d: %w", i, err)
		}
		io.Copy(io.Discard, resp.Body) //nolint
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			return fmt.Errorf("seed doc %d: HTTP %d", i, resp.StatusCode)
		}
	}
	fmt.Printf("Seeding complete.\n")
	return nil
}

// run executes the load test and returns sorted latency samples, total requests, and error count.
func run(baseURL string, concurrency int, duration time.Duration) ([]time.Duration, int64, int64) {
	var (
		total  int64
		errors int64
		mu     sync.Mutex
		all    []time.Duration
	)

	deadline := time.Now().Add(duration)
	client := &http.Client{Timeout: 10 * time.Second}

	var wg sync.WaitGroup
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			local := make([]time.Duration, 0, 128)
			for i := 0; ; i++ {
				if time.Now().After(deadline) {
					break
				}
				query := retrieveQueries[i%len(retrieveQueries)]
				lat, err := retrieve(client, baseURL, query)
				atomic.AddInt64(&total, 1)
				if err != nil {
					atomic.AddInt64(&errors, 1)
				} else {
					local = append(local, lat)
				}
			}
			mu.Lock()
			all = append(all, local...)
			mu.Unlock()
		}(w)
	}
	wg.Wait()

	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	return all, atomic.LoadInt64(&total), atomic.LoadInt64(&errors)
}

// retrieve sends a single POST /v1/retrieve and returns the round-trip latency.
func retrieve(client *http.Client, baseURL, query string) (time.Duration, error) {
	body, _ := json.Marshal(map[string]any{
		"query":   query,
		"user_id": "loadtest",
		"k":       5,
		"rerank":  false,
	})
	start := time.Now()
	resp, err := client.Post(baseURL+"/v1/retrieve", "application/json", bytes.NewReader(body))
	lat := time.Since(start)
	if err != nil {
		return lat, err
	}
	io.Copy(io.Discard, resp.Body) //nolint
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return lat, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return lat, nil
}

// percentiles returns p50, p95, p99 from an already-sorted slice.
func percentiles(sorted []time.Duration) (p50, p95, p99 time.Duration) {
	n := len(sorted)
	if n == 0 {
		return 0, 0, 0
	}
	idx := func(pct float64) time.Duration {
		i := int(float64(n-1) * pct / 100.0)
		return sorted[i]
	}
	return idx(50), idx(95), idx(99)
}
