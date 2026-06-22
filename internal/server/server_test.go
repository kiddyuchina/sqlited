package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/kiddyuchina/sqlited/internal/config"
	"github.com/kiddyuchina/sqlited/internal/d1"
)

func TestAuthMiddleware(t *testing.T) {
	cfg := &config.Config{AuthToken: "valid-token", Databases: map[string]config.DatabaseConfig{}}
	s := &Server{cfg: cfg}

	handler := s.authMiddleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"missing", "", http.StatusUnauthorized},
		{"no bearer prefix", "valid-token", http.StatusUnauthorized},
		{"wrong token", "Bearer wrong", http.StatusUnauthorized},
		{"extra whitespace", "Bearer  valid-token ", http.StatusOK},
		{"valid", "Bearer valid-token", http.StatusOK},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/accounts/local/d1/database/db/query", nil)
			if c.header != "" {
				req.Header.Set("Authorization", c.header)
			}
			rr := httptest.NewRecorder()
			handler(rr, req)

			if rr.Code != c.want {
				t.Errorf("expected status %d, got %d", c.want, rr.Code)
			}
			if c.want != http.StatusOK {
				var resp d1.Response
				if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
					t.Fatalf("invalid error response JSON: %v", err)
				}
				if resp.Success || len(resp.Errors) == 0 {
					t.Errorf("expected error envelope, got %v", resp)
				}
			}
		})
	}
}

func TestHandleQuery(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		AuthToken: "token",
		Databases: map[string]config.DatabaseConfig{
			"db1": {Path: filepath.Join(dir, "db1.db"), BusyTimeoutMS: 5000, WALMode: true},
		},
	}
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer srv.Close()

	makeRequest := func(token, dbKey string, body []d1.QueryRequest) *httptest.ResponseRecorder {
		payload, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/accounts/local/d1/database/"+dbKey+"/query", bytes.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		srv.Routes().ServeHTTP(rr, req)
		return rr
	}

	t.Run("successful select", func(t *testing.T) {
		rr := makeRequest("token", "db1", []d1.QueryRequest{{SQL: "SELECT 1 AS n"}})
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
		var resp d1.Response
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("invalid response JSON: %v", err)
		}
		if !resp.Success || len(resp.Result) != 1 {
			t.Fatalf("unexpected response: %v", resp)
		}
		if resp.Result[0].Results[0]["n"] != float64(1) {
			t.Errorf("expected 1, got %v (type %T)", resp.Result[0].Results[0]["n"], resp.Result[0].Results[0]["n"])
		}
	})

	t.Run("unknown database", func(t *testing.T) {
		rr := makeRequest("token", "missing", []d1.QueryRequest{{SQL: "SELECT 1"}})
		if rr.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rr.Code)
		}
	})

	t.Run("bad auth", func(t *testing.T) {
		rr := makeRequest("wrong", "db1", []d1.QueryRequest{{SQL: "SELECT 1"}})
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rr.Code)
		}
	})

	t.Run("empty batch", func(t *testing.T) {
		rr := makeRequest("token", "db1", []d1.QueryRequest{})
		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rr.Code)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/accounts/local/d1/database/db1/query", bytes.NewReader([]byte("not json")))
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		srv.Routes().ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rr.Code)
		}
	})

	t.Run("single object", func(t *testing.T) {
		payload := `{"sql":"SELECT 1 AS n","params":[]}`
		req := httptest.NewRequest(http.MethodPost, "/accounts/local/d1/database/db1/query", bytes.NewReader([]byte(payload)))
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		srv.Routes().ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
		var resp d1.Response
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("invalid response JSON: %v", err)
		}
		if !resp.Success || len(resp.Result) != 1 {
			t.Fatalf("unexpected response: %v", resp)
		}
		if resp.Result[0].Results[0]["n"] != float64(1) {
			t.Errorf("expected 1, got %v", resp.Result[0].Results[0]["n"])
		}
	})
}

func TestHandleHealth(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		AuthToken: "token",
		Databases: map[string]config.DatabaseConfig{
			"a": {Path: filepath.Join(dir, "a.db"), BusyTimeoutMS: 5000, WALMode: true},
		},
	}
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer srv.Close()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid health JSON: %v", err)
	}
	if resp["status"] != "ok" || resp["databases"] != float64(1) {
		t.Fatalf("unexpected health response: %v", resp)
	}
}
