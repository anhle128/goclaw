-- llm_fixtures: captures raw LLM request/response pairs for evaluation,
-- replay, and Langfuse-style observability. Tenant-scoped; no PII-redaction
-- here — redaction handled by the retention worker (Phase 07).
-- DOWN migration drops this table and all data — document before running.
CREATE TABLE llm_fixtures (
    id               UUID        PRIMARY KEY,
    tenant_id        UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    agent_id         UUID        REFERENCES agents(id) ON DELETE SET NULL,
    session_key      TEXT,
    span_id          UUID,
    provider         VARCHAR(64) NOT NULL,
    model            VARCHAR(128) NOT NULL,
    request_body     JSONB       NOT NULL,
    response_body    JSONB,
    tool_call_count  INT         NOT NULL DEFAULT 0,
    input_tokens     INT         NOT NULL DEFAULT 0,
    output_tokens    INT         NOT NULL DEFAULT 0,
    total_cost_usd   NUMERIC(12,6) NOT NULL DEFAULT 0,
    latency_ms       INT         NOT NULL DEFAULT 0,
    status           VARCHAR(32) NOT NULL,
    error            TEXT,
    tags             TEXT[]      NOT NULL DEFAULT '{}',
    metadata         JSONB       NOT NULL DEFAULT '{}',
    captured_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_llm_fixtures_tenant_time
    ON llm_fixtures (tenant_id, captured_at DESC);

CREATE INDEX idx_llm_fixtures_tenant_agent_time
    ON llm_fixtures (tenant_id, agent_id, captured_at DESC);

CREATE INDEX idx_llm_fixtures_tags_gin
    ON llm_fixtures USING GIN (tags);

CREATE INDEX idx_llm_fixtures_status
    ON llm_fixtures (tenant_id, status)
    WHERE status <> 'ok';
