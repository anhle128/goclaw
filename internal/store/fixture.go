package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// FixtureStore persists raw LLM request/response pairs for evaluation and replay.
// All methods are tenant-scoped via TenantIDFromContext.
type FixtureStore interface {
	Insert(ctx context.Context, f Fixture) error
	BatchInsert(ctx context.Context, fs []Fixture) error
	Get(ctx context.Context, id uuid.UUID) (*Fixture, error)
	List(ctx context.Context, filter FixtureListFilter) ([]Fixture, string, error)
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
	CountByAgent(ctx context.Context, filter FixtureListFilter) (map[uuid.UUID]int64, error)
}

// Fixture captures a single LLM call for evaluation or replay.
type Fixture struct {
	ID            uuid.UUID
	TenantID      uuid.UUID
	AgentID       *uuid.UUID
	SessionKey    *string
	SpanID        *uuid.UUID
	Provider      string
	Model         string
	RequestBody   json.RawMessage
	ResponseBody  json.RawMessage // nil on error
	ToolCallCount int
	InputTokens   int
	OutputTokens  int
	TotalCostUSD  float64
	LatencyMs     int
	Status        string // "ok" | "error" | "cancelled"
	Error         *string
	Tags          []string
	Metadata      json.RawMessage
	CapturedAt    time.Time
}

// FixtureListFilter filters List and CountByAgent queries.
type FixtureListFilter struct {
	AgentID *uuid.UUID
	Since   *time.Time
	Until   *time.Time
	Tags    []string // all tags must match (AND semantics)
	Status  *string
	Limit   int    // default 100, max 1000
	Cursor  string // opaque keyset cursor from prior List call
}

// fixtureCursor encodes the keyset position for pagination.
type fixtureCursor struct {
	CapturedAt time.Time `json:"ca"`
	ID         uuid.UUID `json:"id"`
}

// EncodeFixtureCursor serialises (capturedAt, id) to an opaque base64-JSON cursor.
func EncodeFixtureCursor(capturedAt time.Time, id uuid.UUID) string {
	b, _ := json.Marshal(fixtureCursor{CapturedAt: capturedAt, ID: id})
	return base64.StdEncoding.EncodeToString(b)
}

// DecodeFixtureCursor parses an opaque cursor back to (capturedAt, id).
func DecodeFixtureCursor(cursor string) (time.Time, uuid.UUID, error) {
	b, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	var c fixtureCursor
	if err := json.Unmarshal(b, &c); err != nil {
		return time.Time{}, uuid.Nil, err
	}
	return c.CapturedAt, c.ID, nil
}
