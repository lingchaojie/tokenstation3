CREATE TABLE IF NOT EXISTS web_chat_conversations (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title VARCHAR(200) NOT NULL DEFAULT '',
    default_model VARCHAR(100) NOT NULL DEFAULT '',
    default_provider VARCHAR(50) NOT NULL DEFAULT '',
    last_model VARCHAR(100) NOT NULL DEFAULT '',
    last_provider VARCHAR(50) NOT NULL DEFAULT '',
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    message_count INTEGER NOT NULL DEFAULT 0,
    last_message_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT web_chat_conversations_status_check CHECK (status IN ('active', 'archived', 'deleted'))
);

CREATE INDEX IF NOT EXISTS idx_web_chat_conversations_user_updated
    ON web_chat_conversations(user_id, updated_at DESC)
    WHERE status <> 'deleted';

CREATE TABLE IF NOT EXISTS web_chat_messages (
    id BIGSERIAL PRIMARY KEY,
    conversation_id BIGINT NOT NULL REFERENCES web_chat_conversations(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role VARCHAR(20) NOT NULL,
    model VARCHAR(100) NOT NULL DEFAULT '',
    provider VARCHAR(50) NOT NULL DEFAULT '',
    content_text TEXT NOT NULL DEFAULT '',
    content_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    status VARCHAR(20) NOT NULL DEFAULT 'completed',
    error_code VARCHAR(80),
    error_message TEXT,
    usage_log_id BIGINT REFERENCES usage_logs(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT web_chat_messages_role_check CHECK (role IN ('user', 'assistant', 'system')),
    CONSTRAINT web_chat_messages_status_check CHECK (status IN ('pending', 'streaming', 'completed', 'failed', 'canceled'))
);

CREATE INDEX IF NOT EXISTS idx_web_chat_messages_conversation_created
    ON web_chat_messages(conversation_id, created_at ASC);
CREATE INDEX IF NOT EXISTS idx_web_chat_messages_user_created
    ON web_chat_messages(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_web_chat_messages_usage_log_id
    ON web_chat_messages(usage_log_id)
    WHERE usage_log_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS web_chat_attachments (
    id BIGSERIAL PRIMARY KEY,
    message_id BIGINT REFERENCES web_chat_messages(id) ON DELETE SET NULL,
    conversation_id BIGINT REFERENCES web_chat_conversations(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind VARCHAR(20) NOT NULL,
    filename VARCHAR(255) NOT NULL,
    content_type VARCHAR(120) NOT NULL,
    size_bytes BIGINT NOT NULL,
    storage_key VARCHAR(500) NOT NULL,
    sha256 VARCHAR(64) NOT NULL,
    text_preview TEXT,
    status VARCHAR(20) NOT NULL DEFAULT 'uploaded',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT web_chat_attachments_kind_check CHECK (kind IN ('image', 'file')),
    CONSTRAINT web_chat_attachments_status_check CHECK (status IN ('uploaded', 'processed', 'unsupported', 'deleted'))
);

CREATE INDEX IF NOT EXISTS idx_web_chat_attachments_user_created
    ON web_chat_attachments(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_web_chat_attachments_message
    ON web_chat_attachments(message_id)
    WHERE message_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_web_chat_attachments_conversation
    ON web_chat_attachments(conversation_id)
    WHERE conversation_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS web_chat_artifacts (
    id BIGSERIAL PRIMARY KEY,
    message_id BIGINT NOT NULL REFERENCES web_chat_messages(id) ON DELETE CASCADE,
    conversation_id BIGINT NOT NULL REFERENCES web_chat_conversations(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    filename VARCHAR(255) NOT NULL,
    content_type VARCHAR(120) NOT NULL,
    size_bytes BIGINT NOT NULL,
    storage_key VARCHAR(500) NOT NULL,
    sha256 VARCHAR(64) NOT NULL,
    source VARCHAR(30) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT web_chat_artifacts_source_check CHECK (source IN ('model_output', 'image_output', 'generated_file'))
);

CREATE INDEX IF NOT EXISTS idx_web_chat_artifacts_message
    ON web_chat_artifacts(message_id);
CREATE INDEX IF NOT EXISTS idx_web_chat_artifacts_user_created
    ON web_chat_artifacts(user_id, created_at DESC);

CREATE UNIQUE INDEX IF NOT EXISTS idx_api_keys_web_chat_user_group_unique
    ON api_keys(user_id, group_id)
    WHERE key_type = 'web_chat' AND group_id IS NOT NULL AND deleted_at IS NULL;
