package api

import (
	"context"
	"fmt"
	"net/http"
)

const defaultPort = 9147 // "DRCL" on phone keypad

// Server is the localhost-only HTTP API for desktop app and integrations.
type Server struct {
	port int
	srv  *http.Server
}

// NewServer creates a local API server on the given port (0 = default 9147).
func NewServer(port int) *Server {
	if port == 0 {
		port = defaultPort
	}
	return &Server{port: port}
}

// Start begins serving on localhost only. It blocks until the context is cancelled.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	s.srv = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", s.port),
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		s.srv.Close()
	}()

	if err := s.srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/status", s.handleStatus)
	// TODO: register remaining routes
	// GET  /api/standup
	// GET  /api/week
	// GET  /api/summary
	// POST /api/chat
	// GET  /api/activities
	// GET  /api/search
	// POST /api/sync
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}
