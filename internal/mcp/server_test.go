package mcp

import (
	"testing"
)

// TestNewServerNotNil verifies that NewServer returns a non-nil *Server when
// given nil service dependencies. It is a compile-time and basic sanity check;
// no live services are required.
func TestNewServerNotNil(t *testing.T) {
	s := NewServer(nil, nil, nil)
	if s == nil {
		t.Fatal("NewServer returned nil")
	}
	if s.srv == nil {
		t.Fatal("NewServer: internal MCPServer is nil")
	}
}
