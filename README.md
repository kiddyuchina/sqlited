# sqlited

[![CI](https://github.com/kiddyuchina/sqlited/actions/workflows/ci.yml/badge.svg)](https://github.com/kiddyuchina/sqlited/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/badge/go-1.24%2B-blue)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-green)](LICENSE)

A lightweight, single-binary HTTP daemon that exposes **one or more local SQLite
files** through a **Cloudflare D1-compatible** JSON API. Download, run
`sqlited init`, then `sqlited run` -- no cloud, no dependencies, no build step.

```bash
# 1. Download the binary from GitHub Releases
# 2. Generate a config
sqlited init
# 3. Start serving all configured SQLite files
sqlited run
```

## Features

- **One binary, zero runtime dependencies** -- download and run; no package manager, no CGO, no external services.
- **Serve multiple SQLite files at once** -- one `sqlited` process can open many databases, each reachable by its own `db_key` and route.
- **CGO-free** -- built on the pure-Go driver [`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite), so it cross-compiles anywhere.
- **Atomic batches** -- an array of statements runs inside a single transaction and rolls back on any failure.
- **Standard library routing** -- uses Go 1.22+ `net/http` path patterns, no web framework.

## Quick Start

### 1. Download a release

Grab the binary for your platform from the [GitHub Releases](https://github.com/kiddyuchina/sqlited/releases) page. No installation step is required -- it is a single self-contained executable.

### 2. Generate a config

```bash
sqlited init
```

This writes `sqlited.json` to the current directory with a freshly generated,
cryptographically random 32-character `auth_token`. It refuses to overwrite an
existing file.

### 3. Start the server

```bash
sqlited run              # reads ./sqlited.json
sqlited run -config /etc/sqlited.json
```

### 4. Query the API

```bash
TOKEN=$(jq -r .auth_token sqlited.json)
curl -s http://localhost:4567/accounts/local/d1/database/app_prod/query \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '[{"sql":"SELECT 1 AS n","params":[]}]'
```

## CLI Commands

```
sqlited init        Generate a sqlited.json config in the current directory
sqlited run         Start the HTTP server
sqlited serve       Alias for run
sqlited help        Show help
```

## Configuration (`sqlited.json`)

Map as many SQLite files as you need under `databases`. Each key becomes the
`db_key` used in the URL:

```json
{
  "port": 4567,
  "auth_token": "a-secure-random-token",
  "databases": {
    "app_prod": {
      "path": "./data/app.db",
      "busy_timeout_ms": 5000,
      "wal_mode": true
    },
    "blog_db": {
      "path": "./data/blog.db",
      "busy_timeout_ms": 3000,
      "wal_mode": true
    }
  }
}
```

With this config, a single `sqlited run` opens both files and exposes them on
separate routes:

```
POST /accounts/local/d1/database/app_prod/query
POST /accounts/local/d1/database/blog_db/query
```

Each database is opened with `PRAGMA busy_timeout` set to `busy_timeout_ms`
(default `5000`) and, when `wal_mode` is `true`, `PRAGMA journal_mode=WAL` to
maximize concurrency and avoid "database is locked" errors. The parent
directory of each `path` must already exist.

## HTTP API

### Endpoint

```
POST /accounts/local/d1/database/{db_key}/query
Authorization: Bearer <auth_token>
Content-Type: application/json
```

- `{db_key}` must match a key under `databases` in the config, otherwise `404`.
- A missing or incorrect bearer token returns `401`.

There is also an unauthenticated `GET /health` liveness probe.

### Request body

A JSON array of statement objects (batch) or a single statement object:

```json
[
  { "sql": "INSERT INTO users (name) VALUES (?)", "params": ["Alice"] },
  { "sql": "UPDATE stats SET count = count + 1 WHERE id = 1", "params": [] }
]
```

Alternatively, send a single object without wrapping in an array:

```json
{ "sql": "SELECT 1 AS n", "params": [] }
```

If any statement in a batch fails, the transaction is rolled back and an error
response is returned; otherwise it is committed.

### Response body (D1 format)

```json
{
  "result": [
    {
      "results": [],
      "success": true,
      "meta": { "rows_read": 0, "rows_written": 1 }
    }
  ],
  "success": true,
  "errors": [],
  "messages": []
}
```

`SELECT`/`PRAGMA`/`EXPLAIN`/`WITH` statements populate `results` with row
objects and `meta.rows_read`; write statements populate `meta.rows_written`.

## Project Structure

```
.
├── cmd/sqlited/main.go          # entry point
├── internal/
│   ├── d1/                      # D1 JSON types
│   ├── config/                  # config loading & init
│   ├── sqlite/                  # SQLite connection & batch execution
│   └── server/                  # HTTP routing, auth, D1 handlers
├── go.mod
├── go.sum
├── Makefile
├── LICENSE
├── .gitignore
├── README.md
└── .github/workflows/ci.yml
```

## Building from Source (for development)

If you want to hack on sqlited:

```bash
# Build
go build -o sqlited ./cmd/sqlited

# Or with Make
make build

# Tests
make test
make integration
make lint
```

Requires Go 1.22+ (developed and tested on Go 1.24). The project is intentionally
CGO-free, so the default test targets do not use `-race`. Use `make test-race` if
you enable CGO.

## Notes

- The SQLite driver chosen is **`modernc.org/sqlite`** (pure Go, no CGO).
- Errors return the D1 envelope with `success: false` and a populated `errors`
  array.
