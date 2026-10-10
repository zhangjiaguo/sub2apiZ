-- 用户消息页面的查看授权：被授权用户可以查看指定分组范围的进站消息与
-- 该分组的 token 用量排行（运营给客服/组长等角色开视野用）。
-- 与 user_allowed_groups（Key 绑定可见性）语义无关，绝不可混用。
CREATE TABLE IF NOT EXISTS user_message_viewers (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_id   BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    created_by BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_user_message_viewers UNIQUE (user_id, group_id)
);

CREATE INDEX IF NOT EXISTS idx_user_message_viewers_user
    ON user_message_viewers(user_id);
CREATE INDEX IF NOT EXISTS idx_user_message_viewers_group
    ON user_message_viewers(group_id);
