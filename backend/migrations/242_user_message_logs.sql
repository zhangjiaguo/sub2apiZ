-- 用户进站消息明文留存（独立于提示词审计：纯记录、无扫描语义）。
-- 覆盖所有网关入口（messages / responses / chat_completions / gemini /
-- systemone / ws 回合），正文截断到 64K runes，保留期由应用层清理（默认 7 天）。
CREATE TABLE IF NOT EXISTS user_message_logs (
    id             BIGSERIAL PRIMARY KEY,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    request_id     VARCHAR(128) NOT NULL DEFAULT '',
    user_id        BIGINT,
    username       VARCHAR(255) NOT NULL DEFAULT '',
    user_email     VARCHAR(320) NOT NULL DEFAULT '',
    api_key_id     BIGINT,
    api_key_name   VARCHAR(255) NOT NULL DEFAULT '',
    group_id       BIGINT,
    group_name     VARCHAR(255) NOT NULL DEFAULT '',
    provider       VARCHAR(64) NOT NULL DEFAULT '',
    endpoint       VARCHAR(128) NOT NULL DEFAULT '',
    protocol       VARCHAR(64) NOT NULL DEFAULT '',
    model          VARCHAR(255) NOT NULL DEFAULT '',
    stream         BOOLEAN NOT NULL DEFAULT FALSE,
    stage          VARCHAR(32) NOT NULL DEFAULT 'http',
    content        TEXT NOT NULL DEFAULT '',
    content_length INT NOT NULL DEFAULT 0,
    truncated      BOOLEAN NOT NULL DEFAULT FALSE,
    CONSTRAINT chk_user_message_logs_nonnegative
        CHECK (content_length >= 0)
);

CREATE INDEX IF NOT EXISTS idx_user_message_logs_created
    ON user_message_logs(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_user_message_logs_user_created
    ON user_message_logs(user_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_user_message_logs_model
    ON user_message_logs(model);

-- 内容模糊检索走 trigram（扩展不可用时静默跳过，检索退化为顺序扫描）。
DO $$
BEGIN
    CREATE EXTENSION IF NOT EXISTS pg_trgm;
    CREATE INDEX IF NOT EXISTS idx_user_message_logs_content_trgm
        ON user_message_logs USING GIN (content gin_trgm_ops);
EXCEPTION WHEN OTHERS THEN
    RAISE NOTICE 'pg_trgm unavailable, content search falls back to seq scan: %', SQLERRM;
END $$;
