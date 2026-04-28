package chunk

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// mockEmbedder returns a deterministic vector per (sentence-text -> vector) entry
// or, failing that, a fallback vector keyed by the call-order index.
type mockEmbedder struct {
	dim      int
	byText   map[string][]float32
	fallback [][]float32 // indexed in call order across one Embed() call
}

func (m *mockEmbedder) embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		if v, ok := m.byText[t]; ok {
			out[i] = append([]float32(nil), v...)
			continue
		}
		if i < len(m.fallback) {
			out[i] = append([]float32(nil), m.fallback[i]...)
			continue
		}
		// Default: orthogonal-ish unit vector based on i.
		v := make([]float32, m.dim)
		v[i%m.dim] = 1
		out[i] = v
	}
	return out, nil
}

func (m *mockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	return m.embed(ctx, texts)
}

func (m *mockEmbedder) EmbedQuery(ctx context.Context, texts []string) ([][]float32, error) {
	return m.embed(ctx, texts)
}

func (m *mockEmbedder) Dim() int { return m.dim }

// vec produces a length-`dim` vector with 1.0 at position `idx`.
func vec(dim, idx int) []float32 {
	v := make([]float32, dim)
	if idx >= 0 && idx < dim {
		v[idx] = 1
	}
	return v
}

// padSentence repeats `word` n times to control token count.
func padSentence(word string, n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = word
	}
	return strings.Join(parts, " ") + "."
}

func TestChunker_EmptyInput(t *testing.T) {
	mock := &mockEmbedder{dim: 4}
	c := NewChunker(mock, Config{MaxTokens: 100, MinTokens: 1, SimilarityThreshold: 0.5})
	chunks, err := c.Chunk(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) != 0 {
		t.Fatalf("expected 0 chunks, got %d", len(chunks))
	}
}

func TestChunker_WhitespaceOnly(t *testing.T) {
	mock := &mockEmbedder{dim: 4}
	c := NewChunker(mock, Config{MaxTokens: 100, MinTokens: 1, SimilarityThreshold: 0.5})
	chunks, err := c.Chunk(context.Background(), "   \n\t   ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) != 0 {
		t.Fatalf("expected 0 chunks, got %d", len(chunks))
	}
}

func TestChunker_SingleSentence(t *testing.T) {
	mock := &mockEmbedder{dim: 4}
	c := NewChunker(mock, Config{MaxTokens: 100, MinTokens: 1, SimilarityThreshold: 0.5})
	in := "This is the only sentence."
	chunks, err := c.Chunk(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Content != in {
		t.Fatalf("content mismatch: got %q want %q", chunks[0].Content, in)
	}
	if len(chunks[0].EmbVec) != mock.dim {
		t.Fatalf("expected embedding of dim %d, got %d", mock.dim, len(chunks[0].EmbVec))
	}
}

func TestChunker_ShortInputBelowMinTokensStaysSingle(t *testing.T) {
	// Two sentences with low cosine similarity — but total tokens are below MinTokens,
	// so the merge step should collapse them into a single chunk.
	dim := 4
	mock := &mockEmbedder{
		dim: dim,
		byText: map[string][]float32{
			"Cats sleep.": vec(dim, 0),
			"Dogs run.":   vec(dim, 1), // orthogonal -> cosine 0 -> would split
		},
	}
	c := NewChunker(mock, Config{MaxTokens: 100, MinTokens: 100, SimilarityThreshold: 0.6})
	chunks, err := c.Chunk(context.Background(), "Cats sleep. Dogs run.")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk after merge, got %d (%v)", len(chunks), chunks)
	}
	want := "Cats sleep. Dogs run."
	if chunks[0].Content != want {
		t.Fatalf("content mismatch: got %q want %q", chunks[0].Content, want)
	}
}

func TestChunker_LongMonologueOneTopic(t *testing.T) {
	// All sentences embed to the same vector -> cosine 1.0 -> never split on similarity.
	dim := 4
	sentences := []string{
		"Alpha alpha alpha alpha alpha.",
		"Beta beta beta beta beta.",
		"Gamma gamma gamma gamma gamma.",
		"Delta delta delta delta delta.",
		"Epsilon epsilon epsilon epsilon epsilon.",
	}
	byText := map[string][]float32{}
	for _, s := range sentences {
		byText[s] = vec(dim, 0)
	}
	mock := &mockEmbedder{dim: dim, byText: byText}

	c := NewChunker(mock, Config{MaxTokens: 1000, MinTokens: 1, SimilarityThreshold: 0.5})
	chunks, err := c.Chunk(context.Background(), strings.Join(sentences, " "))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk for monotopic input, got %d", len(chunks))
	}
}

