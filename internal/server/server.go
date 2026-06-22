// Package server implements the HTTP API for sqlited, including D1-compatible
// routing, authentication, and request handling.
package server

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kiddyuchina/sqlited/internal/config"
	"github.com/kiddyuchina/sqlited/internal/d1"
	"github.com/kiddyuchina/sqlited/internal/sqlite"
)

// maxRequestBytes caps the size of a query request body to prevent a single
// client from exhausting memory with an oversized JSON payload.
const maxRequestBytes = 1 << 20 // 1 MiB

// Server holds the live database connection pool keyed by db_key.
type Server struct {
	cfg    *config.Config
	pool   map[string]*sql.DB
	mu     sync.RWMutex
	closed bool
}

// NewServer opens every database declared in the config and applies the
// configured concurrency PRAGMAs.
func NewServer(cfg *config.Config) (*Server, error) {
	s := &Server{cfg: cfg, pool: make(map[string]*sql.DB)}
	for key, dbCfg := range cfg.Databases {
		db, err := sqlite.OpenDatabase(key, dbCfg)
		if err != nil {
			s.Close()
			return nil, err
		}
		s.pool[key] = db
	}
	return s, nil
}

// Close releases every open database connection. It takes the write lock, so
// it blocks until all in-flight requests (which hold the read lock for their
// full duration) have finished, preventing use-after-close races.
func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for _, db := range s.pool {
		sqlite.Close(db)
	}
}

// Routes wires up the HTTP handlers.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /accounts/local/d1/database/{db_key}/query", s.authMiddleware(s.handleQuery))
	mux.HandleFunc("GET /health", s.handleHealth)
	return logRequests(mux)
}

// authMiddleware verifies the Authorization: Bearer <token> header.
func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(header, prefix) {
			writeError(w, http.StatusUnauthorized, "missing or invalid authorization token")
			return
		}
		token := strings.TrimSpace(header[len(prefix):])
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.AuthToken)) != 1 {
			writeError(w, http.StatusUnauthorized, "missing or invalid authorization token")
			return
		}
		next(w, r)
	}
}

// handleHealth is a simple liveness probe.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]any{"status": "ok", "databases": len(s.cfg.Databases)}); err != nil {
		log.Printf("error encoding health response: %v", err)
	}
}

// handleQuery executes a batch of statements atomically against the routed db.
func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	dbKey := r.PathValue("db_key")

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)

	// Hold the read lock for the whole request so Close cannot close the
	// database out from under an in-flight query.
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		writeError(w, http.StatusServiceUnavailable, "server is shutting down")
		return
	}
	db, ok := s.pool[dbKey]
	if !ok || db == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("unknown database key %q", dbKey))
		return
	}

	statements, err := decodeStatements(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(statements) == 0 {
		writeError(w, http.StatusBadRequest, "request contains no SQL statements")
		return
	}

	results, err := sqlite.ExecuteBatch(r.Context(), db, statements)
	if err != nil {
		log.Printf("query failed on %q: %v", dbKey, err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, d1.Response{
		Result:   results,
		Success:  true,
		Errors:   []d1.Message{},
		Messages: []d1.Message{},
	})
}

// decodeStatements parses the request body, accepting a JSON array of statement
// objects (the D1 batch form) or a single statement object.
func decodeStatements(r *http.Request) ([]d1.QueryRequest, error) {
	defer r.Body.Close()

	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return nil, errors.New("request body must be a JSON array or object")
	}

	var batch []d1.QueryRequest
	if err := json.Unmarshal(raw, &batch); err == nil {
		return batch, nil
	}

	var single d1.QueryRequest
	if err := json.Unmarshal(raw, &single); err != nil {
		return nil, errors.New("request body must be a JSON array of {sql, params} objects or a single {sql, params} object")
	}
	return []d1.QueryRequest{single}, nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("error encoding response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, d1.Response{
		Result:   []d1.QueryResult{},
		Success:  false,
		Errors:   []d1.Message{{Code: status, Message: message}},
		Messages: []d1.Message{},
	})
}

// logRequests is a tiny logging middleware.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s (%s)", r.Method, r.URL.Path, time.Since(start))
	})
}
