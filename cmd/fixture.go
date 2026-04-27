package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// errConfirmRequired is returned when a large purge is attempted without --confirm.
var errConfirmRequired = errors.New("large deletion requires --confirm flag")

// openFixtureStoreFn is the store opener seam. Tests override this to inject a fake.
// Returns the store, a close func, and an error.
var openFixtureStoreFn = openFixtureStore

func fixtureCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fixture",
		Short: "Manage LLM fixture captures (list, export, purge)",
	}
	cmd.AddCommand(fixtureListCmd())
	cmd.AddCommand(fixtureExportCmd())
	cmd.AddCommand(fixturePurgeCmd())
	return cmd
}

// parseSince parses a --since value: Go duration (e.g. "24h", "7d") or RFC3339 timestamp.
// "7d" is a convenience shorthand (not a standard Go duration).
func parseSince(s string) (time.Time, error) {
	// Handle "Nd" shorthand for days (e.g. "7d", "30d").
	if strings.HasSuffix(s, "d") {
		daysStr := strings.TrimSuffix(s, "d")
		var days int
		if _, err := fmt.Sscanf(daysStr, "%d", &days); err != nil || days <= 0 {
			return time.Time{}, fmt.Errorf("invalid duration %q: expected positive integer before 'd'", s)
		}
		return time.Now().Add(-time.Duration(days) * 24 * time.Hour), nil
	}
	// Standard Go duration (e.g. "24h", "90m").
	if d, err := time.ParseDuration(s); err == nil {
		if d <= 0 {
			return time.Time{}, fmt.Errorf("duration must be positive, got %q", s)
		}
		return time.Now().Add(-d), nil
	}
	// RFC3339 timestamp.
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("cannot parse %q: use a Go duration (e.g. 24h), day shorthand (e.g. 7d), or RFC3339 timestamp", s)
}

// resolveTenant resolves a tenant UUID from the --tenant flag or GOCLAW_TENANT_ID env.
// Returns store.MasterTenantID if neither is set.
func resolveTenant(flagVal string) (uuid.UUID, error) {
	s := flagVal
	if s == "" {
		s = os.Getenv("GOCLAW_TENANT_ID")
	}
	if s == "" {
		return store.MasterTenantID, nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid tenant UUID %q: %w", s, err)
	}
	return id, nil
}
