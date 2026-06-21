//go:build integration

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiddyuchina/sqlited/internal/config"
	"github.com/kiddyuchina/sqlited/internal/d1"
)

// startTestServer boots a real sqlited HTTP server on an ephemeral port and
// returns the base URL, the cleanup function, and the auth token.
func startTestServer(t *testing.T) (string, func(), string) {
	t.Helper()

	dir := t.TempDir()
	token := "integration-test-token"
	cfg := &config.Config{
		Port:      0, // let the kernel choose a free port
		AuthToken: token,
		Databases: map[string]config.DatabaseConfig{
			"test": {
				Path:          filepath.Join(dir, "test.db"),
				BusyTimeoutMS: 5000,
				WALMode:       true,
			},
		},
	}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		srv.Close()
		t.Fatalf("listen: %v", err)
	}

	httpSrv := &http.Server{
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := httpSrv.Serve(listener); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	baseURL := fmt.Sprintf("http://%s", listener.Addr().String())
	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
		srv.Close()
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(baseURL + "/health")
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			cleanup()
			t.Fatalf("server did not start: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	return baseURL, cleanup, token
}

func d1Request(t *testing.T, baseURL, token, dbKey string, body []d1.QueryRequest) (*http.Response, []byte) {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	url := fmt.Sprintf("%s/accounts/local/d1/database/%s/query", baseURL, dbKey)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return resp, data
}

func TestIntegrationHealth(t *testing.T) {
	baseURL, cleanup, _ := startTestServer(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/health")
	if err != nil {
		t.Fatalf("health request: %v", err)
	}
	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read health: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, data)
	}
	var status struct {
		Status    string `json:"status"`
		Databases int    `json:"databases"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatalf("invalid health JSON: %v", err)
	}
	if status.Status != "ok" || status.Databases != 1 {
		t.Fatalf("unexpected health status: %+v", status)
	}
}

func TestIntegrationD1Batch(t *testing.T) {
	baseURL, cleanup, token := startTestServer(t)
	defer cleanup()

	body := []d1.QueryRequest{
		{SQL: "CREATE TABLE IF NOT EXISTS users (id INTEGER PRIMARY KEY, name TEXT)"},
		{SQL: "INSERT INTO users (name) VALUES (?)", Params: []any{"Alice"}},
		{SQL: "INSERT INTO users (name) VALUES (?)", Params: []any{"Bob"}},
		{SQL: "SELECT name FROM users ORDER BY name"},
	}
	resp, data := d1Request(t, baseURL, token, "test", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, data)
	}

	var d1Resp d1.Response
	if err := json.Unmarshal(data, &d1Resp); err != nil {
		t.Fatalf("invalid D1 response: %v", err)
	}
	if !d1Resp.Success || len(d1Resp.Result) != 4 {
		t.Fatalf("unexpected response: %+v", d1Resp)
	}
	if d1Resp.Result[1].Meta.RowsWritten != 1 || d1Resp.Result[2].Meta.RowsWritten != 1 {
		t.Errorf("insert rows_written mismatch: %+v", d1Resp.Result)
	}
	if len(d1Resp.Result[3].Results) != 2 {
		t.Errorf("expected 2 rows, got %d", len(d1Resp.Result[3].Results))
	}
	if d1Resp.Result[3].Meta.RowsRead != 2 {
		t.Errorf("expected rows_read=2, got %d", d1Resp.Result[3].Meta.RowsRead)
	}
}

func TestIntegrationD1TransactionRollback(t *testing.T) {
	baseURL, cleanup, token := startTestServer(t)
	defer cleanup()

	resp, data := d1Request(t, baseURL, token, "test", []d1.QueryRequest{
		{SQL: "CREATE TABLE IF NOT EXISTS counters (id INTEGER PRIMARY KEY, val INTEGER)"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setup failed: %d %s", resp.StatusCode, data)
	}

	resp, data = d1Request(t, baseURL, token, "test", []d1.QueryRequest{
		{SQL: "INSERT INTO counters (id, val) VALUES (1, 1)"},
		{SQL: "INSERT INTO counters (id, val) VALUES (1, 2)"}, // duplicate primary key
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for failing batch, got %d: %s", resp.StatusCode, data)
	}
	var d1Resp d1.Response
	if err := json.Unmarshal(data, &d1Resp); err != nil {
		t.Fatalf("invalid error response: %v", err)
	}
	if d1Resp.Success || len(d1Resp.Errors) == 0 {
		t.Fatalf("expected error envelope, got %+v", d1Resp)
	}

	resp, data = d1Request(t, baseURL, token, "test", []d1.QueryRequest{
		{SQL: "SELECT count(*) AS n FROM counters"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("count failed: %d %s", resp.StatusCode, data)
	}
	if err := json.Unmarshal(data, &d1Resp); err != nil {
		t.Fatalf("invalid count response: %v", err)
	}
	count, ok := d1Resp.Result[0].Results[0]["n"].(float64)
	if !ok {
		t.Fatalf("unexpected count type: %T", d1Resp.Result[0].Results[0]["n"])
	}
	if count != 0 {
		t.Errorf("expected 0 rows after rollback, got %v", count)
	}
}

func TestIntegrationAuthAndRouting(t *testing.T) {
	baseURL, cleanup, token := startTestServer(t)
	defer cleanup()

	cases := []struct {
		name   string
		token  string
		dbKey  string
		status int
	}{
		{"missing auth", "", "test", http.StatusUnauthorized},
		{"wrong auth", "wrong", "test", http.StatusUnauthorized},
		{"unknown db", token, "does-not-exist", http.StatusNotFound},
		{"valid", token, "test", http.StatusOK},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, data := d1Request(t, baseURL, c.token, c.dbKey, []d1.QueryRequest{{SQL: "SELECT 1"}})
			if resp.StatusCode != c.status {
				t.Errorf("expected %d, got %d: %s", c.status, resp.StatusCode, data)
			}
		})
	}
}
