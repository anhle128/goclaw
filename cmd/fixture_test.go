package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// --- fake FixtureStore ---

type fakeFixtureStore struct {
	rows          []store.Fixture
	pageSize      int // rows per page (0 = return all at once)
	errorOnPage   int // 1-indexed; 0 = never error
	callsToDelete int
	lastCutoff    time.Time
	countResult   map[uuid.UUID]int64
	capturedFilter store.FixtureListFilter
}

func (f *fakeFixtureStore) Insert(_ context.Context, _ store.Fixture) error { return nil }
func (f *fakeFixtureStore) BatchInsert(_ context.Context, _ []store.Fixture) error { return nil }
func (f *fakeFixtureStore) Get(_ context.Context, _ uuid.UUID) (*store.Fixture, error) {
	return nil, nil
}

func (f *fakeFixtureStore) List(_ context.Context, filter store.FixtureListFilter) ([]store.Fixture, string, error) {
	f.capturedFilter = filter

	all := f.rows
	pageSize := f.pageSize
	if pageSize <= 0 {
		pageSize = len(all) + 1
	}

	// Decode cursor to find offset by matching encoded cursor to rows.
	offset := 0
	if filter.Cursor != "" {
		for i, r := range all {
			encoded := store.EncodeFixtureCursor(r.CapturedAt, r.ID)
			if encoded == filter.Cursor {
				offset = i + 1
				break
			}
		}
	}

	page := (offset / pageSize) + 1
	if f.errorOnPage > 0 && page == f.errorOnPage {
		return nil, "", fmt.Errorf("fake store error on page %d", page)
	}

	end := offset + pageSize
	if end > len(all) {
		end = len(all)
	}
	chunk := all[offset:end]

	var nextCursor string
	if end < len(all) {
		last := chunk[len(chunk)-1]
		nextCursor = store.EncodeFixtureCursor(last.CapturedAt, last.ID)
	}
	return chunk, nextCursor, nil
}

func (f *fakeFixtureStore) DeleteOlderThan(_ context.Context, cutoff time.Time) (int64, error) {
	f.callsToDelete++
	f.lastCutoff = cutoff
	return 0, nil
}

func (f *fakeFixtureStore) CountByAgent(_ context.Context, _ store.FixtureListFilter) (map[uuid.UUID]int64, error) {
	if f.countResult != nil {
		return f.countResult, nil
	}
	return map[uuid.UUID]int64{}, nil
}

// makeFixtures generates n fixture rows with deterministic CapturedAt for pagination.
func makeFixtures(n int) []store.Fixture {
	base := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	rows := make([]store.Fixture, n)
	for i := range rows {
		id := uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1))
		rows[i] = store.Fixture{
			ID:          id,
			TenantID:    uuid.MustParse("00000000-0000-0000-0000-000000000001"),
			Provider:    "anthropic",
			Model:       "claude-opus",
			RequestBody: json.RawMessage(`{"messages":[]}`),
			Status:      "ok",
			CapturedAt:  base.Add(time.Duration(i) * time.Second),
		}
	}
	return rows
}

// --- helpers ---

func withFakeStore(fake *fakeFixtureStore) func() {
	orig := openFixtureStoreFn
	openFixtureStoreFn = func(_ string) (store.FixtureStore, func(), error) {
		return fake, func() {}, nil
	}
	return func() { openFixtureStoreFn = orig }
}

// --- TestFixtureCmd_ParseSince ---

func TestFixtureCmd_ParseSince(t *testing.T) {
	now := time.Now()
	cases := []struct {
		input   string
		wantErr bool
		check   func(t *testing.T, got time.Time)
	}{
		{
			input: "24h",
			check: func(t *testing.T, got time.Time) {
				diff := now.Sub(got)
				if diff < 23*time.Hour || diff > 25*time.Hour {
					t.Errorf("24h: expected ~24h ago, got diff=%v", diff)
				}
			},
		},
		{
			input: "7d",
			check: func(t *testing.T, got time.Time) {
				diff := now.Sub(got)
				if diff < 6*24*time.Hour || diff > 8*24*time.Hour {
					t.Errorf("7d: expected ~7d ago, got diff=%v", diff)
				}
			},
		},
		{
			input: "2026-04-20T00:00:00Z",
			check: func(t *testing.T, got time.Time) {
				want := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
				if !got.Equal(want) {
					t.Errorf("RFC3339: want %v got %v", want, got)
				}
			},
		},
		{
			input:   "bogus",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got, err := parseSince(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tc.check(t, got)
		})
	}
}

// --- TestFixtureCmd_ExportStreamsAllRows ---

func TestFixtureCmd_ExportStreamsAllRows(t *testing.T) {
	const total = 250
	fake := &fakeFixtureStore{
		rows:     makeFixtures(total),
		pageSize: 100, // 3 pages: 100 + 100 + 50
	}
	restore := withFakeStore(fake)
	defer restore()

	var buf bytes.Buffer
	cmd := fixtureCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"export", "--out", "-", "--tenant", "00000000-0000-0000-0000-000000000001"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != total {
		t.Errorf("want %d lines, got %d", total, len(lines))
	}
	for i, line := range lines {
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Errorf("line %d not valid JSON: %v", i+1, err)
		}
	}
}

// --- TestFixtureCmd_ExportStdout ---

func TestFixtureCmd_ExportStdout(t *testing.T) {
	fake := &fakeFixtureStore{rows: makeFixtures(5)}
	restore := withFakeStore(fake)
	defer restore()

	var buf bytes.Buffer
	cmd := fixtureCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"export", "--out", "-", "--tenant", "00000000-0000-0000-0000-000000000001"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if buf.Len() == 0 {
		t.Error("expected output on stdout writer, got empty")
	}
}

