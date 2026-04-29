package cli

import (
	"context"
	"fmt"
	"strings"
)

// RunConfig holds the dependencies for CLI command handlers.
type RunConfig struct {
	Client *Client
	Parser Parser
	UserID string
}

// RunPut parses raw text into structured facts via the local model and stores each one.
// It continues storing remaining facts even if one fails, returning the first error.
func RunPut(ctx context.Context, rc RunConfig, raw string) error {
	facts, err := rc.Parser.Parse(ctx, raw)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	var firstErr error
	for _, f := range facts {
		resp, err := rc.Client.Store(ctx, f.Content, f.Source, f.Tags)
		if err != nil {
			fmt.Printf("error storing: %s: %v\n", truncate(f.Content, 50), err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		status := "stored"
		if !resp.Stored {
			status = "deduped"
		}
		short := resp.MemoryID
		if len(short) > 8 {
			short = short[:8]
		}
		fmt.Printf("%s: %s [%s] id=%s\n", status, truncate(f.Content, 50), strings.Join(f.Tags, " "), short)
	}
	return firstErr
}

// RunGet retrieves memories matching a query and prints them.
func RunGet(ctx context.Context, rc RunConfig, query string, k int) error {
	resp, err := rc.Client.Retrieve(ctx, query, k, true)
	if err != nil {
		return fmt.Errorf("retrieve: %w", err)
	}
	for _, r := range resp.Results {
		fmt.Printf("%.3f  %s\n", r.Score, truncate(r.Content, 80))
	}
	fmt.Printf("retrieved %d results in %dms\n", len(resp.Results), resp.Stats.TotalMS)
	return nil
}

// RunStatus prints the user's memory stats and server health.
func RunStatus(ctx context.Context, rc RunConfig) error {
	state, err := rc.Client.State(ctx)
	if err != nil {
		return fmt.Errorf("state: %w", err)
	}
	fmt.Printf("memories: %d  chunks: %d\n", state.MemoryCount, state.ChunkCount)
	fmt.Printf("last:     %s\n", state.LastMemory)
	if len(state.TopSources) > 0 {
		fmt.Printf("sources:  %s\n", strings.Join(state.TopSources, ", "))
	}
	return nil
}

// truncate shortens s to at most n runes, appending "..." if truncated.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
