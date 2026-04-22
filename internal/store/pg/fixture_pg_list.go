package pg

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

func (s *PGFixtureStore) List(ctx context.Context, filter store.FixtureListFilter) ([]store.Fixture, string, error) {
	tid := store.TenantIDFromContext(ctx)
	if tid == uuid.Nil {
		return nil, "", fmt.Errorf("fixture.List: tenant_id required")
	}
	limit := clampLimit(filter.Limit)

	where := []string{"tenant_id = $1"}
	args := []any{tid}

	addArg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	if filter.AgentID != nil {
		where = append(where, "agent_id = "+addArg(*filter.AgentID))
	}
	if filter.Status != nil {
		where = append(where, "status = "+addArg(*filter.Status))
	}
	if filter.Since != nil {
		where = append(where, "captured_at >= "+addArg(filter.Since.UTC()))
	}
	if filter.Until != nil {
		where = append(where, "captured_at <= "+addArg(filter.Until.UTC()))
	}
	for _, tag := range filter.Tags {
		where = append(where, addArg(tag)+" = ANY(tags)")
	}
	if filter.Cursor != "" {
		ca, cid, err := store.DecodeFixtureCursor(filter.Cursor)
		if err == nil {
			p1, p2 := addArg(ca.UTC()), addArg(cid)
			where = append(where, fmt.Sprintf("(captured_at,id) < (%s,%s)", p1, p2))
		}
	}

	q := fixtureSelectCols + ` FROM llm_fixtures WHERE ` +
		strings.Join(where, " AND ") +
		fmt.Sprintf(" ORDER BY captured_at DESC, id DESC LIMIT %d", limit+1)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("fixture.List: %w", err)
	}
	defer rows.Close()

	var fixtures []store.Fixture
	for rows.Next() {
		f, err := scanPGFixture(rows)
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

func (s *PGFixtureStore) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	tid := store.TenantIDFromContext(ctx)
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM llm_fixtures WHERE tenant_id = $1 AND captured_at < $2`, tid, cutoff.UTC())
	if err != nil {
		return 0, fmt.Errorf("fixture.DeleteOlderThan: %w", err)
	}
	return res.RowsAffected()
}

func (s *PGFixtureStore) CountByAgent(ctx context.Context, filter store.FixtureListFilter) (map[uuid.UUID]int64, error) {
	tid := store.TenantIDFromContext(ctx)
	where := []string{"tenant_id = $1"}
	args := []any{tid}
	addArg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if filter.Since != nil {
		where = append(where, "captured_at >= "+addArg(filter.Since.UTC()))
	}
	if filter.Until != nil {
		where = append(where, "captured_at <= "+addArg(filter.Until.UTC()))
	}
	q := `SELECT agent_id, count(*) FROM llm_fixtures WHERE ` +
		strings.Join(where, " AND ") + ` GROUP BY agent_id`
	rows, err := s.db.QueryContext(ctx, q, args...)
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

func clampLimit(n int) int {
	if n <= 0 {
		return 100
	}
	if n > 1000 {
		return 1000
	}
	return n
}
