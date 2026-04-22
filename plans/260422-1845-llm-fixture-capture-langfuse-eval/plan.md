---
title: "LLM Fixture Capture + Langfuse Model-Comparison Eval Pipeline"
description: "Opt-in capture of full LLM request/response fixtures for offline replay and model-migration confidence via Langfuse datasets."
status: pending
priority: P2
effort: 16h
branch: release/kevin
tags: [llm, fixtures, langfuse, eval, dual-db, tdd]
created: 2026-04-22
---

# LLM Fixture Capture + Langfuse Eval Pipeline

## Goal
Capture LLM `ChatRequest`/`ChatResponse` pairs (opt-in) into a dedicated `llm_fixtures` table, then export as NDJSON or push to self-hosted Langfuse for dataset-driven model comparison experiments.

## Architecture Decision (locked)
- **Path 2 — Opt-in Fixture Capture** (dedicated table, async hook at agent loop boundary, CLI export).
- Hook site: `internal/agent/loop_pipeline_callbacks.go:271,290` (stream + non-stream branches) — full-fidelity access to `chatReq` + `resp`.
- Existing tracing path **untouched**. Zero regression on `spans`/OTel.

## Phases

| # | Phase | File | Status | Blocks |
|---|-------|------|--------|--------|
| 1 | DB schema (PG + SQLite dual-DB) | [phase-01-db-schema-dual-db.md](./phase-01-db-schema-dual-db.md) | complete | 2 |
| 2 | Store interface + impls | [phase-02-store-interface-implementations.md](./phase-02-store-interface-implementations.md) | complete | 3,4 |
| 3 | Capture config + Capturer package | [phase-03-capture-hook-config.md](./phase-03-capture-hook-config.md) | pending | 4 |
| 4 | Wire Capturer into agent loop | [phase-04-wire-capture-into-loop.md](./phase-04-wire-capture-into-loop.md) | pending | 5,6,7 |
| 5 | Export CLI (NDJSON) | [phase-05-export-cli-ndjson.md](./phase-05-export-cli-ndjson.md) | pending | 6 |
| 6 | Langfuse dataset import | [phase-06-langfuse-dataset-import.md](./phase-06-langfuse-dataset-import.md) | pending | - |
| 7 | Redaction + retention worker | [phase-07-redaction-retention.md](./phase-07-redaction-retention.md) | pending | - |
| 8 | Docs + setup guide | [phase-08-docs-setup-guide.md](./phase-08-docs-setup-guide.md) | pending | - |

## Key Dependencies
- PG pgvector pg18 test container (port 5433) for integration tests
- Self-hosted Langfuse via docker-compose (phase 8)
- Env vars: `LANGFUSE_PUBLIC_KEY`, `LANGFUSE_SECRET_KEY`, `LANGFUSE_HOST`
- Dual-DB rule (CLAUDE.md): every schema change needs PG migration + SQLite full schema + SQLite incremental patch + two version bumps

## Cross-Phase Invariants
- Tenant-scoped. `WHERE tenant_id=$N` on every read/write. Master tenant for desktop/lite.
- Async channel-based capture. Never block provider.Chat. Drop-on-full with `slog.Warn("fixture.drop")`.
- All SQL parameterized. Indexes: `(tenant_id, captured_at DESC)`, `(tenant_id, agent_id, captured_at DESC)`, `tags GIN` (PG only).
- File size ≤200 LOC where feasible. Kebab-case filenames.
- TDD per phase: failing test → implement → refactor.

## Unresolved Questions (global)
1. Langfuse dataset naming convention: per-agent+date vs per-capture-session? (default to `goclaw-{agent_key}-{YYYY-MM-DD}`)
2. `llm_fixtures.span_id UUID NULL` FK to `spans.id` for correlation? (recommend nullable, no hard FK)
3. Lite edition exposes `fixture` CLI? (recommend yes — useful for solo-dev eval)
4. Streaming call capture: final assembled `Content+ToolCalls` only vs chunk stream? (recommend final only)
