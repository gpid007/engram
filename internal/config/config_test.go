package config

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleYAML = `
server:
  mcp_stdio: false
  http_addr: ":9090"

embedding:
  provider: ollama
  base_url: http://localhost:11434
  model: nomic-embed-text
  dim: 384
  batch: 16
  timeout_ms: 3000
  retries: 2

vector:
  provider: qdrant
  addr: localhost:6334
  collection: test-memories

meta:
  provider: postgres
  dsn: postgres://user:pass@localhost:5432/testdb?sslmode=disable

retrieval:
  vector_k: 10
  bm25_k: 10
  rerank_k: 10
  final_k: 3
  rrf_k: 30
  vector_floor: 0.3

rerank:
  enabled: false
  provider: none
  crossenc_model_path: /tmp/model.onnx
  llm_model: llama3.2:1b
  remote:
    base_url: ""
    api_key_env: ""
    model: ""
  timeout_ms: 1000
  max_candidates: 10

chunking:
  max_tokens: 256
  min_tokens: 50
  similarity_threshold: 0.7

logging:
  level: debug
  format: text
`

func writeTempYAML(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "config-*.yaml")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	f.Close()
	return f.Name()
}

func TestLoad_FromFile(t *testing.T) {
	path := writeTempYAML(t, sampleYAML)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// Server
	if cfg.Server.MCPStdio != false {
		t.Errorf("Server.MCPStdio = %v, want false", cfg.Server.MCPStdio)
	}
	if cfg.Server.HTTPAddr != ":9090" {
		t.Errorf("Server.HTTPAddr = %q, want :9090", cfg.Server.HTTPAddr)
	}

	// Embedding
	if cfg.Embedding.Dim != 384 {
		t.Errorf("Embedding.Dim = %d, want 384", cfg.Embedding.Dim)
	}
	if cfg.Embedding.Model != "nomic-embed-text" {
		t.Errorf("Embedding.Model = %q, want nomic-embed-text", cfg.Embedding.Model)
	}
	if cfg.Embedding.Batch != 16 {
		t.Errorf("Embedding.Batch = %d, want 16", cfg.Embedding.Batch)
	}

	// Vector
	if cfg.Vector.Addr != "localhost:6334" {
		t.Errorf("Vector.Addr = %q, want localhost:6334", cfg.Vector.Addr)
	}
	if cfg.Vector.Collection != "test-memories" {
		t.Errorf("Vector.Collection = %q, want test-memories", cfg.Vector.Collection)
	}

	// Meta
	if cfg.Meta.DSN != "postgres://user:pass@localhost:5432/testdb?sslmode=disable" {
		t.Errorf("Meta.DSN = %q, unexpected", cfg.Meta.DSN)
	}

	// Retrieval
	if cfg.Retrieval.FinalK != 3 {
		t.Errorf("Retrieval.FinalK = %d, want 3", cfg.Retrieval.FinalK)
	}
	if cfg.Retrieval.VectorFloor != 0.3 {
		t.Errorf("Retrieval.VectorFloor = %v, want 0.3", cfg.Retrieval.VectorFloor)
	}

	// Rerank
	if cfg.Rerank.Provider != "none" {
		t.Errorf("Rerank.Provider = %q, want none", cfg.Rerank.Provider)
	}
	if cfg.Rerank.Enabled != false {
		t.Errorf("Rerank.Enabled = %v, want false", cfg.Rerank.Enabled)
	}

	// Chunking
	if cfg.Chunking.MaxTokens != 256 {
		t.Errorf("Chunking.MaxTokens = %d, want 256", cfg.Chunking.MaxTokens)
	}
	if cfg.Chunking.SimilarityThreshold != 0.7 {
		t.Errorf("Chunking.SimilarityThreshold = %v, want 0.7", cfg.Chunking.SimilarityThreshold)
	}

	// Logging
	if cfg.Logging.Level != "debug" {
		t.Errorf("Logging.Level = %q, want debug", cfg.Logging.Level)
	}
	if cfg.Logging.Format != "text" {
		t.Errorf("Logging.Format = %q, want text", cfg.Logging.Format)
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	path := writeTempYAML(t, "embedding: [invalid: yaml: :")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestEnvOverride_EmbeddingModel(t *testing.T) {
	path := writeTempYAML(t, sampleYAML)

	t.Setenv("ENGRAM_EMBEDDING_MODEL", "foo")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Embedding.Model != "foo" {
		t.Errorf("Embedding.Model = %q, want foo", cfg.Embedding.Model)
	}
}

func TestEnvOverride_MetaDSN(t *testing.T) {
	path := writeTempYAML(t, sampleYAML)

	t.Setenv("ENGRAM_META_DSN", "postgres://override:override@db:5432/override")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Meta.DSN != "postgres://override:override@db:5432/override" {
		t.Errorf("Meta.DSN = %q, want override value", cfg.Meta.DSN)
	}
}

func TestEnvOverride_IntAndFloat(t *testing.T) {
	path := writeTempYAML(t, sampleYAML)

	t.Setenv("ENGRAM_EMBEDDING_DIM", "512")
	t.Setenv("ENGRAM_RETRIEVAL_VECTOR_FLOOR", "0.5")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Embedding.Dim != 512 {
		t.Errorf("Embedding.Dim = %d, want 512", cfg.Embedding.Dim)
	}
	if cfg.Retrieval.VectorFloor != 0.5 {
		t.Errorf("Retrieval.VectorFloor = %v, want 0.5", cfg.Retrieval.VectorFloor)
	}
}

func TestEnvOverride_Bool(t *testing.T) {
	path := writeTempYAML(t, sampleYAML)

	t.Setenv("ENGRAM_SERVER_MCP_STDIO", "true")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Server.MCPStdio {
		t.Errorf("Server.MCPStdio = %v, want true", cfg.Server.MCPStdio)
	}
}

func TestValidation_BadEmbeddingDim(t *testing.T) {
	path := writeTempYAML(t, sampleYAML)
	t.Setenv("ENGRAM_EMBEDDING_DIM", "0")

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for dim=0, got nil")
	}
}

func TestValidation_BadRerankProvider(t *testing.T) {
	path := writeTempYAML(t, sampleYAML)
	t.Setenv("ENGRAM_RERANK_PROVIDER", "bad")

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for bad rerank provider, got nil")
	}
}

