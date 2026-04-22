//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/internal/store/pg"
)

func fixtureCtx(tenantID uuid.UUID) context.Context {
	return store.WithTenantID(context.Background(), tenantID)
}

func makeTestFixture(tid uuid.UUID) store.Fixture {
	return store.Fixture{
		ID:          uuid.Must(uuid.NewV7()),
		TenantID:    tid,
		Provider:    "anthropic",
		Model:       "claude-sonnet-4-20250514",
		RequestBody: []byte(`{"messages":[]}`),
		Status:      "ok",
		Tags:        []string{"integration", "test"},
		Metadata:    []byte(`{}`),
		CapturedAt:  time.Now().UTC(),
	}
}

func TestFixtureStorePG_InsertGet(t *testing.T) {
	db := testDB(t)
	s := pg.NewPGFixtureStore(db)
	tid, _ := seedTenantAgent(t, db)
	ctx := fixtureCtx(tid)

	f := makeTestFixture(tid)
	f.InputTokens = 55
	f.OutputTokens = 120
	f.TotalCostUSD = 0.002
	f.Tags = []string{"pg", "roundtrip"}

	if err := s.Insert(ctx, f); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, err := s.Get(ctx, f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.InputTokens != 55 {
		t.Errorf("InputTokens = %d, want 55", got.InputTokens)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "pg" {
		t.Errorf("Tags = %v", got.Tags)
	}
}

func TestFixtureStorePG_TenantIsolation(t *testing.T) {
	db := testDB(t)
	s := pg.NewPGFixtureStore(db)
	tidA, _ := seedTenantAgent(t, db)
	tidB, _ := seedTenantAgent(t, db)

	ctxA := fixtureCtx(tidA)
	f := makeTestFixture(tidA)
	if err := s.Insert(ctxA, f); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Tenant B should not see tenant A's fixture.
	ctxB := fixtureCtx(tidB)
	got, err := s.Get(ctxB, f.ID)
	if err == nil && got != nil {
		t.Error("cross-tenant Get returned a row — tenant isolation broken")
	}
}

func TestFixtureStorePG_BatchInsert(t *testing.T) {
	db := testDB(t)
	s := pg.NewPGFixtureStore(db)
	tid, _ := seedTenantAgent(t, db)
	ctx := fixtureCtx(tid)

	fs := make([]store.Fixture, 10)
	for i := range fs {
		fs[i] = makeTestFixture(tid)
	}
	if err := s.BatchInsert(ctx, fs); err != nil {
		t.Fatalf("BatchInsert: %v", err)
	}
	res, _, err := s.List(ctx, store.FixtureListFilter{Limit: 20})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res) < 10 {
		t.Errorf("got %d rows, want ≥10", len(res))
	}
}

func TestFixtureStorePG_Pagination(t *testing.T) {
	db := testDB(t)
	s := pg.NewPGFixtureStore(db)
	tid, _ := seedTenantAgent(t, db)
	ctx := fixtureCtx(tid)

	base := time.Now().UTC().Add(-25 * time.Minute)
	for i := range 25 {
		f := makeTestFixture(tid)
		f.CapturedAt = base.Add(time.Duration(i) * time.Minute)
		if err := s.Insert(ctx, f); err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
	}

	seen := map[uuid.UUID]bool{}
	cursor := ""
	for {
		res, next, err := s.List(ctx, store.FixtureListFilter{Limit: 10, Cursor: cursor})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		for _, f := range res {
			if seen[f.ID] {
				t.Errorf("duplicate fixture %s", f.ID)
			}
			seen[f.ID] = true
		}
		cursor = next
		if next == "" {
			break
		}
	}
	if len(seen) < 25 {
		t.Errorf("total seen = %d, want ≥25", len(seen))
	}
}

func TestFixtureStorePG_DeleteOlderThan(t *testing.T) {
	db := testDB(t)
	s := pg.NewPGFixtureStore(db)
	tid, _ := seedTenantAgent(t, db)
	ctx := fixtureCtx(tid)
	now := time.Now().UTC()

	for _, offset := range []time.Duration{0, -24 * time.Hour, -48 * time.Hour} {
		f := makeTestFixture(tid)
		f.CapturedAt = now.Add(offset)
		s.Insert(ctx, f)
	}

	n, err := s.DeleteOlderThan(ctx, now.Add(-30*time.Hour))
	if err != nil {
		t.Fatalf("DeleteOlderThan: %v", err)
	}
	if n != 1 {
		t.Errorf("deleted %d rows, want 1", n)
	}
}
