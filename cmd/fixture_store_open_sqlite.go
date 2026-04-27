//go:build sqliteonly

package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nextlevelbuilder/goclaw/internal/config"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/internal/store/sqlitestore"
)

// openFixtureStore opens a SQLite-backed FixtureStore from the resolved config path.
// Returns the store, a close func (closes the underlying DB), and an error.
func openFixtureStore(cfgPath string) (store.FixtureStore, func(), error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}
	dbPath := cfg.Database.SQLitePath
	if dbPath == "" {
		dataDir := cfg.ResolvedDataDir()
		if dataDir == "" {
			home, herr := os.UserHomeDir()
			if herr != nil {
				return nil, nil, fmt.Errorf("resolve home dir: %w", herr)
			}
			dataDir = filepath.Join(home, ".goclaw", "data")
		}
		dbPath = filepath.Join(dataDir, "goclaw.db")
	}
	db, err := sqlitestore.OpenDB(dbPath)
	if err != nil {
		return nil, nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := sqlitestore.EnsureSchema(db); err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("ensure schema: %w", err)
	}
	fs := sqlitestore.NewSQLiteFixtureStore(db)
	return fs, func() { _ = db.Close() }, nil
}
