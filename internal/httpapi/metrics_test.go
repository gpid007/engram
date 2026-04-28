package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestNewMetrics_NotNil(t *testing.T) {
	m := NewMetrics()
	if m == nil {
		t.Fatal("NewMetrics() returned nil")
	}
	if m.Handler() == nil {
		t.Fatal("Handler() returned nil")
	}
}

func TestRecordIngest_Count(t *testing.T) {
	m := NewMetrics()
	m.RecordIngest(10.0)
	m.RecordIngest(20.0)
	m.RecordIngest(30.0)

	// Gather from the registry and verify count via text format.
	recs, err := m.registry.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	found := false
	for _, mf := range recs {
		if mf.GetName() == "engram_ingest_duration_ms" {
			found = true
			h := mf.GetMetric()[0].GetHistogram()
			if h.GetSampleCount() != 3 {
				t.Errorf("expected sample count 3, got %d", h.GetSampleCount())
			}
			if h.GetSampleSum() != 60.0 {
				t.Errorf("expected sample sum 60, got %f", h.GetSampleSum())
			}
		}
	}
	if !found {
		t.Error("engram_ingest_duration_ms not found in gathered metrics")
	}
}

func TestRecordRetrieve(t *testing.T) {
	m := NewMetrics()
	m.RecordRetrieve(100.0, true)
	m.RecordRetrieve(200.0, false)
	m.RecordRetrieve(50.0, true)

	recs, err := m.registry.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	found := false
	for _, mf := range recs {
		if mf.GetName() != "engram_retrieve_duration_ms" {
			continue
		}
		found = true
		for _, metric := range mf.GetMetric() {
			h := metric.GetHistogram()
			// identify label
			rerank := ""
			for _, lp := range metric.GetLabel() {
				if lp.GetName() == "rerank" {
					rerank = lp.GetValue()
				}
			}
			switch rerank {
			case "true":
				if h.GetSampleCount() != 2 {
					t.Errorf("rerank=true: expected count 2, got %d", h.GetSampleCount())
				}
				if h.GetSampleSum() != 150.0 {
					t.Errorf("rerank=true: expected sum 150, got %f", h.GetSampleSum())
				}
			case "false":
				if h.GetSampleCount() != 1 {
					t.Errorf("rerank=false: expected count 1, got %d", h.GetSampleCount())
				}
				if h.GetSampleSum() != 200.0 {
					t.Errorf("rerank=false: expected sum 200, got %f", h.GetSampleSum())
				}
			default:
				t.Errorf("unexpected rerank label value %q", rerank)
			}
		}
	}
	if !found {
		t.Error("engram_retrieve_duration_ms not found in gathered metrics")
	}
}

func TestRecordEmbedderFailure(t *testing.T) {
	m := NewMetrics()
	m.RecordEmbedderFailure()
	m.RecordEmbedderFailure()
	m.RecordEmbedderFailure()

	val := testutil.ToFloat64(m.EmbedderFailures)
	if val != 3.0 {
		t.Errorf("expected EmbedderFailures=3, got %f", val)
	}
}

func TestSetPendingVectors(t *testing.T) {
	m := NewMetrics()
	m.SetPendingVectors(42.0)

	val := testutil.ToFloat64(m.PendingVectors)
	if val != 42.0 {
		t.Errorf("expected PendingVectors=42, got %f", val)
	}

	m.SetPendingVectors(7.0)
	val = testutil.ToFloat64(m.PendingVectors)
	if val != 7.0 {
		t.Errorf("expected PendingVectors=7 after update, got %f", val)
	}
}

func TestHandlerResponds200(t *testing.T) {
	m := NewMetrics()
	// Record something so the metric appears in output.
	m.RecordIngest(10.0)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()

	m.Handler().ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	body := w.Body.String()
	if !strings.Contains(body, "engram_ingest_duration") {
		t.Errorf("expected body to contain 'engram_ingest_duration', got:\n%s", body)
	}
}
