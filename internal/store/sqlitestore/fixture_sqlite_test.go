//go:build sqlite || sqliteonly

package sqlitestore

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

func newFixtureCtx(tenantID uuid.UUID) context.Context {
	return store.WithTenantID(context.Background(), tenantID)
}

// openFixtureDB opens a fresh in-mem DB, applies schema, and seeds a tenant.
// Returns store, raw db (for seeding FK rows), and tenant UUID.
func openFixtureDB(t *testing.T) (*SQLiteFixtureStore, *sql.DB, uuid.UUID) {
	t.Helper()
	db := openTestDB(t)
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	tid := uuid.MustParse("00000000-0000-0000-0000-000000000099")
	db.Exec(`INSERT OR IGNORE INTO tenants (id,name,slug,status) VALUES (?,'T','t','active')`, tid.String())
	return NewSQLiteFixtureStore(db), db, tid
}

// seedTestAgent inserts a minimal agent row to satisfy agent_id FK.
func seedTestAgent(t *testing.T, db *sql.DB, tid uuid.UUID) uuid.UUID {
	t.Helper()
	agentID := uuid.Must(uuid.NewV7())
	// Use agent ID as key suffix to avoid UNIQUE(tenant_id, agent_key) collisions.
	key := "test-" + agentID.String()
	if _, err := db.Exec(
		`INSERT INTO agents (id,tenant_id,agent_key,display_name,provider,model,agent_type,owner_id)
		 VALUES (?,?,?,?,'anthropic','claude-sonnet-4','predefined','owner-1')`,
		agentID.String(), tid.String(), key, key,
	); err != nil {
		t.Fatalf("seedTestAgent: %v", err)
	}
	return agentID
}

func makeFixture(tid uuid.UUID) store.Fixture {
	return store.Fixture{
		ID:          uuid.Must(uuid.NewV7()),
		TenantID:    tid,
		Provider:    "anthropic",
		Model:       "claude-sonnet-4",
		RequestBody: []byte(`{"messages":[]}`),
		Status:      "ok",
		Tags:        []string{"eval", "test"},
		Metadata:    []byte(`{}`),
		CapturedAt:  time.Now().UTC().Truncate(time.Second),
	}
}

