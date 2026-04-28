// Package config loads and validates YAML configuration with env overrides.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ServerConfig holds transport settings.
type ServerConfig struct {
	MCPStdio bool   `yaml:"mcp_stdio"`
	HTTPAddr string `yaml:"http_addr"`
}

// EmbeddingConfig holds embedder settings.
type EmbeddingConfig struct {
	Provider  string `yaml:"provider"`
	BaseURL   string `yaml:"base_url"`
	Model     string `yaml:"model"`
	Dim       int    `yaml:"dim"`
	Batch     int    `yaml:"batch"`
	TimeoutMS int    `yaml:"timeout_ms"`
	Retries   int    `yaml:"retries"`
}

// VectorConfig holds vector store settings.
type VectorConfig struct {
	Provider   string `yaml:"provider"`
	Addr       string `yaml:"addr"`
	Collection string `yaml:"collection"`
}

// MetaConfig holds metadata store settings.
type MetaConfig struct {
	Provider string `yaml:"provider"`
	DSN      string `yaml:"dsn"`
}

// RetrievalConfig holds retrieval tuning.
type RetrievalConfig struct {
	VectorK     int     `yaml:"vector_k"`
	BM25K       int     `yaml:"bm25_k"`
	RerankK     int     `yaml:"rerank_k"`
	FinalK      int     `yaml:"final_k"`
	RRFK        int     `yaml:"rrf_k"`
	VectorFloor float64 `yaml:"vector_floor"`
}

// RemoteRerankerConfig holds settings for the remote rerank API.
type RemoteRerankerConfig struct {
	BaseURL   string `yaml:"base_url"`
	APIKeyEnv string `yaml:"api_key_env"`
	Model     string `yaml:"model"`
}

// RerankConfig holds reranker settings.
type RerankConfig struct {
	Enabled           bool                 `yaml:"enabled"`
	Provider          string               `yaml:"provider"`
	CrossEncModelPath string               `yaml:"crossenc_model_path"`
	LLMModel          string               `yaml:"llm_model"`
	Remote            RemoteRerankerConfig `yaml:"remote"`
	TimeoutMS         int                  `yaml:"timeout_ms"`
	MaxCandidates     int                  `yaml:"max_candidates"`
}

// ChunkingConfig holds chunker settings.
type ChunkingConfig struct {
	MaxTokens           int     `yaml:"max_tokens"`
	MinTokens           int     `yaml:"min_tokens"`
	SimilarityThreshold float64 `yaml:"similarity_threshold"`
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// Config is the root configuration struct.
type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Embedding EmbeddingConfig `yaml:"embedding"`
	Vector    VectorConfig    `yaml:"vector"`
	Meta      MetaConfig      `yaml:"meta"`
	Retrieval RetrievalConfig `yaml:"retrieval"`
	Rerank    RerankConfig    `yaml:"rerank"`
	Chunking  ChunkingConfig  `yaml:"chunking"`
	Logging   LoggingConfig   `yaml:"logging"`
}

// Defaults returns a Config populated with sensible defaults.
func Defaults() *Config {
	return &Config{
		Server: ServerConfig{
			MCPStdio: true,
			HTTPAddr: ":8080",
		},
		Embedding: EmbeddingConfig{
			Provider:  "ollama",
			BaseURL:   "http://ollama:11434",
			Model:     "nomic-embed-text",
			Dim:       768,
			Batch:     32,
			TimeoutMS: 5000,
			Retries:   3,
		},
		Vector: VectorConfig{
			Provider:   "qdrant",
			Addr:       "qdrant:6334",
			Collection: "memories",
		},
		Meta: MetaConfig{
			Provider: "postgres",
			DSN:      "postgres://engram:engram@postgres:5432/engram?sslmode=disable",
		},
		Retrieval: RetrievalConfig{
			VectorK:     20,
			BM25K:       20,
			RerankK:     20,
			FinalK:      5,
			RRFK:        60,
			VectorFloor: 0.25,
		},
		Rerank: RerankConfig{
			Enabled:           true,
			Provider:          "crossenc",
			CrossEncModelPath: "/models/bge-reranker-base.onnx",
			LLMModel:          "llama3.2:1b",
			TimeoutMS:         1500,
			MaxCandidates:     20,
		},
		Chunking: ChunkingConfig{
			MaxTokens:           512,
			MinTokens:           100,
			SimilarityThreshold: 0.6,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}
}

// Load reads a YAML config file at path, applies env overrides, and validates.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %q: %w", path, err)
	}

	cfg := Defaults()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("config: parse %q: %w", path, err)
	}

	applyEnvOverrides(cfg)

	if err := validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// LoadOrDefaults returns Defaults() if path is empty; otherwise calls Load.
func LoadOrDefaults(path string) (*Config, error) {
	if path == "" {
		return Defaults(), nil
	}
	return Load(path)
}

