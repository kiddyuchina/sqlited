// Package sqlite wraps opening SQLite databases and executing D1-style
// statement batches with WAL and busy_timeout configured.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/kiddyuchina/sqlited/internal/config"
	"github.com/kiddyuchina/sqlited/internal/d1"

	_ "modernc.org/sqlite"
)

// OpenDatabase opens a single SQLite database with busy_timeout and WAL applied.
func OpenDatabase(key string, cfg config.DatabaseConfig) (*sql.DB, error) {
	if strings.TrimSpace(cfg.Path) == "" {
		return nil, fmt.Errorf("database %q has an empty path", key)
	}

	busyTimeout := cfg.BusyTimeoutMS
	if busyTimeout <= 0 {
		busyTimeout = config.DefaultBusyTimeout
	}

	// modernc.org/sqlite accepts PRAGMAs as query parameters via _pragma.
	// WAL is applied explicitly after opening (see below) so it is not set here.
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(%d)", cfg.Path, busyTimeout)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database %q (%s): %w", key, cfg.Path, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("connecting to database %q (%s): %w", key, cfg.Path, err)
	}

	if cfg.WALMode {
		var mode string
		if err := db.QueryRow("PRAGMA journal_mode=WAL;").Scan(&mode); err != nil {
			db.Close()
			return nil, fmt.Errorf("enabling WAL on %q: %w", key, err)
		}
		log.Printf("database %q opened (%s) journal_mode=%s busy_timeout=%dms", key, cfg.Path, mode, busyTimeout)
	} else {
		log.Printf("database %q opened (%s) busy_timeout=%dms", key, cfg.Path, busyTimeout)
	}

	return db, nil
}

// Close closes a single database connection, logging any error.
func Close(db *sql.DB) {
	if db == nil {
		return
	}
	if err := db.Close(); err != nil {
		log.Printf("error closing database: %v", err)
	}
}

// ExecuteBatch runs all statements inside a single transaction.
func ExecuteBatch(ctx context.Context, db *sql.DB, statements []d1.QueryRequest) ([]d1.QueryResult, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("starting transaction: %w", err)
	}

	results := make([]d1.QueryResult, 0, len(statements))
	for i, stmt := range statements {
		res, err := execStatement(ctx, tx, stmt)
		if err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("statement %d failed: %w", i+1, err)
		}
		results = append(results, res)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}
	return results, nil
}

// execStatement runs a single SQL statement, returning rows for SELECT-style
// queries and affected-row counts for writes.
func execStatement(ctx context.Context, tx *sql.Tx, stmt d1.QueryRequest) (d1.QueryResult, error) {
	trimmed := strings.TrimSpace(stmt.SQL)
	if trimmed == "" {
		return d1.QueryResult{}, errors.New("empty sql statement")
	}

	if isReadStatement(trimmed) {
		return execQuery(ctx, tx, stmt)
	}
	return execWrite(ctx, tx, stmt)
}

// isReadStatement reports whether the statement returns rows.
func isReadStatement(sqlText string) bool {
	head := strings.ToUpper(strings.Fields(sqlText)[0])
	switch head {
	case "SELECT", "PRAGMA", "EXPLAIN", "WITH":
		return true
	default:
		return false
	}
}

// execQuery handles row-returning statements.
func execQuery(ctx context.Context, tx *sql.Tx, stmt d1.QueryRequest) (d1.QueryResult, error) {
	rows, err := tx.QueryContext(ctx, stmt.SQL, stmt.Params...)
	if err != nil {
		return d1.QueryResult{}, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return d1.QueryResult{}, err
	}

	out := make([]map[string]any, 0)
	var rowsRead int64
	for rows.Next() {
		scan := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range scan {
			ptrs[i] = &scan[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return d1.QueryResult{}, err
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			row[c] = normalizeValue(scan[i])
		}
		out = append(out, row)
		rowsRead++
	}
	if err := rows.Err(); err != nil {
		return d1.QueryResult{}, err
	}

	return d1.QueryResult{
		Results: out,
		Success: true,
		Meta:    d1.QueryMeta{RowsRead: rowsRead},
	}, nil
}

// execWrite handles INSERT/UPDATE/DELETE and other non-row statements.
func execWrite(ctx context.Context, tx *sql.Tx, stmt d1.QueryRequest) (d1.QueryResult, error) {
	res, err := tx.ExecContext(ctx, stmt.SQL, stmt.Params...)
	if err != nil {
		return d1.QueryResult{}, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		log.Printf("warning: could not read rows affected: %v", err)
	}
	return d1.QueryResult{
		Results: []map[string]any{},
		Success: true,
		Meta:    d1.QueryMeta{RowsWritten: affected},
	}, nil
}

// normalizeValue converts driver byte slices to strings for clean JSON output.
func normalizeValue(v any) any {
	switch val := v.(type) {
	case []byte:
		return string(val)
	default:
		return val
	}
}
