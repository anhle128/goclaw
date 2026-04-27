package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// confirmThreshold is the row count above which --confirm is required for live purge.
const confirmThreshold = 10_000

func fixturePurgeCmd() *cobra.Command {
	var (
		olderThanFlag string
		tenantFlag    string
		dryRunFlag    bool
		confirmFlag   bool
	)

	cmd := &cobra.Command{
		Use:   "purge",
		Short: "Delete fixtures older than a retention cutoff",
		Long: `Delete LLM fixture captures older than the given cutoff.

Use --dry-run to preview the row count without deleting.
If the estimated deletion count exceeds 10,000 rows, --confirm is required.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			olderThan := olderThanFlag
			if olderThan == "" {
				olderThan = "30d"
			}

			cutoff, err := parseSince(olderThan)
			if err != nil {
				return fmt.Errorf("--older-than: %w", err)
			}

			tenantID, err := resolveTenant(tenantFlag)
			if err != nil {
				return err
			}

			fs, closeDB, err := openFixtureStoreFn(resolveConfigPath())
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}
			defer closeDB()

			ctx := store.WithTenantID(context.Background(), tenantID)

			// Count rows affected (using Since=nil, Until=cutoff to match DeleteOlderThan scope).
			until := cutoff
			counts, err := fs.CountByAgent(ctx, store.FixtureListFilter{Until: &until})
			if err != nil {
				return fmt.Errorf("count fixtures: %w", err)
			}

			var total int64
			for _, n := range counts {
				total += n
			}

			if dryRunFlag {
				fmt.Fprintf(cmd.OutOrStdout(), "dry-run: would delete %d fixture(s) older than %s\n", total, cutoff.Format("2006-01-02T15:04:05Z"))
				return nil
			}

			if total > confirmThreshold && !confirmFlag {
				return fmt.Errorf("%w: %d rows would be deleted (threshold %d); re-run with --confirm", errConfirmRequired, total, confirmThreshold)
			}

			deleted, err := fs.DeleteOlderThan(ctx, cutoff)
			if err != nil {
				return fmt.Errorf("delete fixtures: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "purged %d fixture(s) older than %s\n", deleted, cutoff.Format("2006-01-02T15:04:05Z"))
			return nil
		},
	}

	cmd.Flags().StringVar(&olderThanFlag, "older-than", "30d", "delete fixtures older than this value (e.g. 30d, 720h, RFC3339)")
	cmd.Flags().StringVar(&tenantFlag, "tenant", "", "tenant UUID (default: master or GOCLAW_TENANT_ID env)")
	cmd.Flags().BoolVar(&dryRunFlag, "dry-run", false, "print row count without deleting")
	cmd.Flags().BoolVar(&confirmFlag, "confirm", false, "required when deletion count exceeds 10,000 rows")

	return cmd
}
