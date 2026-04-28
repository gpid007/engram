// Package mcp implements the MCP stdio transport.
package mcp

import (
	"context"
	"os"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/gregdhill/engram/internal/memory"
	"github.com/gregdhill/engram/internal/store/postgres"
)

// Server wraps the MCP server with Engram tool handlers.
type Server struct {
	srv       *mcpserver.MCPServer
	ingestor  *memory.Ingestor
	retriever *memory.Retriever
	meta      postgres.MetaStore
}

// NewServer constructs a Server, registers the three Engram tools, and returns
// a ready-to-serve instance.
func NewServer(ingestor *memory.Ingestor, retriever *memory.Retriever, meta postgres.MetaStore) *Server {
	s := &Server{
		ingestor:  ingestor,
		retriever: retriever,
		meta:      meta,
	}

	s.srv = mcpserver.NewMCPServer("engram", "1.0.0")
	registerTools(s)
	return s
}

// ServeStdio starts the MCP server on stdin/stdout using the mcp-go stdio
// transport. It blocks until ctx is cancelled or an error occurs.
func (s *Server) ServeStdio(ctx context.Context) error {
	stdio := mcpserver.NewStdioServer(s.srv)

	// Respect context cancellation by wrapping Listen with the caller's ctx.
	return stdio.Listen(ctx, os.Stdin, os.Stdout)
}