// applyEnvOverrides overwrites config fields from ENGRAM_<SECTION>_<KEY> env vars.
func applyEnvOverrides(cfg *Config) {
	setStr := func(key string, dst *string) {
		if v := os.Getenv(key); v != "" {
			*dst = v
		}
	}
	setBool := func(key string, dst *bool) {
		if v := os.Getenv(key); v != "" {
			if b, err := strconv.ParseBool(v); err == nil {
				*dst = b
			}
		}
	}
	setInt := func(key string, dst *int) {
		if v := os.Getenv(key); v != "" {
			if i, err := strconv.Atoi(v); err == nil {
				*dst = i
			}
		}
	}
	setFloat := func(key string, dst *float64) {
		if v := os.Getenv(key); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				*dst = f
			}
		}
	}

	// Server
	setBool("ENGRAM_SERVER_MCP_STDIO", &cfg.Server.MCPStdio)
	setStr("ENGRAM_SERVER_HTTP_ADDR", &cfg.Server.HTTPAddr)

	// Embedding
	setStr("ENGRAM_EMBEDDING_PROVIDER", &cfg.Embedding.Provider)
	setStr("ENGRAM_EMBEDDING_BASE_URL", &cfg.Embedding.BaseURL)
	setStr("ENGRAM_EMBEDDING_MODEL", &cfg.Embedding.Model)
	setInt("ENGRAM_EMBEDDING_DIM", &cfg.Embedding.Dim)
	setInt("ENGRAM_EMBEDDING_BATCH", &cfg.Embedding.Batch)
	setInt("ENGRAM_EMBEDDING_TIMEOUT_MS", &cfg.Embedding.TimeoutMS)
	setInt("ENGRAM_EMBEDDING_RETRIES", &cfg.Embedding.Retries)

	// Vector
	setStr("ENGRAM_VECTOR_PROVIDER", &cfg.Vector.Provider)
	setStr("ENGRAM_VECTOR_ADDR", &cfg.Vector.Addr)
	setStr("ENGRAM_VECTOR_COLLECTION", &cfg.Vector.Collection)

	// Meta
	setStr("ENGRAM_META_PROVIDER", &cfg.Meta.Provider)
	setStr("ENGRAM_META_DSN", &cfg.Meta.DSN)

	// Retrieval
	setInt("ENGRAM_RETRIEVAL_VECTOR_K", &cfg.Retrieval.VectorK)
	setInt("ENGRAM_RETRIEVAL_BM25_K", &cfg.Retrieval.BM25K)
	setInt("ENGRAM_RETRIEVAL_RERANK_K", &cfg.Retrieval.RerankK)
	setInt("ENGRAM_RETRIEVAL_FINAL_K", &cfg.Retrieval.FinalK)
	setInt("ENGRAM_RETRIEVAL_RRF_K", &cfg.Retrieval.RRFK)
	setFloat("ENGRAM_RETRIEVAL_VECTOR_FLOOR", &cfg.Retrieval.VectorFloor)

	// Rerank
	setBool("ENGRAM_RERANK_ENABLED", &cfg.Rerank.Enabled)
	setStr("ENGRAM_RERANK_PROVIDER", &cfg.Rerank.Provider)
	setStr("ENGRAM_RERANK_CROSSENC_MODEL_PATH", &cfg.Rerank.CrossEncModelPath)
	setStr("ENGRAM_RERANK_LLM_MODEL", &cfg.Rerank.LLMModel)
	setStr("ENGRAM_RERANK_REMOTE_BASE_URL", &cfg.Rerank.Remote.BaseURL)
	setStr("ENGRAM_RERANK_REMOTE_API_KEY_ENV", &cfg.Rerank.Remote.APIKeyEnv)
	setStr("ENGRAM_RERANK_REMOTE_MODEL", &cfg.Rerank.Remote.Model)
	setInt("ENGRAM_RERANK_TIMEOUT_MS", &cfg.Rerank.TimeoutMS)
	setInt("ENGRAM_RERANK_MAX_CANDIDATES", &cfg.Rerank.MaxCandidates)

	// Chunking
	setInt("ENGRAM_CHUNKING_MAX_TOKENS", &cfg.Chunking.MaxTokens)
	setInt("ENGRAM_CHUNKING_MIN_TOKENS", &cfg.Chunking.MinTokens)
	setFloat("ENGRAM_CHUNKING_SIMILARITY_THRESHOLD", &cfg.Chunking.SimilarityThreshold)

	// Logging
	setStr("ENGRAM_LOGGING_LEVEL", &cfg.Logging.Level)
	setStr("ENGRAM_LOGGING_FORMAT", &cfg.Logging.Format)
}

var validRerankProviders = map[string]bool{
	"crossenc": true,
	"llm":      true,
	"remote":   true,
	"none":     true,
}

// validate checks required fields and acceptable ranges.
func validate(cfg *Config) error {
	var errs []string

	if cfg.Embedding.Dim <= 0 {
		errs = append(errs, "embedding.dim must be > 0")
	}
	if cfg.Retrieval.FinalK <= 0 {
		errs = append(errs, "retrieval.final_k must be > 0")
	}
	if cfg.Retrieval.VectorFloor < 0 || cfg.Retrieval.VectorFloor > 1 {
		errs = append(errs, "retrieval.vector_floor must be in [0, 1]")
	}
	if strings.TrimSpace(cfg.Meta.DSN) == "" {
		errs = append(errs, "meta.dsn must not be empty")
	}
	if strings.TrimSpace(cfg.Vector.Addr) == "" {
		errs = append(errs, "vector.addr must not be empty")
	}
	if !validRerankProviders[cfg.Rerank.Provider] {
		errs = append(errs, fmt.Sprintf("rerank.provider %q is not valid; must be one of: crossenc, llm, remote, none", cfg.Rerank.Provider))
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}
