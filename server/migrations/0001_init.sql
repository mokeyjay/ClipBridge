-- 0001_init.sql — ClipBridge initial schema.
-- Conventions:
--   * All business primary keys are UUID strings (lowercase).
--   * All timestamps are INTEGER Unix seconds in UTC. The API layer converts to
--     RFC 3339 at its boundary; the database stays unambiguous about timezone.
--   * Booleans are INTEGER 0/1. allowed_types is a JSON array of content types.
-- Clipboard bodies are NEVER stored here — only in data/ as temporary ciphertext.

CREATE TABLE IF NOT EXISTS admins (
    id            TEXT PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'active', -- active | disabled
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS web_sessions (
    id           TEXT PRIMARY KEY,
    subject_type TEXT NOT NULL,                   -- admin | user
    subject_id   TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    expires_at   INTEGER NOT NULL,
    created_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_web_sessions_subject ON web_sessions(subject_type, subject_id);
CREATE INDEX IF NOT EXISTS idx_web_sessions_expires ON web_sessions(expires_at);

-- Single-row instance configuration. Enforced single row via fixed primary key.
-- allowed_types is the instance-level hard ceiling: a content type the admin
-- disables here can never be re-enabled by a user/device (intersected in
-- EffectiveConfig).
CREATE TABLE IF NOT EXISTS server_settings (
    id                      INTEGER PRIMARY KEY CHECK (id = 1),
    server_id               TEXT NOT NULL,
    server_name             TEXT NOT NULL DEFAULT 'ClipBridge',
    registration_enabled    INTEGER NOT NULL DEFAULT 0,
    max_sync_size_bytes     INTEGER NOT NULL DEFAULT 104857600,  -- 100 MiB
    allowed_types           TEXT NOT NULL DEFAULT '["text","image","file","rich_text"]',
    ciphertext_ttl_seconds  INTEGER NOT NULL DEFAULT 300,
    sync_log_retention_days INTEGER NOT NULL DEFAULT 30,
    created_at              INTEGER NOT NULL,
    updated_at              INTEGER NOT NULL
);

-- User-level sync-policy template. Devices inherit these defaults or override
-- per field. file_ttl_days is the received-file retention default (1..365).
CREATE TABLE IF NOT EXISTS user_settings (
    user_id                      TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    max_sync_size_bytes          INTEGER NOT NULL DEFAULT 104857600, -- 100 MiB
    allowed_types                TEXT NOT NULL DEFAULT '["text","image","file","rich_text"]',
    max_auto_upload_size_bytes   INTEGER NOT NULL DEFAULT 10485760,  -- 10 MiB
    max_auto_download_size_bytes INTEGER NOT NULL DEFAULT 10485760,  -- 10 MiB
    file_ttl_days                INTEGER NOT NULL DEFAULT 7,
    updated_at                   INTEGER NOT NULL
);

-- Per-device inherit/override flags for the inheritable sync policy fields.
CREATE TABLE IF NOT EXISTS device_settings (
    device_id                            TEXT PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
    max_sync_size_inherit                INTEGER NOT NULL DEFAULT 1,
    max_sync_size_bytes                  INTEGER,
    allowed_types_inherit                INTEGER NOT NULL DEFAULT 1,
    allowed_types                        TEXT,
    max_auto_upload_size_inherit         INTEGER NOT NULL DEFAULT 1,
    max_auto_upload_size_bytes           INTEGER,
    max_auto_download_size_inherit       INTEGER NOT NULL DEFAULT 1,
    max_auto_download_size_bytes         INTEGER,
    updated_at                           INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS devices (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    platform        TEXT NOT NULL,                 -- darwin | windows
    client_version  TEXT NOT NULL DEFAULT '',
    hpke_public_key TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'active',-- active | disabled | revoked
    last_seen_at    INTEGER,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_devices_user ON devices(user_id);

CREATE TABLE IF NOT EXISTS device_tokens (
    id           TEXT PRIMARY KEY,
    device_id    TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL UNIQUE,
    expires_at   INTEGER,
    revoked_at   INTEGER,
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER
);
CREATE INDEX IF NOT EXISTS idx_device_tokens_device ON device_tokens(device_id);

CREATE TABLE IF NOT EXISTS pairing_codes (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash  TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'active',     -- active | consumed | cancelled | expired
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_pairing_codes_status ON pairing_codes(status, expires_at);
-- At most one active pairing code per user.
CREATE UNIQUE INDEX IF NOT EXISTS idx_pairing_codes_one_active
    ON pairing_codes(user_id) WHERE status = 'active';

CREATE TABLE IF NOT EXISTS pairing_requests (
    id                  TEXT PRIMARY KEY,
    pairing_code_id     TEXT NOT NULL REFERENCES pairing_codes(id) ON DELETE CASCADE,
    device_name         TEXT NOT NULL,
    platform            TEXT NOT NULL,
    client_version      TEXT NOT NULL DEFAULT '',
    hpke_public_key     TEXT NOT NULL,
    poll_token_hash     TEXT NOT NULL,
    status              TEXT NOT NULL DEFAULT 'pending', -- pending | confirmed | rejected | expired | claimed
    confirmed_device_id TEXT REFERENCES devices(id) ON DELETE SET NULL,
    expires_at          INTEGER NOT NULL,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_pairing_requests_code ON pairing_requests(pairing_code_id);

-- One uploaded ciphertext's routing/state metadata. chunk_size_bytes/total_chunks
-- frame the chunked-AEAD stream; encrypted_metadata is the DEK-sealed, opaque
-- per-item metadata blob (filename, image dimensions, rich-text flags) the server
-- never decrypts.
CREATE TABLE IF NOT EXISTS clipboard_items (
    id                    TEXT PRIMARY KEY,
    user_id               TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source_device_id      TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    content_type          TEXT NOT NULL,           -- text | image | file | rich_text
    ciphertext_size_bytes INTEGER NOT NULL,
    ciphertext_path       TEXT NOT NULL,
    ciphertext_sha256     TEXT NOT NULL,
    chunk_size_bytes      INTEGER NOT NULL DEFAULT 0,
    total_chunks          INTEGER NOT NULL DEFAULT 0,
    encrypted_metadata    TEXT NOT NULL DEFAULT '',
    status                TEXT NOT NULL DEFAULT 'active', -- active | completed | expired
    expires_at            INTEGER NOT NULL,
    created_at            INTEGER NOT NULL,
    completed_at          INTEGER
);
CREATE INDEX IF NOT EXISTS idx_clipboard_items_expires ON clipboard_items(expires_at);
CREATE INDEX IF NOT EXISTS idx_clipboard_items_status ON clipboard_items(status);

CREATE TABLE IF NOT EXISTS clipboard_deliveries (
    id                TEXT PRIMARY KEY,
    clipboard_item_id TEXT NOT NULL REFERENCES clipboard_items(id) ON DELETE CASCADE,
    target_device_id  TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    wrapped_dek       TEXT NOT NULL,
    status            TEXT NOT NULL DEFAULT 'pending', -- pending | acked | rejected | expired
    reject_reason     TEXT,
    created_at        INTEGER NOT NULL,
    resolved_at       INTEGER
);
CREATE INDEX IF NOT EXISTS idx_deliveries_target_status ON clipboard_deliveries(target_device_id, status);
CREATE INDEX IF NOT EXISTS idx_deliveries_item ON clipboard_deliveries(clipboard_item_id);

CREATE TABLE IF NOT EXISTS sync_logs (
    id                    TEXT PRIMARY KEY,
    user_id               TEXT NOT NULL,
    item_id               TEXT,
    source_device_id      TEXT,
    target_device_id      TEXT,
    event_type            TEXT NOT NULL,
    content_type          TEXT,
    ciphertext_size_bytes INTEGER,
    result                TEXT NOT NULL,           -- success | failure
    error_code            TEXT,
    created_at            INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sync_logs_created ON sync_logs(created_at);
CREATE INDEX IF NOT EXISTS idx_sync_logs_user ON sync_logs(user_id);
