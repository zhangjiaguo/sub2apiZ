-- Codex 模型降智（换模）检测（codex_modeltrace）相关表。
-- 机制：用「生成 292..332 个 1..355 整数」的挑战探测 chatgpt.com/backend-api/codex/responses，
-- 对回答里的数字分布做指纹比对。样本表存原始采集；指纹库表存整库 JSON
-- （单行原子替换）；结果表存每次建库/检测任务的逐对结论。

-- 建库原始样本：每 (model, 采样) 一行。建库时全量读取聚合，删除即可清理重建。
CREATE TABLE IF NOT EXISTS codex_modeltrace_samples (
    id              BIGSERIAL PRIMARY KEY,
    model           TEXT NOT NULL,
    account_id      BIGINT NOT NULL,
    expected_count  INT NOT NULL,
    numbers         JSONB NOT NULL,
    numbers_count   INT NOT NULL,
    latency_ms      INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_codex_modeltrace_samples_model
    ON codex_modeltrace_samples (model, id);

-- 指纹库：单行（id=1 CHECK 约束）整库 JSON，构建时原子替换。
CREATE TABLE IF NOT EXISTS codex_modeltrace_bank (
    id          INT PRIMARY KEY DEFAULT 1,
    bank        JSONB NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT codex_modeltrace_bank_single CHECK (id = 1)
);

-- 检测/建库任务结果：每 (task, account, model) 一行。
-- kind: bank=建库采样统计  detect=降智检测判定
-- match: verdict == model（请求模型与判定一致；false 即疑似换模）
CREATE TABLE IF NOT EXISTS codex_modeltrace_results (
    id              BIGSERIAL PRIMARY KEY,
    task_id         TEXT NOT NULL,
    kind            TEXT NOT NULL,
    account_id      BIGINT NOT NULL,
    model           TEXT NOT NULL,
    verdict         TEXT NOT NULL DEFAULT '',
    probability     DOUBLE PRECISION NOT NULL DEFAULT 0,
    match           BOOLEAN NOT NULL DEFAULT FALSE,
    top_hits        INT NOT NULL DEFAULT 0,
    valid_runs      INT NOT NULL DEFAULT 0,
    failures        INT NOT NULL DEFAULT 0,
    avg_latency_ms  INT NOT NULL DEFAULT 0,
    reasons         TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_codex_modeltrace_results_task
    ON codex_modeltrace_results (task_id, id);
CREATE INDEX IF NOT EXISTS idx_codex_modeltrace_results_created
    ON codex_modeltrace_results (created_at DESC);
