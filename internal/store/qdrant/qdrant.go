// Package qdrant implements the VectorStore interface backed by Qdrant.
package qdrant

import (
	"context"
	"fmt"
	"strings"

	qdrantclient "github.com/qdrant/go-client/qdrant"
)

// Point is a vector point to be stored in Qdrant.
type Point struct {
	ID      string         // chunk_id UUID string
	Vector  []float32
	Payload map[string]any // memory_id, chunk_id, user_id, ord, source, importance, created_at
}

// SearchResult is a single result from a vector search.
type SearchResult struct {
	ID      string
	Score   float32
	Payload map[string]any
}

// VectorStore is the interface for Qdrant-backed vector storage.
type VectorStore interface {
	EnsureCollection(ctx context.Context, dim uint64) error
	Upsert(ctx context.Context, points []Point) error
	Search(ctx context.Context, vector []float32, k uint64, userID string) ([]SearchResult, error)
	// DeleteByMemoryID removes all vectors associated with a memory ID.
	DeleteByMemoryID(ctx context.Context, memoryID string) error
	Close() error
}

// Store is the concrete implementation of VectorStore.
type Store struct {
	client     *qdrantclient.Client
	collection string
}

// New creates a new Qdrant-backed Store.
// addr is the gRPC address, e.g. "localhost:6334".
func New(ctx context.Context, addr string, collection string) (*Store, error) {
	// Parse host:port from addr
	host, port, err := parseAddr(addr)
	if err != nil {
		return nil, fmt.Errorf("qdrant: parse addr %q: %w", addr, err)
	}

	client, err := qdrantclient.NewClient(&qdrantclient.Config{
		Host:     host,
		Port:     port,
		PoolSize: 1,
	})
	if err != nil {
		return nil, fmt.Errorf("qdrant: create client: %w", err)
	}

	return &Store{
		client:     client,
		collection: collection,
	}, nil
}

// parseAddr splits "host:port" into host (string) and port (int).
func parseAddr(addr string) (string, int, error) {
	lastColon := strings.LastIndex(addr, ":")
	if lastColon < 0 {
		return "", 0, fmt.Errorf("no port in address")
	}
	host := addr[:lastColon]
	var port int
	_, err := fmt.Sscanf(addr[lastColon+1:], "%d", &port)
	if err != nil {
		return "", 0, fmt.Errorf("invalid port: %w", err)
	}
	return host, port, nil
}

// EnsureCollection creates the collection with Cosine distance if it doesn't
// exist. If the collection already exists, it verifies the vector dimension
// matches dim; a mismatch returns an error.
func (s *Store) EnsureCollection(ctx context.Context, dim uint64) error {
	exists, err := s.client.CollectionExists(ctx, s.collection)
	if err != nil {
		return fmt.Errorf("qdrant: check collection: %w", err)
	}

	if !exists {
		err = s.client.CreateCollection(ctx, &qdrantclient.CreateCollection{
			CollectionName: s.collection,
			VectorsConfig: qdrantclient.NewVectorsConfig(&qdrantclient.VectorParams{
				Size:     dim,
				Distance: qdrantclient.Distance_Cosine,
			}),
		})
		if err != nil {
			return fmt.Errorf("qdrant: create collection: %w", err)
		}
		return nil
	}

	// Collection exists — verify dimension.
	info, err := s.client.GetCollectionInfo(ctx, s.collection)
	if err != nil {
		return fmt.Errorf("qdrant: get collection info: %w", err)
	}

	existingDim := uint64(0)
	if cfg := info.GetConfig(); cfg != nil {
		if params := cfg.GetParams(); params != nil {
			if vc := params.GetVectorsConfig(); vc != nil {
				if vp := vc.GetParams(); vp != nil {
					existingDim = vp.GetSize()
				}
			}
		}
	}

	if existingDim != dim {
		return fmt.Errorf("qdrant: collection %q already exists with dim=%d, want dim=%d",
			s.collection, existingDim, dim)
	}
	return nil
}

// Upsert inserts or updates a batch of points in the collection.
func (s *Store) Upsert(ctx context.Context, points []Point) error {
	if len(points) == 0 {
		return nil
	}

	structs := make([]*qdrantclient.PointStruct, 0, len(points))
	for _, p := range points {
		payload, err := toQdrantPayload(p.Payload)
		if err != nil {
			return fmt.Errorf("qdrant: encode payload for point %q: %w", p.ID, err)
		}
		structs = append(structs, &qdrantclient.PointStruct{
			Id:      qdrantclient.NewIDUUID(p.ID),
			Vectors: qdrantclient.NewVectorsDense(p.Vector),
			Payload: payload,
		})
	}

	wait := true
	_, err := s.client.Upsert(ctx, &qdrantclient.UpsertPoints{
		CollectionName: s.collection,
		Wait:           &wait,
		Points:         structs,
	})
	if err != nil {
		return fmt.Errorf("qdrant: upsert: %w", err)
	}
	return nil
}

