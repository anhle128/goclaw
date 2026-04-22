# Phase 02 — Store Interface + PG/SQLite Implementations

## Context Links
- `internal/store/` — interface definitions (see e.g. `hooks.go`, `session.go` for style)
- `internal/store/pg/` — PG impls using `database/sql` + `sqlx` + `pgx/v5/stdlib`
- `internal/store/sqlitestore/` — SQLite impls using `modernc.org/sqlite`
- `internal/store/base/helpers.go` — `BuildMapUpdate`, `BuildScopeClause`, `NilStr`
- `internal/store/base/tenant.go` — tenant context helpers
- Phase 01 schema (pre-req, must merge first)

## Overview
- **Priority:** P0
- **Status:** complete
- **Brief:** Define `store.FixtureStore` interface, implement PG + SQLite variants. All methods tenant-scoped. TDD-first.

## Key Insights
- Shared Dialect pattern means PG/SQLite impls share query-building logic; divergence limited to JSONB vs JSON-text and array vs JSON-text tags.
- List queries MUST paginate (keyset on `captured_at, id`) to avoid loading 100k rows.
- Insert is the hot path — keep it minimal, no round-trips.

## Requirements
**Functional**
- `Insert(ctx, Fixture) error` — single-row insert.
- `BatchInsert(ctx, []Fixture) error` — for async flush (Phase 03 uses this).
- `List(ctx, ListFilter) ([]Fixture, string /*nextCursor*/, error)` — keyset pagination.
- `Get(ctx, id) (*Fixture, error)`.
- `DeleteOlderThan(ctx, t time.Time) (int64, error)` — used by retention worker (Phase 07).
- `CountByAgent(ctx, filter) (map[uuid.UUID]int64, error)` — CLI `fixture list` summary.

**Non-functional**
- All methods scoped via `store.TenantIDFromContext(ctx)` WHERE clause.
- `BatchInsert` single SQL statement (multi-row VALUES) to minimise round-trips.
- Zero allocations on the scan hot-path beyond decoded JSON.

## Architecture
### Interface
```go
// internal/store/fixture.go
type FixtureStore interface {
    Insert(ctx context.Context, f Fixture) error
    BatchInsert(ctx context.Context, fs []Fixture) error
    Get(ctx context.Context, id uuid.UUID) (*Fixture, error)
    List(ctx context.Context, filter FixtureListFilter) ([]Fixture, string, error)
    DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
    CountByAgent(ctx context.Context, filter FixtureListFilter) (map[uuid.UUID]int64, error)
}

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

type FixtureListFilter struct {
    AgentID   *uuid.UUID
    Since     *time.Time
    Until     *time.Time
    Tags      []string // AND match
    Status    *string
    Limit     int    // default 100, max 1000
    Cursor    string // opaque keyset cursor
}
```

### PG impl
- `internal/store/pg/fixture_pg.go` — `type FixturePG struct { db *sqlx.DB }`.
- Use `pq.Array(tags)` for `TEXT[]` parameter binding.
- Cursor encodes `(captured_at, id)` base64-json.

### SQLite impl
- `internal/store/sqlitestore/fixture_sqlite.go` — `type FixtureSQLite struct { db *sqlx.DB }`.
- Marshal `tags` to JSON text; `response_body` stored as JSON text (Go `string` conversion).
- Tag filter uses `tags LIKE '%"tag1"%'` (acceptable for low-cardinality lite use).

## Related Code Files
**Create**
- `internal/store/fixture.go` — interface + types
- `internal/store/pg/fixture_pg.go` — PG impl (~180 LOC target)
- `internal/store/pg/fixture_pg_test.go` — PG unit tests (sqlmock or integration)
- `internal/store/sqlitestore/fixture_sqlite.go` — SQLite impl (~180 LOC target)
- `internal/store/sqlitestore/fixture_sqlite_test.go` — SQLite in-mem tests
- `tests/integration/fixture_store_pg_test.go` — full PG roundtrip

**Modify**
- `internal/store/registry.go` (or wherever store bundle is assembled — check existing pattern) — add `FixtureStore` to `Stores` struct if such aggregator exists.

## Implementation Steps
1. Write `fixture_sqlite_test.go` covering all interface methods (failing).
2. Write `fixture_pg_test.go` (failing, integration-tagged).
3. Define `internal/store/fixture.go` interface + structs.
4. Implement `FixtureSQLite` in `fixture_sqlite.go` using `sqlx` prepared statements.
5. Implement `FixturePG` in `fixture_pg.go` mirroring shape; use `pq.Array` for tags, `pgtype.JSONB` for JSON cols.
6. Implement `BatchInsert` with multi-row `INSERT INTO ... VALUES ($1,..),($N,..)`.
7. Wire both into store registry.
8. Run unit + integration tests.
9. `go vet ./...` + both build tags compile.

## Test Plan (TDD — write first)
**Unit (both impls)**
- `TestFixtureInsertGet_Roundtrip` — insert → get by id → compare.
- `TestFixtureInsert_TenantScoped` — context without tenant → error; context with wrong tenant on Get → nil result.
- `TestFixtureList_Pagination` — insert 250 rows, list 100-at-a-time, assert cursor chain produces all rows exactly once.
- `TestFixtureList_FilterByAgent` / `_ByTags` / `_BySince` — filter correctness.
- `TestFixtureBatchInsert_Atomic` — partial failure rolls back entire batch.
- `TestFixtureDeleteOlderThan` — inserts at t0, t-24h, t-48h; delete cutoff t-30h → only t-48h row removed.

**Integration (PG)**
- `fixture_store_pg_test.go` — same tests against real pgtest container (port 5433); verifies GIN + `TEXT[]` behaviour.
- Tenant isolation: insert tenant A → read as tenant B → empty.

**Order:** write all tests, assert failing, implement interface+struct, impl SQLite, impl PG, green.

## Todo List
- [x] Write SQLite test suite (failing)
- [x] Write PG integration tests (failing)
- [x] Define `store.FixtureStore` interface + `Fixture` struct in `internal/store/fixture.go`
- [x] Implement `FixtureSQLite`
- [x] Implement `FixturePG`
- [x] `BatchInsert` multi-row VALUES
- [x] Wire into store registry
- [x] Both builds green (`./...` and `-tags sqliteonly ./...`)
- [x] All tests green

## Success Criteria
- Interface methods all pass tests on both backends.
- Tenant isolation enforced — cross-tenant reads return empty.
- Insert latency p95 < 5ms (local PG) for single row.
- BatchInsert 100 rows in single statement.

## Risk Assessment
| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|-----------|
| Forget tenant guard on a method | M | H | Test `_TenantScoped` for every method; code review checklist |
| SQLite `LIKE` tag filter false-positive | L | L | Escape quotes in tag values; document limitation |
| pgx BatchInsert param count > 65535 limit | L | M | Chunk batch to max 500 rows per statement |

## Security Considerations
- SQL injection: all queries parameterized (`$1, $2` / `?`).
- Tenant isolation: `store.TenantIDFromContext` or explicit `tenantID` arg on every WHERE.
- No master-scope bypass — writes by master tenant still record `tenant_id = MasterTenantID`.

## Next Steps / Dependencies
- Phase 01 must be merged (schema exists).
- Unblocks Phase 03 (capturer needs `FixtureStore.BatchInsert`) and Phase 04 (wire).

## Unresolved Questions
- Should `Get` return full body or allow `lite` flag to skip request/response JSON for listing screens? (defer — YAGNI; add only if CLI perf becomes an issue)
