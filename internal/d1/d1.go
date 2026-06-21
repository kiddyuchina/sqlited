// Package d1 defines the Cloudflare D1-compatible JSON request/response types.
package d1

// QueryRequest is a single SQL statement plus its bound parameters.
type QueryRequest struct {
	SQL    string `json:"sql"`
	Params []any  `json:"params"`
}

// QueryMeta mirrors the subset of D1 meta fields that we populate.
type QueryMeta struct {
	RowsRead    int64 `json:"rows_read"`
	RowsWritten int64 `json:"rows_written"`
}

// QueryResult is the result of executing a single statement.
type QueryResult struct {
	Results []map[string]any `json:"results"`
	Success bool             `json:"success"`
	Meta    QueryMeta        `json:"meta"`
}

// Response is the top-level envelope returned to the client.
type Response struct {
	Result   []QueryResult `json:"result"`
	Success  bool          `json:"success"`
	Errors   []Message     `json:"errors"`
	Messages []Message     `json:"messages"`
}

// Message represents an entry in the errors/messages arrays.
type Message struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
