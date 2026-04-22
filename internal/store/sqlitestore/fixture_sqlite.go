//go:build sqlite || sqliteonly

package sqlitestore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// SQLiteFixtureStore implements store.FixtureStore backed by SQLite.
type SQLiteFixtureStore struct {
	db *sql.DB
}

func NewSQLiteFixtureStore(db *sql.DB) *SQLiteFixtureStore { return &SQLiteFixtureStore{db: db} }

var _ store.FixtureStore = (*SQLiteFixtureStore)(nil)

func (s *SQLiteFixtureStore) Insert(ctx context.Context, f store.Fixture) error {
	tid, err := requireTenantID(ctx)
	if err != nil {
		return fmt.Errorf("fixture.Insert: %w", err)
	}
	if f.ID == uuid.Nil {
		f.ID = uuid.Must(uuid.NewV7())
	}
	if f.CapturedAt.IsZero() {
		f.CapturedAt = time.Now().UTC()
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO llm_fixtures
			(id,tenant_id,agent_id,session_key,span_id,provider,model,
			 request_body,response_body,tool_call_count,input_tokens,output_tokens,
			 total_cost_usd,latency_ms,status,error,tags,metadata,captured_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		f.ID.String(), tid.String(), uuidPtrStr(f.AgentID), f.SessionKey, uuidPtrStr(f.SpanID),
		f.Provider, f.Model,
		jsonOrEmpty(f.RequestBody), jsonOrNull(f.ResponseBody),
		f.ToolCallCount, f.InputTokens, f.OutputTokens,
		f.TotalCostUSD, f.LatencyMs, f.Status, f.Error,
		jsonStringArray(f.Tags), jsonOrEmpty(f.Metadata),
		f.CapturedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

// BatchInsert inserts multiple fixtures atomically in chunks of 500.
func (s *SQLiteFixtureStore) BatchInsert(ctx context.Context, fs []store.Fixture) error {
	if len(fs) == 0 {
		return nil
	}
	tid, err := requireTenantID(ctx)
	if err != nil {
		return fmt.Errorf("fixture.BatchInsert: %w", err)
	}
	const chunkSize = 500
	for i := 0; i < len(fs); i += chunkSize {
		end := i + chunkSize
		if end > len(fs) {
			end = len(fs)
		}
		if err := s.batchChunk(ctx, tid, fs[i:end]); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteFixtureStore) batchChunk(ctx context.Context, tid uuid.UUID, fs []store.Fixture) error {
	ph := make([]string, len(fs))
	args := make([]any, 0, len(fs)*19)
	for i, f := range fs {
		if f.ID == uuid.Nil {
			f.ID = uuid.Must(uuid.NewV7())
		}
		if f.CapturedAt.IsZero() {
			f.CapturedAt = time.Now().UTC()
		}
		ph[i] = "(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)"
		args = append(args,
			f.ID.String(), tid.String(), uuidPtrStr(f.AgentID), f.SessionKey, uuidPtrStr(f.SpanID),
			f.Provider, f.Model,
			jsonOrEmpty(f.RequestBody), jsonOrNull(f.ResponseBody),
			f.ToolCallCount, f.InputTokens, f.OutputTokens,
			f.TotalCostUSD, f.LatencyMs, f.Status, f.Error,
			jsonStringArray(f.Tags), jsonOrEmpty(f.Metadata),
			f.CapturedAt.UTC().Format(time.RFC3339Nano),
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

func (s *SQLiteFixtureStore) Get(ctx context.Context, id uuid.UUID) (*store.Fixture, error) {
	tid, err := requireTenantID(ctx)
	if err != nil {
		return nil, fmt.Errorf("fixture.Get: %w", err)
	}
	row := s.db.QueryRowContext(ctx, sqliteFixtureSelect+
		` FROM llm_fixtures WHERE id = ? AND tenant_id = ?`, id.String(), tid.String())
	return scanSQLiteFixture(row)
}

func (s *SQLiteFixtureStore) List(ctx context.Context, filter store.FixtureListFilter) ([]store.Fixture, string, error) {
	tid, err := requireTenantID(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("fixture.List: %w", err)
	}
	limit := clampFixtureLimit(filter.Limit)
	where := []string{"tenant_id = ?"}
	args := []any{tid.String()}

	add := func(v any) string { args = append(args, v); return "?" }

	if filter.AgentID != nil {
		where = append(where, "agent_id = "+add(filter.AgentID.String()))
	}
	if filter.Status != nil {
		where = append(where, "status = "+add(*filter.Status))
	}
	if filter.Since != nil {
		where = append(where, "captured_at >= "+add(filter.Since.UTC().Format(time.RFC3339Nano)))
	}
	if filter.Until != nil {
		where = append(where, "captured_at <= "+add(filter.Until.UTC().Format(time.RFC3339Nano)))
	}
	// SQLite has no native array — use JSON LIKE for tag filtering (low-cardinality OK).
	for _, tag := range filter.Tags {
		where = append(where, "tags LIKE "+add("%\""+strings.ReplaceAll(tag, `"`, `\"`)+"\"%"))
	}
	if filter.Cursor != "" {
		ca, cid, cerr := store.DecodeFixtureCursor(filter.Cursor)
		if cerr == nil {
			where = append(where, "(captured_at < ? OR (captured_at = ? AND id < ?))")
			args = append(args,
				ca.UTC().Format(time.RFC3339Nano),
				ca.UTC().Format(time.RFC3339Nano),
				cid.String(),
			)
		}
	}

	q := sqliteFixtureSelect + ` FROM llm_fixtures WHERE ` +
		strings.Join(where, " AND ") +
		fmt.Sprintf(" ORDER BY captured_at DESC, id DESC LIMIT %d", limit+1)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("fixture.List: %w", err)
	}
	defer rows.Close()

	var fixtures []store.Fixture
	for rows.Next() {
		f, err := scanSQLiteFixture(rows)
		if err != nil {
			return nil, "", err
		}
		fixtures = append(fixtures, *f)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var nextCursor string
	if len(fixtures) > limit {
		last := fixtures[limit-1]
		nextCursor = store.EncodeFixtureCursor(last.CapturedAt, last.ID)
		fixtures = fixtures[:limit]
	}
	return fixtures, nextCursor, nil
}

func (s *SQLiteFixtureStore) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	tid, err := requireTenantID(ctx)
	if err != nil {
		return 0, fmt.Errorf("fixture.DeleteOlderThan: %w", err)
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM llm_fixtures WHERE tenant_id = ? AND captured_at < ?`,
		tid.String(), cutoff.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("fixture.DeleteOlderThan: %w", err)
	}
	return res.RowsAffected()
}

func (s *SQLiteFixtureStore) CountByAgent(ctx context.Context, filter store.FixtureListFilter) (map[uuid.UUID]int64, error) {
	tid, err := requireTenantID(ctx)
	if err != nil {
		return nil, fmt.Errorf("fixture.CountByAgent: %w", err)
	}
	where := []string{"tenant_id = ?"}
	args := []any{tid.String()}
	add := func(v any) string { args = append(args, v); return "?" }
	if filter.Since != nil {
		where = append(where, "captured_at >= "+add(filter.Since.UTC().Format(time.RFC3339Nano)))
	}
	if filter.Until != nil {
		where = append(where, "captured_at <= "+add(filter.Until.UTC().Format(time.RFC3339Nano)))
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT agent_id, count(*) FROM llm_fixtures WHERE `+
			strings.Join(where, " AND ")+` GROUP BY agent_id`, args...)
	if err != nil {
		return nil, fmt.Errorf("fixture.CountByAgent: %w", err)
	}
	defer rows.Close()
	result := map[uuid.UUID]int64{}
	for rows.Next() {
		var agentStr *string
		var count int64
		if err := rows.Scan(&agentStr, &count); err != nil {
			return nil, err
		}
		var aid uuid.UUID
		if agentStr != nil {
			aid, _ = uuid.Parse(*agentStr)
		}
		result[aid] = count
	}
	return result, rows.Err()
}

const sqliteFixtureSelect = `SELECT id,tenant_id,agent_id,session_key,span_id,provider,model,
	request_body,response_body,tool_call_count,input_tokens,output_tokens,
	total_cost_usd,latency_ms,status,error,tags,metadata,captured_at`

type sqliteFixtureScanner interface{ Scan(dest ...any) error }

func scanSQLiteFixture(sc sqliteFixtureScanner) (*store.Fixture, error) {
	var f store.Fixture
	var idStr, tidStr string
	var agentStr, spanStr, sessionKey, errorStr *string
	var reqBody, respBody, metadata, tagsJSON []byte
	var capturedAt sqliteTime

	err := sc.Scan(
		&idStr, &tidStr, &agentStr, &sessionKey, &spanStr,
		&f.Provider, &f.Model,
		&reqBody, &respBody,
		&f.ToolCallCount, &f.InputTokens, &f.OutputTokens,
		&f.TotalCostUSD, &f.LatencyMs, &f.Status, &errorStr,
		&tagsJSON, &metadata, &capturedAt,
	)
	if err != nil {
		return nil, err
	}
	f.ID, _ = uuid.Parse(idStr)
	f.TenantID, _ = uuid.Parse(tidStr)
	if agentStr != nil {
		if aid, err := uuid.Parse(*agentStr); err == nil {
			f.AgentID = &aid
		}
	}
	if spanStr != nil {
		if sid, err := uuid.Parse(*spanStr); err == nil {
			f.SpanID = &sid
		}
	}
	f.SessionKey = sessionKey
	f.Error = errorStr
	f.RequestBody = reqBody
	f.ResponseBody = respBody
	f.Metadata = metadata
	f.CapturedAt = capturedAt.Time
	scanJSONStringArray(tagsJSON, &f.Tags)
	return &f, nil
}

// uuidPtrStr converts *uuid.UUID to *string for SQLite TEXT storage.
func uuidPtrStr(u *uuid.UUID) *string {
	if u == nil || *u == uuid.Nil {
		return nil
	}
	s := u.String()
	return &s
}

func clampFixtureLimit(n int) int {
	if n <= 0 {
		return 100
	}
	if n > 1000 {
		return 1000
	}
	return n
}
