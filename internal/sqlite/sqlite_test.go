package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kiddyuchina/sqlited/internal/config"
	"github.com/kiddyuchina/sqlited/internal/d1"
)

func TestIsReadStatement(t *testing.T) {
	cases := []struct {
		sql  string
		want bool
	}{
		{"SELECT 1", true},
		{"select 1", true},
		{"  SELECT * FROM t", true},
		{"PRAGMA journal_mode", true},
		{"EXPLAIN SELECT 1", true},
		{"WITH cte AS (SELECT 1) SELECT * FROM cte", true},
		{"INSERT INTO t VALUES (1)", false},
		{"UPDATE t SET x = 1", false},
		{"DELETE FROM t", false},
		{"CREATE TABLE t (id INT)", false},
	}
	for _, c := range cases {
		t.Run(c.sql, func(t *testing.T) {
			if got := isReadStatement(c.sql); got != c.want {
				t.Errorf("isReadStatement(%q) = %v, want %v", c.sql, got, c.want)
			}
		})
	}
}

func TestExecuteBatch(t *testing.T) {
	dir := t.TempDir()
	cfg := config.DatabaseConfig{Path: filepath.Join(dir, "test.db"), BusyTimeoutMS: 5000, WALMode: true}
	db, err := OpenDatabase("test", cfg)
	if err != nil {
		t.Fatalf("OpenDatabase failed: %v", err)
	}
	defer Close(db)

	ctx := context.Background()

	t.Run("create table and insert", func(t *testing.T) {
		stmts := []d1.QueryRequest{
			{SQL: "CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)"},
			{SQL: "INSERT INTO t (name) VALUES (?)", Params: []any{"Alice"}},
		}
		res, err := ExecuteBatch(ctx, db, stmts)
		if err != nil {
			t.Fatalf("batch failed: %v", err)
		}
		if len(res) != 2 {
			t.Fatalf("expected 2 results, got %d", len(res))
		}
		if res[0].Meta.RowsWritten != 0 {
			t.Errorf("CREATE rows_written = %d, want 0", res[0].Meta.RowsWritten)
		}
		if res[1].Meta.RowsWritten != 1 {
			t.Errorf("INSERT rows_written = %d, want 1", res[1].Meta.RowsWritten)
		}
	})

	t.Run("select returns rows", func(t *testing.T) {
		res, err := ExecuteBatch(ctx, db, []d1.QueryRequest{{SQL: "SELECT id, name FROM t"}})
		if err != nil {
			t.Fatalf("select failed: %v", err)
		}
		if len(res) != 1 || len(res[0].Results) != 1 {
			t.Fatalf("expected 1 row, got %v", res)
		}
		if res[0].Results[0]["name"] != "Alice" {
			t.Errorf("name = %v, want Alice", res[0].Results[0]["name"])
		}
		if res[0].Meta.RowsRead != 1 {
			t.Errorf("rows_read = %d, want 1", res[0].Meta.RowsRead)
		}
	})

	t.Run("failed batch rolls back", func(t *testing.T) {
		_, err := ExecuteBatch(ctx, db, []d1.QueryRequest{
			{SQL: "INSERT INTO t (id, name) VALUES (2, ?)", Params: []any{"Bob"}},
			{SQL: "INSERT INTO t (id, name) VALUES (2, ?)", Params: []any{"Charlie"}}, // duplicate primary key
		})
		if err == nil {
			t.Fatal("expected error for invalid batch, got nil")
		}

		res, err := ExecuteBatch(ctx, db, []d1.QueryRequest{{SQL: "SELECT count(*) AS n FROM t"}})
		if err != nil {
			t.Fatalf("count failed: %v", err)
		}
		count, ok := res[0].Results[0]["n"].(int64)
		if !ok {
			t.Fatalf("unexpected count type: %T", res[0].Results[0]["n"])
		}
		if count != 1 {
			t.Errorf("expected 1 row after rollback, got %d", count)
		}
	})
}