// Search performs a cosine similarity search filtered by user_id and returns
// the top k results.
func (s *Store) Search(ctx context.Context, vector []float32, k uint64, userID string) ([]SearchResult, error) {
	limit := k
	results, err := s.client.Query(ctx, &qdrantclient.QueryPoints{
		CollectionName: s.collection,
		Query:          qdrantclient.NewQueryDense(vector),
		Filter: &qdrantclient.Filter{
			Must: []*qdrantclient.Condition{
				qdrantclient.NewMatchKeyword("user_id", userID),
			},
		},
		Limit:       &limit,
		WithPayload: qdrantclient.NewWithPayload(true),
	})
	if err != nil {
		return nil, fmt.Errorf("qdrant: query: %w", err)
	}

	out := make([]SearchResult, 0, len(results))
	for _, sp := range results {
		id := ""
		if sp.GetId() != nil {
			id = sp.GetId().GetUuid()
		}
		payload := fromQdrantPayload(sp.GetPayload())
		out = append(out, SearchResult{
			ID:      id,
			Score:   sp.GetScore(),
			Payload: payload,
		})
	}
	return out, nil
}

// Close tears down the underlying gRPC connection pool.
func (s *Store) Close() error {
	return s.client.Close()
}

// toQdrantPayload converts a map[string]any to the Qdrant protobuf payload map.
func toQdrantPayload(m map[string]any) (map[string]*qdrantclient.Value, error) {
	out := make(map[string]*qdrantclient.Value, len(m))
	for k, v := range m {
		qv, err := anyToValue(v)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", k, err)
		}
		out[k] = qv
	}
	return out, nil
}

// anyToValue converts a Go any value to a qdrant *Value.
func anyToValue(v any) (*qdrantclient.Value, error) {
	switch val := v.(type) {
	case string:
		return &qdrantclient.Value{Kind: &qdrantclient.Value_StringValue{StringValue: val}}, nil
	case bool:
		return &qdrantclient.Value{Kind: &qdrantclient.Value_BoolValue{BoolValue: val}}, nil
	case int:
		return &qdrantclient.Value{Kind: &qdrantclient.Value_IntegerValue{IntegerValue: int64(val)}}, nil
	case int32:
		return &qdrantclient.Value{Kind: &qdrantclient.Value_IntegerValue{IntegerValue: int64(val)}}, nil
	case int64:
		return &qdrantclient.Value{Kind: &qdrantclient.Value_IntegerValue{IntegerValue: val}}, nil
	case float32:
		return &qdrantclient.Value{Kind: &qdrantclient.Value_DoubleValue{DoubleValue: float64(val)}}, nil
	case float64:
		return &qdrantclient.Value{Kind: &qdrantclient.Value_DoubleValue{DoubleValue: val}}, nil
	case nil:
		return &qdrantclient.Value{Kind: &qdrantclient.Value_NullValue{}}, nil
	default:
		return nil, fmt.Errorf("unsupported payload value type %T", v)
	}
}

// fromQdrantPayload converts a Qdrant protobuf payload map back to map[string]any.
// DeleteByMemoryID removes all Qdrant points whose payload "memory_id" == memoryID.
func (s *Store) DeleteByMemoryID(ctx context.Context, memoryID string) error {
	_, err := s.client.Delete(ctx, &qdrantclient.DeletePoints{
		CollectionName: s.collection,
		Points: &qdrantclient.PointsSelector{
			PointsSelectorOneOf: &qdrantclient.PointsSelector_Filter{
				Filter: &qdrantclient.Filter{
					Must: []*qdrantclient.Condition{
						qdrantclient.NewMatchKeyword("memory_id", memoryID),
					},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("qdrant: delete by memory_id %q: %w", memoryID, err)
	}
	return nil
}

func fromQdrantPayload(m map[string]*qdrantclient.Value) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if v == nil {
			out[k] = nil
			continue
		}
		switch kind := v.Kind.(type) {
		case *qdrantclient.Value_StringValue:
			out[k] = kind.StringValue
		case *qdrantclient.Value_BoolValue:
			out[k] = kind.BoolValue
		case *qdrantclient.Value_IntegerValue:
			out[k] = kind.IntegerValue
		case *qdrantclient.Value_DoubleValue:
			out[k] = kind.DoubleValue
		case *qdrantclient.Value_NullValue:
			out[k] = nil
		default:
			out[k] = nil
		}
	}
	return out
}
