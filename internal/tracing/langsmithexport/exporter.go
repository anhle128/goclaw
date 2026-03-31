package langsmithexport

import (
	"context"
	"log/slog"
	"sync"
	"time"

	langsmith "github.com/langchain-ai/langsmith-go"

	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/internal/tracing"
)

const (
	// pendingTTL is how long we keep "running" spans in the pending map
	// before cleaning them up as orphans.
	pendingTTL       = 5 * time.Minute
	pendingPruneFreq = 2 * time.Minute
)

// pendingEntry stores metadata needed when a two-phase span update arrives.
type pendingEntry struct {
	createdAt   time.Time
	dottedOrder string
}

// Config configures the LangSmith exporter.
type Config struct {
	APIKey  string // required
	Project string // default: "default"
	APIUrl  string // default: LangSmith cloud
}

// Exporter sends GoClaw spans to LangSmith as runs via the multipart
// ingestion API. Implements tracing.SpanExporter and tracing.SpanUpdateExporter.
type Exporter struct {
	client *langsmith.TracingClient

	// pending tracks "running" spans (two-phase start) waiting for their
	// update. Key: spanID, value: pendingEntry.
	pending   sync.Map
	pruneOnce sync.Once
	pruneWg   sync.WaitGroup
	stopCh    chan struct{}
}

// New creates a LangSmith exporter with the given config.
func New(cfg Config) (*Exporter, error) {
	if cfg.Project == "" {
		cfg.Project = "default"
	}

	opts := []langsmith.TracingOption{
		langsmith.WithTracingAPIKey(cfg.APIKey),
		langsmith.WithTracingProject(cfg.Project),
	}
	if cfg.APIUrl != "" {
		opts = append(opts, langsmith.WithTracingAPIURL(cfg.APIUrl))
	}

	client, err := langsmith.NewTracingClient(context.Background(), opts...)
	if err != nil {
		return nil, err
	}

	return &Exporter{
		client: client,
		stopCh: make(chan struct{}),
	}, nil
}

// ExportSpans converts GoClaw SpanData to LangSmith runs and sends them.
// Called by the Collector during flush alongside the PostgreSQL batch insert.
func (e *Exporter) ExportSpans(ctx context.Context, spans []store.SpanData) {
	if e == nil || len(spans) == 0 {
		return
	}

	e.startPruneLoop()

	for _, s := range spans {
		rc := spanToRunCreate(s)

		if isRunningSpan(s) {
			// Two-phase: span just started. Track it so we can send RunUpdate later.
			e.pending.Store(s.ID, pendingEntry{
				createdAt:   time.Now(),
				dottedOrder: rc.DottedOrder,
			})
		}

		if err := e.client.CreateRun(rc); err != nil {
			slog.Warn("langsmith: failed to create run",
				"span_id", s.ID, "name", s.Name, "error", err)
		}
	}
}

// ExportSpanUpdates converts deferred span updates to LangSmith RunUpdates.
// Called by the Collector after DB span updates are applied.
func (e *Exporter) ExportSpanUpdates(ctx context.Context, updates []tracing.SpanUpdate) {
	if e == nil || len(updates) == 0 {
		return
	}

	for _, u := range updates {
		ru := spanUpdateToRunUpdate(u)

		// Attach DottedOrder from the pending entry (required by LangSmith).
		if entry, ok := e.pending.LoadAndDelete(u.SpanID); ok {
			if pe, ok := entry.(pendingEntry); ok {
				ru.DottedOrder = pe.dottedOrder
			}
		}

		if err := e.client.UpdateRun(ru); err != nil {
			slog.Warn("langsmith: failed to update run",
				"span_id", u.SpanID, "error", err)
		}
	}
}

// Shutdown gracefully shuts down the LangSmith exporter, flushing remaining runs.
func (e *Exporter) Shutdown(ctx context.Context) error {
	if e == nil {
		return nil
	}
	close(e.stopCh)
	e.pruneWg.Wait()
	slog.Info("langsmith exporter shutting down")
	e.client.Close()
	return nil
}

// startPruneLoop starts a background goroutine (once) to clean up orphaned
// pending entries that never received an update.
func (e *Exporter) startPruneLoop() {
	e.pruneOnce.Do(func() {
		e.pruneWg.Add(1)
		go func() {
			defer e.pruneWg.Done()
			ticker := time.NewTicker(pendingPruneFreq)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					e.pruneStale()
				case <-e.stopCh:
					return
				}
			}
		}()
	})
}

// pruneStale removes pending entries older than pendingTTL.
func (e *Exporter) pruneStale() {
	cutoff := time.Now().Add(-pendingTTL)
	var pruned int
	e.pending.Range(func(key, value any) bool {
		if pe, ok := value.(pendingEntry); ok && pe.createdAt.Before(cutoff) {
			e.pending.Delete(key)
			pruned++
		}
		return true
	})
	if pruned > 0 {
		slog.Debug("langsmith: pruned stale pending spans", "count", pruned)
	}
}

// Compile-time interface checks.
var (
	_ tracing.SpanExporter       = (*Exporter)(nil)
	_ tracing.SpanUpdateExporter = (*Exporter)(nil)
)
