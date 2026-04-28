CREATE TABLE IF NOT EXISTS schema_migrations (
  version TEXT PRIMARY KEY,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS memories (
  id           UUID PRIMARY KEY,
  user_id      TEXT NOT NULL DEFAULT 'default',
  source       TEXT,
  content      TEXT NOT NULL,
  metadata     JSONB NOT NULL DEFAULT '{}',
  importance   REAL NOT NULL DEFAULT 0.5,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  accessed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  access_count INT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS chunks (
  id           UUID PRIMARY KEY,
  memory_id    UUID NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
  user_id      TEXT NOT NULL,
  ord          INT NOT NULL,
  content      TEXT NOT NULL,
  tsv          tsvector GENERATED ALWAYS AS (to_tsvector('english', content)) STORED,
  content_hash BYTEA NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS chunks_tsv_gin   ON chunks USING GIN(tsv);
CREATE INDEX IF NOT EXISTS chunks_user      ON chunks(user_id);
CREATE UNIQUE INDEX IF NOT EXISTS chunks_user_hash ON chunks(user_id, content_hash);

CREATE TABLE IF NOT EXISTS pending_vectors (
  chunk_id    UUID PRIMARY KEY REFERENCES chunks(id) ON DELETE CASCADE,
  attempts    INT NOT NULL DEFAULT 0,
  last_error  TEXT,
  enqueued_at TIMESTAMPTZ NOT NULL DEFAULT now()
);