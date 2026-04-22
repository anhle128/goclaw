//go:build integration

package integration

import (
	"testing"
)

// TestFixturePGMigrate_000056 verifies migration 000056 (llm_fixtures) against
// a live pgtest container on port 5433. Requires:
//   TEST_DATABASE_URL="postgres://postgres:test@localhost:5433/goclaw_test?sslmode=disable"
//   docker run -d --name pgtest -p 5433:5432 -e POSTGRES_PASSWORD=test -e POSTGRES_DB=goclaw_test pgvector/pgvector:pg18

func TestFixturePGMigrate_000056(t *testing.T) {
	db := testDB(t)

	// Assert table exists after migrate up.
	var tableCount int
	err := db.QueryRow(`
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name = 'llm_fixtures'
	`).Scan(&tableCount)
	if err != nil {
		t.Fatalf("query information_schema: %v", err)
	}
	if tableCount != 1 {
		t.Fatal("llm_fixtures table not found after migration 000056 up")
	}

	// Assert all 4 indexes exist.
	wantIndexes := []string{
		"idx_llm_fixtures_tenant_time",
		"idx_llm_fixtures_tenant_agent_time",
		"idx_llm_fixtures_tags_gin",
		"idx_llm_fixtures_status",
	}
	for _, idx := range wantIndexes {
		var c int
		db.QueryRow(`
			SELECT count(*) FROM pg_indexes
			WHERE tablename = 'llm_fixtures' AND indexname = $1
		`, idx).Scan(&c)
		if c != 1 {
			t.Errorf("index %q not found", idx)
		}
	}
}