func TestValidation_EmptyMetaDSN(t *testing.T) {
	path := writeTempYAML(t, sampleYAML)
	t.Setenv("ENGRAM_META_DSN", "   ")

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for empty meta.dsn, got nil")
	}
}

func TestValidation_EmptyVectorAddr(t *testing.T) {
	path := writeTempYAML(t, sampleYAML)
	t.Setenv("ENGRAM_VECTOR_ADDR", "   ")

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for empty vector.addr, got nil")
	}
}

func TestValidation_VectorFloorOutOfRange(t *testing.T) {
	path := writeTempYAML(t, sampleYAML)
	t.Setenv("ENGRAM_RETRIEVAL_VECTOR_FLOOR", "1.5")

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for vector_floor=1.5, got nil")
	}
}

func TestValidation_NegativeVectorFloor(t *testing.T) {
	path := writeTempYAML(t, sampleYAML)
	t.Setenv("ENGRAM_RETRIEVAL_VECTOR_FLOOR", "-0.1")

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for vector_floor=-0.1, got nil")
	}
}

func TestValidation_BadFinalK(t *testing.T) {
	path := writeTempYAML(t, sampleYAML)
	t.Setenv("ENGRAM_RETRIEVAL_FINAL_K", "0")

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for final_k=0, got nil")
	}
}

func TestDefaults_Valid(t *testing.T) {
	cfg := Defaults()
	if err := validate(cfg); err != nil {
		t.Fatalf("Defaults() failed validation: %v", err)
	}
}

func TestDefaults_Values(t *testing.T) {
	cfg := Defaults()

	if cfg.Embedding.Dim != 768 {
		t.Errorf("default Embedding.Dim = %d, want 768", cfg.Embedding.Dim)
	}
	if cfg.Embedding.Model != "nomic-embed-text" {
		t.Errorf("default Embedding.Model = %q, want nomic-embed-text", cfg.Embedding.Model)
	}
	if cfg.Retrieval.FinalK != 5 {
		t.Errorf("default Retrieval.FinalK = %d, want 5", cfg.Retrieval.FinalK)
	}
	if cfg.Retrieval.VectorFloor != 0.25 {
		t.Errorf("default Retrieval.VectorFloor = %v, want 0.25", cfg.Retrieval.VectorFloor)
	}
	if cfg.Rerank.Provider != "crossenc" {
		t.Errorf("default Rerank.Provider = %q, want crossenc", cfg.Rerank.Provider)
	}
	if cfg.Vector.Addr != "qdrant:6334" {
		t.Errorf("default Vector.Addr = %q, want qdrant:6334", cfg.Vector.Addr)
	}
}

func TestLoadOrDefaults_EmptyPath(t *testing.T) {
	cfg, err := LoadOrDefaults("")
	if err != nil {
		t.Fatalf("LoadOrDefaults(\"\") error = %v", err)
	}
	def := Defaults()
	if cfg.Embedding.Dim != def.Embedding.Dim {
		t.Errorf("LoadOrDefaults(\"\") Embedding.Dim = %d, want %d", cfg.Embedding.Dim, def.Embedding.Dim)
	}
}

func TestLoadOrDefaults_WithPath(t *testing.T) {
	path := writeTempYAML(t, sampleYAML)
	cfg, err := LoadOrDefaults(path)
	if err != nil {
		t.Fatalf("LoadOrDefaults(%q) error = %v", path, err)
	}
	if cfg.Embedding.Dim != 384 {
		t.Errorf("LoadOrDefaults with path: Embedding.Dim = %d, want 384", cfg.Embedding.Dim)
	}
}

func TestMultipleValidationErrors(t *testing.T) {
	path := writeTempYAML(t, sampleYAML)
	t.Setenv("ENGRAM_EMBEDDING_DIM", "0")
	t.Setenv("ENGRAM_RETRIEVAL_FINAL_K", "0")
	t.Setenv("ENGRAM_RERANK_PROVIDER", "bad")

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected multiple validation errors, got nil")
	}
}
