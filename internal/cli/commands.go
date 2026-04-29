package cli

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// RunConfig holds the dependencies for CLI command handlers.
type RunConfig struct {
	Client *Client
	Parser Parser
	UserID string
}

// RunAdd parses raw text (or ingests a file/directory) and stores facts.
// If dryRun is true, prints what would be stored without making API calls.
func RunAdd(ctx context.Context, rc RunConfig, raw, filePath, dirPath string, dryRun bool) error {
	switch {
	case dirPath != "":
		return runAddDir(ctx, rc, dirPath, dryRun)
	case filePath != "":
		return runAddFile(ctx, rc, filePath, dryRun)
	default:
		if raw == "" {
			return fmt.Errorf("provide text, -f <file>, or -d <dir>")
		}
		return runAddText(ctx, rc, raw, dryRun)
	}
}

func runAddText(ctx context.Context, rc RunConfig, raw string, dryRun bool) error {
	facts, err := rc.Parser.Parse(ctx, raw)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	// Warn if parser fell back to NopParser (Ollama unavailable).
	if len(facts) == 1 && len(facts[0].Tags) == 1 && facts[0].Tags[0] == "untagged" {
		slog.Warn("ollama unavailable — storing raw text as single untagged fact")
	}
	var firstErr error
	for _, f := range facts {
		if dryRun {
			fmt.Printf("[dry-run] would store: %s [%s]\n", truncate(f.Content, 60), strings.Join(f.Tags, " "))
			continue
		}
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

func runAddFile(ctx context.Context, rc RunConfig, path string, dryRun bool) error {
	text, err := ReadFileText(path)
	if err != nil {
		return err
	}
	paragraphs := SplitParagraphs(text)
	name := filepath.Base(path)
	fmt.Printf("ingesting: %s — %d paragraphs\n", name, len(paragraphs))
	stored := 0
	for i, para := range paragraphs {
		if dryRun {
			fmt.Printf("  [dry-run] paragraph %d/%d: %s\n", i+1, len(paragraphs), truncate(para, 60))
			continue
		}
		facts, err := rc.Parser.Parse(ctx, para)
		if err != nil {
			slog.Warn("parse failed for paragraph", "index", i+1, "err", err)
			continue
		}
		if len(facts) == 1 && len(facts[0].Tags) == 1 && facts[0].Tags[0] == "untagged" {
			slog.Warn("ollama unavailable — storing paragraph as untagged fact", "index", i+1)
		}
		for _, f := range facts {
			resp, err := rc.Client.Store(ctx, f.Content, f.Source, f.Tags)
			if err != nil {
				slog.Warn("store failed", "content", truncate(f.Content, 40), "err", err)
				continue
			}
			if resp.Stored {
				stored++
			}
		}
		fmt.Printf("  [%d/%d] %s: %d facts\n", i+1, len(paragraphs), name, stored)
	}
	if !dryRun {
		fmt.Printf("file complete: %s — %d facts stored from %d paragraphs\n", name, stored, len(paragraphs))
	}
	return nil
}

func runAddDir(ctx context.Context, rc RunConfig, dir string, dryRun bool) error {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".txt" || ext == ".md" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk dir %q: %w", dir, err)
	}
	fmt.Printf("found %d files in %s\n", len(files), dir)
	totalStored := 0
	for _, f := range files {
		before := totalStored
		if err := runAddFile(ctx, rc, f, dryRun); err != nil {
			slog.Warn("file ingest error", "file", f, "err", err)
		}
		_ = before
	}
	if !dryRun {
		fmt.Printf("directory complete: %s\n", dir)
	}
	return nil
}

// RunFind retrieves memories matching a query and prints them with IDs.
func RunFind(ctx context.Context, rc RunConfig, query string, k int) error {
	resp, err := rc.Client.Retrieve(ctx, query, k, true)
	if err != nil {
		return fmt.Errorf("retrieve: %w", err)
	}
	for _, r := range resp.Results {
		short := r.MemoryID
		if len(short) > 8 {
			short = short[:8]
		}
		fmt.Printf("%s  %.3f  %s\n", short, r.Score, truncate(r.Content, 80))
	}
	fmt.Printf("retrieved %d results in %dms\n", len(resp.Results), resp.Stats.TotalMs)
	return nil
}

// RunRemove deletes memories by ID or by semantic query.
// If dryRun is true, prints what would be deleted without deleting.
func RunRemove(ctx context.Context, rc RunConfig, ids []string, query string, force, dryRun bool) error {
	if len(ids) > 0 {
		for _, id := range ids {
			if dryRun {
				fmt.Printf("[dry-run] would remove: %s\n", id)
				continue
			}
			if err := rc.Client.DeleteMemory(ctx, id); err != nil {
				fmt.Printf("error removing %s: %v\n", id, err)
			} else {
				fmt.Printf("removed: %s\n", truncate(id, 8))
			}
		}
		return nil
	}

	if query == "" {
		return fmt.Errorf("provide memory IDs or --query")
	}

	resp, err := rc.Client.Retrieve(ctx, query, 20, false)
	if err != nil {
		return fmt.Errorf("find candidates: %w", err)
	}
	if len(resp.Results) == 0 {
		fmt.Printf("no memories found for query: %s\n", query)
		return nil
	}

	fmt.Printf("found %d memories:\n", len(resp.Results))
	for _, r := range resp.Results {
		short := r.MemoryID
		if len(short) > 8 {
			short = short[:8]
		}
		fmt.Printf("  %s  %.3f  %s\n", short, r.Score, truncate(r.Content, 60))
	}

	if dryRun {
		fmt.Printf("[dry-run] would delete %d memories\n", len(resp.Results))
		return nil
	}

	if !force {
		fmt.Printf("delete %d memories? [y/N] ", len(resp.Results))
		scanner := bufio.NewReader(os.Stdin)
		answer, _ := scanner.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" {
			fmt.Println("aborted")
			return nil
		}
	}

	removed := 0
	for _, r := range resp.Results {
		if err := rc.Client.DeleteMemory(ctx, r.MemoryID); err != nil {
			fmt.Printf("error removing %s: %v\n", truncate(r.MemoryID, 8), err)
		} else {
			fmt.Printf("removed: %s\n", truncate(r.MemoryID, 8))
			removed++
		}
	}
	fmt.Printf("removed %d memories\n", removed)
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
