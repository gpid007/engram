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
	ModelDir  string `yaml:"model_dir"`  // ONNX: directory containing model.onnx and tokenizer.json
	LibPath   string `yaml:"lib_path"`   // ONNX: path to onnxruntime shared library; auto-detected if empty
	MaxSeqLen int    `yaml:"max_seq_len"` // ONNX: max token sequence length (default 8192)
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

// GraphConfig holds graph store settings.
type GraphConfig struct {
	Provider         string  `yaml:"provider"`          // "neo4j" | "none" (default)
	URI              string  `yaml:"uri"`               // bolt://host:7687
	Username         string  `yaml:"username"`
	PasswordEnv      string  `yaml:"password_env"`      // env var name (e.g. NEO4J_PASSWORD)
	Database         string  `yaml:"database"`          // default "neo4j"
	WriteSimilar     bool    `yaml:"write_similar"`     // default false (deferred to reconciler)
	SimilarThreshold float64 `yaml:"similar_threshold"` // default 0.75
	MaxExpand        int     `yaml:"max_expand"`        // default 10
	TimeoutMS        int     `yaml:"timeout_ms"`        // default 2000
}

// CLIConfig holds settings for the engram CLI subcommands (put/get/status).
type CLIConfig struct {
	BaseURL        string `yaml:"base_url"`
	UserID         string `yaml:"user_id"`
	ParseModel     string `yaml:"parse_model"`
	ParseBaseURL   string `yaml:"parse_base_url"`
	ParseTimeoutMS int    `yaml:"parse_timeout_ms"`
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
	Graph     GraphConfig     `yaml:"graph"`
	CLI       CLIConfig       `yaml:"cli"`
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
		Graph: GraphConfig{
			Provider:         "none",
			Database:         "neo4j",
			SimilarThreshold: 0.75,
			MaxExpand:        10,
			TimeoutMS:        2000,
		},
		CLI: CLIConfig{
			BaseURL:        "http://localhost:8080",
			UserID:         "greg",
			ParseModel:     "llama3.2:1b",
			ParseBaseURL:   "http://localhost:11434",
			ParseTimeoutMS: 10000,
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
	setStr("ENGRAM_EMBEDDING_MODEL_DIR", &cfg.Embedding.ModelDir)
	setStr("ENGRAM_EMBEDDING_LIB_PATH", &cfg.Embedding.LibPath)
	setInt("ENGRAM_EMBEDDING_MAX_SEQ_LEN", &cfg.Embedding.MaxSeqLen)

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

	// Graph
	setStr("ENGRAM_GRAPH_PROVIDER", &cfg.Graph.Provider)
	setStr("ENGRAM_GRAPH_URI", &cfg.Graph.URI)
	setStr("ENGRAM_GRAPH_USERNAME", &cfg.Graph.Username)
	setStr("ENGRAM_GRAPH_PASSWORD_ENV", &cfg.Graph.PasswordEnv)
	setStr("ENGRAM_GRAPH_DATABASE", &cfg.Graph.Database)
	setBool("ENGRAM_GRAPH_WRITE_SIMILAR", &cfg.Graph.WriteSimilar)
	setFloat("ENGRAM_GRAPH_SIMILAR_THRESHOLD", &cfg.Graph.SimilarThreshold)
	setInt("ENGRAM_GRAPH_MAX_EXPAND", &cfg.Graph.MaxExpand)
	setInt("ENGRAM_GRAPH_TIMEOUT_MS", &cfg.Graph.TimeoutMS)
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

	if cfg.Graph.Provider != "" && cfg.Graph.Provider != "none" {
		if cfg.Graph.Provider != "neo4j" {
			errs = append(errs, fmt.Sprintf("graph.provider %q must be one of: none, neo4j", cfg.Graph.Provider))
		} else {
			if strings.TrimSpace(cfg.Graph.URI) == "" {
				errs = append(errs, "graph.uri must be set when provider=neo4j")
			}
			if strings.TrimSpace(cfg.Graph.Username) == "" {
				errs = append(errs, "graph.username must be set when provider=neo4j")
			}
			if strings.TrimSpace(cfg.Graph.PasswordEnv) == "" {
				errs = append(errs, "graph.password_env must be set when provider=neo4j")
			} else if os.Getenv(cfg.Graph.PasswordEnv) == "" {
				errs = append(errs, fmt.Sprintf("graph.password_env %q resolves to empty value; set it in the environment", cfg.Graph.PasswordEnv))
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}
