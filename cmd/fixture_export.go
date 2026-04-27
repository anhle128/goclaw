package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// fixtureNDJSON is the wire shape for each exported NDJSON line.
type fixtureNDJSON struct {
	ID            string          `json:"id"`
	TenantID      string          `json:"tenant_id"`
	AgentID       *string         `json:"agent_id,omitempty"`
	SessionKey    *string         `json:"session_key,omitempty"`
	SpanID        *string         `json:"span_id,omitempty"`
	Provider      string          `json:"provider"`
	Model         string          `json:"model"`
	Request       json.RawMessage `json:"request"`
	Response      json.RawMessage `json:"response,omitempty"`
	ToolCallCount int             `json:"tool_call_count"`
	Usage         struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	CostUSD    float64         `json:"cost_usd"`
	LatencyMs  int             `json:"latency_ms"`
	Status     string          `json:"status"`
	Error      *string         `json:"error,omitempty"`
	Tags       []string        `json:"tags"`
	Metadata   json.RawMessage `json:"metadata"`
	CapturedAt time.Time       `json:"captured_at"`
}

func toNDJSON(f store.Fixture) fixtureNDJSON {
	r := fixtureNDJSON{
		ID:            f.ID.String(),
		TenantID:      f.TenantID.String(),
		SessionKey:    f.SessionKey,
		Provider:      f.Provider,
		Model:         f.Model,
		Request:       f.RequestBody,
		Response:      f.ResponseBody,
		ToolCallCount: f.ToolCallCount,
		CostUSD:       f.TotalCostUSD,
		LatencyMs:     f.LatencyMs,
		Status:        f.Status,
		Error:         f.Error,
		Tags:          f.Tags,
		Metadata:      f.Metadata,
		CapturedAt:    f.CapturedAt,
	}
	if len(r.Metadata) == 0 {
		r.Metadata = json.RawMessage("{}")
	}
	if r.Tags == nil {
		r.Tags = []string{}
	}
	r.Usage.InputTokens = f.InputTokens
	r.Usage.OutputTokens = f.OutputTokens
	if f.AgentID != nil {
		s := f.AgentID.String()
		r.AgentID = &s
	}
	if f.SpanID != nil && *f.SpanID != uuid.Nil {
		s := f.SpanID.String()
		r.SpanID = &s
	}
	return r
}

func fixtureExportCmd() *cobra.Command {
	var (
		agentFlag  string
		sinceFlag  string
		tagFlag    string
		outFlag    string
		tenantFlag string
	)

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export fixtures as NDJSON (one JSON object per line)",
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

			filter, err := buildFilter(agentFlag, sinceFlag, tagFlag, "", 500)
			if err != nil {
				return err
			}

			ctx := store.WithTenantID(context.Background(), tenantID)
			return runExport(ctx, cmd.OutOrStdout(), fs, filter, outFlag)
		},
	}

	cmd.Flags().StringVar(&agentFlag, "agent", "", "filter by agent UUID")
	cmd.Flags().StringVar(&sinceFlag, "since", "", "filter captured_at >= value (e.g. 24h, 7d, RFC3339)")
	cmd.Flags().StringVar(&tagFlag, "tag", "", "filter by tag (single tag)")
	cmd.Flags().StringVarP(&outFlag, "out", "o", "-", "output path (- for stdout)")
	cmd.Flags().StringVar(&tenantFlag, "tenant", "", "tenant UUID (default: master or GOCLAW_TENANT_ID env)")

	return cmd
}

// runExport streams all matching fixtures as NDJSON via keyset pagination.
// On error mid-stream: renames output file to <path>.partial and returns the error.
func runExport(ctx context.Context, stdout io.Writer, fs store.FixtureStore, filter store.FixtureListFilter, outPath string) error {
	toStdout := outPath == "" || outPath == "-"

	var (
		w        io.Writer
		f        *os.File
		realPath string
	)

	if toStdout {
		w = stdout
	} else {
		realPath = outPath
		var err error
		f, err = os.OpenFile(realPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			return fmt.Errorf("create output file: %w", err)
		}
		w = f
	}

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	var (
		total    int
		writeErr error
		cursor   string
	)

	for {
		filter.Cursor = cursor
		rows, nextCursor, err := fs.List(ctx, filter)
		if err != nil {
			writeErr = fmt.Errorf("list fixtures (cursor=%q): %w", cursor, err)
			break
		}
		for _, row := range rows {
			if err := enc.Encode(toNDJSON(row)); err != nil {
				writeErr = fmt.Errorf("encode fixture %s: %w", row.ID, err)
				break
			}
			total++
			if total%1000 == 0 {
				fmt.Fprintf(os.Stderr, "exported %d rows\n", total)
			}
		}
		if writeErr != nil {
			break
		}
		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}

	// Close file before renaming (required on Windows too).
	if f != nil {
		_ = f.Close()
	}

	if writeErr != nil {
		if !toStdout && realPath != "" {
			partialPath := realPath + ".partial"
			_ = os.Rename(realPath, partialPath)
		}
		return writeErr
	}

	fmt.Fprintf(os.Stderr, "exported %d rows total\n", total)
	return nil
}
