//go:build !sqliteonly

package cmd

import (
	"fmt"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/nextlevelbuilder/goclaw/internal/config"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/internal/store/pg"
)

// openFixtureStore opens a PG-backed FixtureStore from the resolved config path.
// Returns the store, a close func (closes the underlying DB), and an error.
func openFixtureStore(cfgPath string) (store.FixtureStore, func(), error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}
	dsn := cfg.Database.PostgresDSN
	if dsn == "" {
		dsn = os.Getenv("GOCLAW_POSTGRES_DSN")
	}
	if dsn == "" {
		return nil, nil, fmt.Errorf("postgres DSN not configured; set GOCLAW_POSTGRES_DSN or database.postgres_dsn in config")
	}
	db, err := pg.OpenDB(dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("open postgres: %w", err)
	}
	fs := pg.NewPGFixtureStore(db)
	return fs, func() { _ = db.Close() }, nil
}