func TestChunker_MixedTopicSplits(t *testing.T) {
	dim := 4
	// Two clearly distinct topics: first 3 sentences along axis 0, next 3 along axis 1.
	topicA := []string{
		"alpha one alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha.",
		"alpha two alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha.",
		"alpha three alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha alpha.",
	}
	topicB := []string{
		"beta one beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta.",
		"beta two beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta.",
		"beta three beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta beta.",
	}
	byText := map[string][]float32{}
	for _, s := range topicA {
		byText[s] = vec(dim, 0)
	}
	for _, s := range topicB {
		byText[s] = vec(dim, 1)
	}
	mock := &mockEmbedder{dim: dim, byText: byText}

	c := NewChunker(mock, Config{MaxTokens: 10000, MinTokens: 10, SimilarityThreshold: 0.6})
	all := strings.Join(append(append([]string{}, topicA...), topicB...), " ")
	chunks, err := c.Chunk(context.Background(), all)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks at topic boundary, got %d", len(chunks))
	}
	if !strings.Contains(chunks[0].Content, "alpha one") || !strings.Contains(chunks[1].Content, "beta one") {
		t.Fatalf("chunks did not split at topic boundary: %+v", chunks)
	}
}

func TestChunker_MaxTokensEnforced(t *testing.T) {
	// All sentences are same topic (cosine 1.0) but each is large; MaxTokens must still split.
	dim := 4
	// 10-word sentences ending in lowercase >3-char words to avoid abbreviation suppression.
	sentences := make([]string, 6)
	byText := map[string][]float32{}
	words := []string{"alphax", "betaxx", "gammax", "deltax", "thetax", "kappax"}
	for i := range sentences {
		s := padSentence(words[i], 10)
		sentences[i] = s
		byText[s] = vec(dim, 0) // identical vectors -> high similarity
	}
	mock := &mockEmbedder{dim: dim, byText: byText}

	// MaxTokens=25 -> after 2 sentences (20 tokens), adding a 3rd (10) would exceed -> split.
	c := NewChunker(mock, Config{MaxTokens: 25, MinTokens: 1, SimilarityThreshold: 0.5})
	chunks, err := c.Chunk(context.Background(), strings.Join(sentences, " "))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected MaxTokens to force at least 2 chunks, got %d", len(chunks))
	}
	// Verify each chunk respects MaxTokens roughly (each chunk has at most 2 sentences = 20 tokens).
	for i, ch := range chunks {
		toks := tokenCount(ch.Content)
		if toks > 30 {
			t.Errorf("chunk %d token count %d unexpectedly large", i, toks)
		}
	}
}

