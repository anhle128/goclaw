package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

func fixtureListCmd() *cobra.Command {
	var (
		agentFlag  string
		sinceFlag  string
		tagFlag    string
		statusFlag string
		limitFlag  int
		jsonFlag   bool
		tenantFlag string
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List captured LLM fixtures",
		RunE: func(cmd *cobra.Command, _ []string) error {
			tenantID, err := resolveTenant(tenantFlag)
			if err != nil {
				return err
			}

			fs, closeDB, err := openFixtureStoreFn(resolveConfigPath())
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}
			defer closeDB()

			filter, err := buildFilter(agentFlag, sinceFlag, tagFlag, statusFlag, limitFlag)
			if err != nil {
				return err
			}

			ctx := store.WithTenantID(context.Background(), tenantID)
			rows, _, err := fs.List(ctx, filter)
			if err != nil {
				return fmt.Errorf("list fixtures: %w", err)
			}

			return printFixtures(cmd.OutOrStdout(), rows, jsonFlag)
		},
	}

	cmd.Flags().StringVar(&agentFlag, "agent", "", "filter by agent UUID")
	cmd.Flags().StringVar(&sinceFlag, "since", "", "filter captured_at >= value (e.g. 24h, 7d, RFC3339)")
	cmd.Flags().StringVar(&tagFlag, "tag", "", "filter by tag (single tag)")
	cmd.Flags().StringVar(&statusFlag, "status", "", "filter by status: ok|error|cancelled")
	cmd.Flags().IntVar(&limitFlag, "limit", 100, "max rows to return (max 1000)")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "output as JSON array")
	cmd.Flags().StringVar(&tenantFlag, "tenant", "", "tenant UUID (default: master or GOCLAW_TENANT_ID env)")

	return cmd
}

// buildFilter constructs a FixtureListFilter from CLI flag values.
func buildFilter(agentFlag, sinceFlag, tagFlag, statusFlag string, limitFlag int) (store.FixtureListFilter, error) {
	filter := store.FixtureListFilter{Limit: limitFlag}

	if agentFlag != "" {
		id, err := uuid.Parse(agentFlag)
		if err != nil {
			return filter, fmt.Errorf("invalid agent UUID %q: %w", agentFlag, err)
		}
		filter.AgentID = &id
	}
	if sinceFlag != "" {
		t, err := parseSince(sinceFlag)
		if err != nil {
			return filter, err
		}
		filter.Since = &t
	}
	if tagFlag != "" {
		filter.Tags = []string{tagFlag}
	}
	if statusFlag != "" {
		filter.Status = &statusFlag
	}
	return filter, nil
}

// printFixtures renders fixture rows to w in table or JSON format.
func printFixtures(w io.Writer, rows []store.Fixture, asJSON bool) error {
	if asJSON {
		type fixtureJSON struct {
			ID         string    `json:"id"`
			TenantID   string    `json:"tenant_id"`
			AgentID    *string   `json:"agent_id,omitempty"`
			Provider   string    `json:"provider"`
			Model      string    `json:"model"`
			Status     string    `json:"status"`
			InputTok   int       `json:"input_tokens"`
			OutputTok  int       `json:"output_tokens"`
			LatencyMs  int       `json:"latency_ms"`
			CostUSD    float64   `json:"cost_usd"`
			CapturedAt time.Time `json:"captured_at"`
		}
		out := make([]fixtureJSON, len(rows))
		for i, r := range rows {
			j := fixtureJSON{
				ID:         r.ID.String(),
				TenantID:   r.TenantID.String(),
				Provider:   r.Provider,
				Model:      r.Model,
				Status:     r.Status,
				InputTok:   r.InputTokens,
				OutputTok:  r.OutputTokens,
				LatencyMs:  r.LatencyMs,
				CostUSD:    r.TotalCostUSD,
				CapturedAt: r.CapturedAt,
			}
			if r.AgentID != nil {
				s := r.AgentID.String()
				j.AgentID = &s
			}
			out[i] = j
		}
		enc, err := json.Marshal(out)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(enc))
		return err
	}

	if len(rows) == 0 {
		fmt.Fprintln(w, "No fixtures found.")
		return nil
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tPROVIDER\tMODEL\tSTATUS\tIN_TOK\tOUT_TOK\tLAT_MS\tCAPTURED_AT")
	for _, r := range rows {
		idShort := r.ID.String()
		if len(idShort) > 8 {
			idShort = idShort[:8]
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%d\t%s\n",
			idShort, r.Provider, r.Model, r.Status,
			r.InputTokens, r.OutputTokens, r.LatencyMs,
			r.CapturedAt.Format(time.DateTime),
		)
	}
	return tw.Flush()
}