// --- TestFixtureCmd_ExportToFile_PartialOnError ---

func TestFixtureCmd_ExportToFile_PartialOnError(t *testing.T) {
	fake := &fakeFixtureStore{
		rows:        makeFixtures(250),
		pageSize:    100,
		errorOnPage: 2, // errors after page 1 (100 rows written)
	}
	restore := withFakeStore(fake)
	defer restore()

	outPath := t.TempDir() + "/out.jsonl"

	cmd := fixtureCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"export", "--out", outPath, "--tenant", "00000000-0000-0000-0000-000000000001"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	partialPath := outPath + ".partial"
	info, statErr := os.Stat(partialPath)
	if statErr != nil {
		t.Fatalf("expected %s to exist: %v", partialPath, statErr)
	}
	if info.Size() == 0 {
		t.Error("partial file should contain rows from page 1")
	}
}

// --- TestFixtureCmd_ListFilter ---

func TestFixtureCmd_ListFilter(t *testing.T) {
	agentID := uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")
	fake := &fakeFixtureStore{rows: makeFixtures(3)}
	restore := withFakeStore(fake)
	defer restore()

	cmd := fixtureCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{
		"list",
		"--agent", agentID.String(),
		"--since", "24h",
		"--tag", "eval-v1",
		"--status", "ok",
		"--limit", "50",
		"--tenant", "00000000-0000-0000-0000-000000000001",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	f := fake.capturedFilter
	if f.AgentID == nil || *f.AgentID != agentID {
		t.Errorf("AgentID: want %v got %v", agentID, f.AgentID)
	}
	if f.Since == nil {
		t.Error("Since: expected non-nil")
	}
	if len(f.Tags) == 0 || f.Tags[0] != "eval-v1" {
		t.Errorf("Tags: want [eval-v1] got %v", f.Tags)
	}
	if f.Status == nil || *f.Status != "ok" {
		t.Errorf("Status: want ok got %v", f.Status)
	}
	if f.Limit != 50 {
		t.Errorf("Limit: want 50 got %d", f.Limit)
	}
}

// --- TestFixtureCmd_PurgeDryRun ---

func TestFixtureCmd_PurgeDryRun(t *testing.T) {
	fake := &fakeFixtureStore{
		countResult: map[uuid.UUID]int64{
			uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001"): 42,
		},
	}
	restore := withFakeStore(fake)
	defer restore()

	var buf bytes.Buffer
	cmd := fixtureCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"purge", "--dry-run", "--older-than", "30d", "--tenant", "00000000-0000-0000-0000-000000000001"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fake.callsToDelete != 0 {
		t.Errorf("dry-run: DeleteOlderThan must NOT be called, got %d calls", fake.callsToDelete)
	}
	out := buf.String()
	if !strings.Contains(out, "42") {
		t.Errorf("dry-run: expected count 42 in output, got: %s", out)
	}
}

// --- TestFixtureCmd_PurgeLive ---

func TestFixtureCmd_PurgeLive(t *testing.T) {
	fake := &fakeFixtureStore{}
	restore := withFakeStore(fake)
	defer restore()

	before := time.Now()
	cmd := fixtureCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"purge", "--older-than", "30d", "--confirm", "--tenant", "00000000-0000-0000-0000-000000000001"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	after := time.Now()

	if fake.callsToDelete != 1 {
		t.Errorf("want 1 DeleteOlderThan call, got %d", fake.callsToDelete)
	}
	// Cutoff should be ~30d before now.
	wantMin := before.Add(-30 * 24 * time.Hour).Add(-2 * time.Minute)
	wantMax := after.Add(-30 * 24 * time.Hour).Add(2 * time.Minute)
	if fake.lastCutoff.Before(wantMin) || fake.lastCutoff.After(wantMax) {
		t.Errorf("cutoff %v not in expected range [%v, %v]", fake.lastCutoff, wantMin, wantMax)
	}
}

// --- TestFixtureCmd_PurgeLargeDeleteWithoutConfirm ---

func TestFixtureCmd_PurgeLargeDeleteWithoutConfirm(t *testing.T) {
	fake := &fakeFixtureStore{
		countResult: map[uuid.UUID]int64{
			uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001"): 15000,
		},
	}
	restore := withFakeStore(fake)
	defer restore()

	cmd := fixtureCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"purge", "--older-than", "30d", "--tenant", "00000000-0000-0000-0000-000000000001"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for large delete without --confirm")
	}
	if !errors.Is(err, errConfirmRequired) {
		t.Errorf("want errConfirmRequired, got %v", err)
	}
	if fake.callsToDelete != 0 {
		t.Errorf("DeleteOlderThan must NOT be called, got %d", fake.callsToDelete)
	}
}

// --- TestFixtureCmd_ListJSON ---

func TestFixtureCmd_ListJSON(t *testing.T) {
	fake := &fakeFixtureStore{rows: makeFixtures(3)}
	restore := withFakeStore(fake)
	defer restore()

	var buf bytes.Buffer
	cmd := fixtureCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"list", "--json", "--tenant", "00000000-0000-0000-0000-000000000001"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var arr []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &arr); err != nil {
		t.Fatalf("output not valid JSON array: %v\noutput: %s", err, buf.String())
	}
	if len(arr) != 3 {
		t.Errorf("want 3 items, got %d", len(arr))
	}
}