func TestChunker_MinTokensMergeForward(t *testing.T) {
	// Three sentences: tiny+huge+huge. With low MinTokens for huge, the tiny one merges forward.
	dim := 4
	tiny := "Hi."                                 // 1 token
	huge := padSentence("filler", 50) + " End."   // ~51 tokens
	huge2 := padSentence("topic", 50) + " Done."  // ~51 tokens
	byText := map[string][]float32{
		tiny:  vec(dim, 0),
		huge:  vec(dim, 1), // orthogonal -> would split off tiny
		huge2: vec(dim, 1),
	}
	mock := &mockEmbedder{dim: dim, byText: byText}

	// MinTokens 10 forces merge of the 1-token chunk forward.
	c := NewChunker(mock, Config{MaxTokens: 1000, MinTokens: 10, SimilarityThreshold: 0.6})
	in := tiny + " " + huge + " " + huge2
	chunks, err := c.Chunk(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Without merging we'd have 2 chunks ([tiny],[huge,huge2]). After merging: still 1 effective merge -> 1 chunk total.
	if len(chunks) == 0 {
		t.Fatalf("got 0 chunks")
	}
	for i, ch := range chunks {
		toks := tokenCount(ch.Content)
		if toks < 10 && len(chunks) > 1 {
			t.Errorf("chunk %d still under MinTokens (%d) after merge", i, toks)
		}
	}
	// And the tiny content must appear in some chunk.
	found := false
	for _, ch := range chunks {
		if strings.Contains(ch.Content, "Hi.") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("tiny sentence lost during merge: %+v", chunks)
	}
}

func TestChunker_MinTokensMergeBackwardLast(t *testing.T) {
	// Last chunk is tiny -> merges backward into previous.
	dim := 4
	huge := padSentence("filler", 50) + " End."
	huge2 := padSentence("topic", 50) + " Done."
	tiny := "Bye."
	byText := map[string][]float32{
		huge:  vec(dim, 0),
		huge2: vec(dim, 0),
		tiny:  vec(dim, 1), // forces split off as its own chunk
	}
	mock := &mockEmbedder{dim: dim, byText: byText}
	c := NewChunker(mock, Config{MaxTokens: 1000, MinTokens: 10, SimilarityThreshold: 0.6})
	in := huge + " " + huge2 + " " + tiny
	chunks, err := c.Chunk(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) != 1 {
		// Could be 1 (full merge) — what matters is tiny is not alone.
		for _, ch := range chunks {
			if strings.TrimSpace(ch.Content) == tiny {
				t.Fatalf("tiny last sentence remained alone: %+v", chunks)
			}
		}
	}
}

func TestChunker_PunctuationOnlyEdgeCase(t *testing.T) {
	mock := &mockEmbedder{dim: 4}
	c := NewChunker(mock, Config{MaxTokens: 100, MinTokens: 1, SimilarityThreshold: 0.5})
	chunks, err := c.Chunk(context.Background(), "... ?! ...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should not panic; output may be empty or a single chunk.
	for _, ch := range chunks {
		if ch.Content == "" {
			t.Errorf("empty chunk content emitted")
		}
	}
}

func TestChunker_Deterministic(t *testing.T) {
	dim := 4
	in := "First sentence about cats. Second sentence about cats. Third sentence about dogs. Fourth sentence about dogs."
	mock1 := &mockEmbedder{
		dim: dim,
		byText: map[string][]float32{
			"First sentence about cats.":   vec(dim, 0),
			"Second sentence about cats.":  vec(dim, 0),
			"Third sentence about dogs.":   vec(dim, 1),
			"Fourth sentence about dogs.":  vec(dim, 1),
		},
	}
	mock2 := &mockEmbedder{
		dim:    dim,
		byText: mock1.byText,
	}

	c1 := NewChunker(mock1, Config{MaxTokens: 1000, MinTokens: 1, SimilarityThreshold: 0.6})
	c2 := NewChunker(mock2, Config{MaxTokens: 1000, MinTokens: 1, SimilarityThreshold: 0.6})

	a, err := c1.Chunk(context.Background(), in)
	if err != nil {
		t.Fatalf("err1: %v", err)
	}
	b, err := c2.Chunk(context.Background(), in)
	if err != nil {
		t.Fatalf("err2: %v", err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("non-deterministic output:\n a=%+v\n b=%+v", a, b)
	}
}

func TestSplitSentences(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"single no terminator", "hello world", []string{"hello world"}},
		{"single period", "Hello world.", []string{"Hello world."}},
		{"two periods", "Hello world. Goodbye world.", []string{"Hello world.", "Goodbye world."}},
		{"question and exclamation", "Really? Yes! Okay.", []string{"Really?", "Yes!", "Okay."}},
		{"ellipsis", "Wait... what happened?", []string{"Wait...", "what happened?"}},
		{"abbreviation short", "Dr. Smith arrived. He was late.", []string{"Dr. Smith arrived.", "He was late."}},
		{"abbreviation allcaps", "She works at NASA. Then she left.", []string{"She works at NASA. Then she left."}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitSentences(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("splitSentences(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestCosine(t *testing.T) {
	tests := []struct {
		name string
		a, b []float32
		want float64
	}{
		{"identical unit", []float32{1, 0, 0}, []float32{1, 0, 0}, 1.0},
		{"orthogonal", []float32{1, 0, 0}, []float32{0, 1, 0}, 0.0},
		{"opposite", []float32{1, 0, 0}, []float32{-1, 0, 0}, -1.0},
		{"zero vec", []float32{0, 0, 0}, []float32{1, 0, 0}, 0.0},
		{"length mismatch", []float32{1, 0}, []float32{1, 0, 0}, 0.0},
		{"empty", nil, nil, 0.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cosine(tt.a, tt.b)
			diff := got - tt.want
			if diff < -1e-6 || diff > 1e-6 {
				t.Errorf("cosine(%v,%v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestTokenCount(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"hello", 1},
		{"hello world", 2},
		{"hello, world!", 2},
		{"one two three four", 4},
		{"one. two. three.", 3},
		{"   ", 0},
		{"a1b 2c", 2},
	}
	for _, tt := range tests {
		got := tokenCount(tt.in)
		if got != tt.want {
			t.Errorf("tokenCount(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestMeanVec(t *testing.T) {
	got := meanVec([][]float32{{1, 2, 3}, {3, 4, 5}})
	want := []float32{2, 3, 4}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("meanVec = %v, want %v", got, want)
	}
	if meanVec(nil) != nil {
		t.Errorf("meanVec(nil) should be nil")
	}
}