func TestFixtureInsertGet_Roundtrip(t *testing.T) {
	s, _, tid := openFixtureDB(t)
	ctx := newFixtureCtx(tid)
	f := makeFixture(tid)
	f.InputTokens = 42
	f.OutputTokens = 100
	f.TotalCostUSD = 0.001234

	if err := s.Insert(ctx, f); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, err := s.Get(ctx, f.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != f.ID {
		t.Errorf("ID mismatch")
	}
	if got.Provider != "anthropic" {
		t.Errorf("Provider = %q", got.Provider)
	}
	if got.InputTokens != 42 {
		t.Errorf("InputTokens = %d, want 42", got.InputTokens)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "eval" {
		t.Errorf("Tags = %v", got.Tags)
	}
}

func TestFixtureInsert_TenantScoped(t *testing.T) {
	s, _, tid := openFixtureDB(t)
	ctx := newFixtureCtx(tid)
	f := makeFixture(tid)
	if err := s.Insert(ctx, f); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Get with different tenant must return nothing.
	otherCtx := newFixtureCtx(uuid.New())
	got, _ := s.Get(otherCtx, f.ID)
	if got != nil {
		t.Error("cross-tenant Get returned row — tenant isolation broken")
	}
}

func TestFixtureList_Pagination(t *testing.T) {
	s, _, tid := openFixtureDB(t)
	ctx := newFixtureCtx(tid)

	base := time.Now().UTC().Add(-25 * time.Minute).Truncate(time.Second)
	for i := range 25 {
		f := makeFixture(tid)
		f.ID = uuid.Must(uuid.NewV7())
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
	if len(seen) != 25 {
		t.Errorf("total seen = %d, want 25", len(seen))
	}
}

func TestFixtureList_FilterByAgent(t *testing.T) {
	s, db, tid := openFixtureDB(t)
	ctx := newFixtureCtx(tid)
	agentID := seedTestAgent(t, db, tid) // real agent row required by FK

	f1 := makeFixture(tid)
	f1.AgentID = &agentID
	f2 := makeFixture(tid)

	if err := s.Insert(ctx, f1); err != nil {
		t.Fatalf("Insert f1: %v", err)
	}
	if err := s.Insert(ctx, f2); err != nil {
		t.Fatalf("Insert f2: %v", err)
	}

	res, _, err := s.List(ctx, store.FixtureListFilter{AgentID: &agentID})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res) != 1 || res[0].ID != f1.ID {
		t.Errorf("expected 1 result for agent filter, got %d", len(res))
	}
}

func TestFixtureList_FilterByTags(t *testing.T) {
	s, _, tid := openFixtureDB(t)
	ctx := newFixtureCtx(tid)
	f1 := makeFixture(tid)
	f1.Tags = []string{"langfuse", "prod"}
	f2 := makeFixture(tid)
	f2.Tags = []string{"dev"}

	s.Insert(ctx, f1)
	s.Insert(ctx, f2)

	res, _, err := s.List(ctx, store.FixtureListFilter{Tags: []string{"langfuse"}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res) != 1 {
		t.Errorf("tag filter: got %d results, want 1", len(res))
	}
}

func TestFixtureBatchInsert_Atomic(t *testing.T) {
	s, _, tid := openFixtureDB(t)
	ctx := newFixtureCtx(tid)

	fs := make([]store.Fixture, 5)
	for i := range fs {
		fs[i] = makeFixture(tid)
	}
	// Duplicate first ID to force UNIQUE constraint violation on the batch.
	fs[4].ID = fs[0].ID

	if err := s.BatchInsert(ctx, fs); err == nil {
		t.Fatal("expected BatchInsert to fail on duplicate PK, but it succeeded")
	}

	// Transaction must have rolled back — no rows committed.
	var count int
	s.db.QueryRowContext(ctx, "SELECT count(*) FROM llm_fixtures WHERE tenant_id = ?", tid.String()).Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 rows after failed batch, got %d", count)
	}
}

func TestFixtureDeleteOlderThan(t *testing.T) {
	s, _, tid := openFixtureDB(t)
	ctx := newFixtureCtx(tid)
	now := time.Now().UTC()

	for _, offset := range []time.Duration{0, -24 * time.Hour, -48 * time.Hour} {
		f := makeFixture(tid)
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
	var remaining int
	s.db.QueryRowContext(ctx, "SELECT count(*) FROM llm_fixtures WHERE tenant_id = ?", tid.String()).Scan(&remaining)
	if remaining != 2 {
		t.Errorf("remaining = %d, want 2", remaining)
	}
}

func TestFixtureCountByAgent(t *testing.T) {
	s, db, tid := openFixtureDB(t)
	ctx := newFixtureCtx(tid)
	a1 := seedTestAgent(t, db, tid)
	a2 := seedTestAgent(t, db, tid)

	for range 3 {
		f := makeFixture(tid)
		f.AgentID = &a1
		if err := s.Insert(ctx, f); err != nil {
			t.Fatalf("Insert a1: %v", err)
		}
	}
	for range 2 {
		f := makeFixture(tid)
		f.AgentID = &a2
		if err := s.Insert(ctx, f); err != nil {
			t.Fatalf("Insert a2: %v", err)
		}
	}

	counts, err := s.CountByAgent(ctx, store.FixtureListFilter{})
	if err != nil {
		t.Fatalf("CountByAgent: %v", err)
	}
	if counts[a1] != 3 {
		t.Errorf("agent1 count = %d, want 3", counts[a1])
	}
	if counts[a2] != 2 {
		t.Errorf("agent2 count = %d, want 2", counts[a2])
	}
}
