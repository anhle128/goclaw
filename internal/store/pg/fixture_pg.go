package pg

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// PGFixtureStore implements store.FixtureStore backed by PostgreSQL.
type PGFixtureStore struct {
	db *sql.DB
}

func NewPGFixtureStore(db *sql.DB) *PGFixtureStore { return &PGFixtureStore{db: db} }

var _ store.FixtureStore = (*PGFixtureStore)(nil)

func (s *PGFixtureStore) Insert(ctx context.Context, f store.Fixture) error {
	tid := store.TenantIDFromContext(ctx)
	if tid == uuid.Nil {
		return fmt.Errorf("fixture.Insert: tenant_id required")
	}
	if f.ID == uuid.Nil {
		f.ID = uuid.Must(uuid.NewV7())
	}
	if f.CapturedAt.IsZero() {
		f.CapturedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO llm_fixtures
			(id,tenant_id,agent_id,session_key,span_id,provider,model,
			 request_body,response_body,tool_call_count,input_tokens,output_tokens,
			 total_cost_usd,latency_ms,status,error,tags,metadata,captured_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		f.ID, tid, nilUUID(f.AgentID), f.SessionKey, nilUUID(f.SpanID),
		f.Provider, f.Model,
		jsonOrEmpty(f.RequestBody), jsonOrNull(f.ResponseBody),
		f.ToolCallCount, f.InputTokens, f.OutputTokens,
		f.TotalCostUSD, f.LatencyMs, f.Status, f.Error,
		pqStringArray(f.Tags), jsonOrEmpty(f.Metadata), f.CapturedAt,
	)
	return err
}

// BatchInsert inserts multiple fixtures in single-statement chunks of 500.
// The entire batch is atomic — any chunk failure rolls back that chunk.
func (s *PGFixtureStore) BatchInsert(ctx context.Context, fs []store.Fixture) error {
	if len(fs) == 0 {
		return nil
	}
	tid := store.TenantIDFromContext(ctx)
	if tid == uuid.Nil {
		return fmt.Errorf("fixture.BatchInsert: tenant_id required")
	}
	const chunkSize = 500
	for i := 0; i < len(fs); i += chunkSize {
		end := min(i+chunkSize, len(fs))
		if err := s.batchChunk(ctx, tid, fs[i:end]); err != nil {
			return err
		}
	}
	return nil
}

func (s *PGFixtureStore) batchChunk(ctx context.Context, tid uuid.UUID, fs []store.Fixture) error {
	const cols = 19
	ph := make([]string, len(fs))
	args := make([]any, 0, len(fs)*cols)
	for i, f := range fs {
		if f.ID == uuid.Nil {
			f.ID = uuid.Must(uuid.NewV7())
		}
		if f.CapturedAt.IsZero() {
			f.CapturedAt = time.Now().UTC()
		}
		b := i * cols
		ph[i] = fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			b+1, b+2, b+3, b+4, b+5, b+6, b+7, b+8, b+9, b+10,
			b+11, b+12, b+13, b+14, b+15, b+16, b+17, b+18, b+19)
		args = append(args,
			f.ID, tid, nilUUID(f.AgentID), f.SessionKey, nilUUID(f.SpanID),
			f.Provider, f.Model,
			jsonOrEmpty(f.RequestBody), jsonOrNull(f.ResponseBody),
			f.ToolCallCount, f.InputTokens, f.OutputTokens,
			f.TotalCostUSD, f.LatencyMs, f.Status, f.Error,
			pqStringArray(f.Tags), jsonOrEmpty(f.Metadata), f.CapturedAt,
		)
	}
	q := `INSERT INTO llm_fixtures
		(id,tenant_id,agent_id,session_key,span_id,provider,model,
		 request_body,response_body,tool_call_count,input_tokens,output_tokens,
		 total_cost_usd,latency_ms,status,error,tags,metadata,captured_at)
		VALUES ` + strings.Join(ph, ",")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, q, args...); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("fixture.batchChunk: %w", err)
	}
	return tx.Commit()
}

func (s *PGFixtureStore) Get(ctx context.Context, id uuid.UUID) (*store.Fixture, error) {
	tid := store.TenantIDFromContext(ctx)
	row := s.db.QueryRowContext(ctx, fixtureSelectCols+
		` FROM llm_fixtures WHERE id = $1 AND tenant_id = $2`, id, tid)
	return scanPGFixture(row)
}

// fixtureSelectCols is the shared SELECT column list for all fixture queries.
const fixtureSelectCols = `SELECT id,tenant_id,agent_id,session_key,span_id,provider,model,
	request_body,response_body,tool_call_count,input_tokens,output_tokens,
	total_cost_usd,latency_ms,status,error,tags,metadata,captured_at`

type fixtureScanner interface{ Scan(dest ...any) error }

func scanPGFixture(s fixtureScanner) (*store.Fixture, error) {
	var f store.Fixture
	var agentID, spanID uuid.UUID
	var agentIDOk, spanIDOk bool
	var reqBody, respBody, metadata []byte
	var tagsBytes []byte

	// Scan nullable UUID via string
	var agentStr, spanStr *string

	err := s.Scan(
		&f.ID, &f.TenantID, &agentStr, &f.SessionKey, &spanStr,
		&f.Provider, &f.Model,
		&reqBody, &respBody,
		&f.ToolCallCount, &f.InputTokens, &f.OutputTokens,
		&f.TotalCostUSD, &f.LatencyMs, &f.Status, &f.Error,
		&tagsBytes, &metadata, &f.CapturedAt,
	)
	if err != nil {
		return nil, err
	}
	if agentStr != nil {
		if agentID, err = uuid.Parse(*agentStr); err == nil {
			agentIDOk = true
		}
	}
	if spanStr != nil {
		if spanID, err = uuid.Parse(*spanStr); err == nil {
			spanIDOk = true
		}
	}
	if agentIDOk {
		f.AgentID = &agentID
	}
	if spanIDOk {
		f.SpanID = &spanID
	}
	f.RequestBody = reqBody
	f.ResponseBody = respBody
	f.Metadata = metadata
	scanStringArray(tagsBytes, &f.Tags)
	return &f, nil
}
