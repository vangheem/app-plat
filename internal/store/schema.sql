CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  email TEXT UNIQUE NOT NULL,
  name TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL DEFAULT 'user',
  llm_requires_review BOOLEAN NOT NULL DEFAULT TRUE,
  llm_cap_usd NUMERIC NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS api_keys (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  prefix TEXT NOT NULL,
  key_hash TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  revoked_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS api_keys_prefix_idx ON api_keys(prefix);

CREATE TABLE IF NOT EXISTS apps (
  id TEXT PRIMARY KEY,
  owner_id TEXT NOT NULL REFERENCES users(id),
  name TEXT NOT NULL,
  slug TEXT NOT NULL UNIQUE,
  port INT NOT NULL DEFAULT 8080,
  persistence JSONB NOT NULL DEFAULT '[]',
  status TEXT NOT NULL DEFAULT 'created',
  image TEXT NOT NULL DEFAULT '',
  detected_type TEXT NOT NULL DEFAULT '',
  public_url TEXT NOT NULL DEFAULT '',
  build_log TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  source_path TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS app_env (
  app_id TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  key TEXT NOT NULL,
  value TEXT NOT NULL,
  secret BOOLEAN NOT NULL DEFAULT FALSE,
  PRIMARY KEY (app_id, key)
);

CREATE TABLE IF NOT EXISTS app_resources (
  app_id TEXT PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
  pg_user TEXT,
  pg_password TEXT,
  pg_schema TEXT,
  pg_database TEXT,
  blob_bucket TEXT,
  blob_access_key TEXT,
  blob_secret_key TEXT,
  blob_quota_bytes BIGINT NOT NULL DEFAULT 1073741824,
  blob_used_bytes BIGINT NOT NULL DEFAULT 0,
  kv_username TEXT,
  kv_password TEXT,
  kv_prefix TEXT,
  llm_api_key TEXT,
  llm_enabled BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE TABLE IF NOT EXISTS builds (
  id TEXT PRIMARY KEY,
  app_id TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  status TEXT NOT NULL,
  image TEXT NOT NULL DEFAULT '',
  log TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS llm_models (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT FALSE,
  context_length INT NOT NULL DEFAULT 0,
  prompt_price TEXT NOT NULL DEFAULT '',
  completion_price TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS llm_grants (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id),
  app_id TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  status TEXT NOT NULL,
  spending_cap_usd NUMERIC NOT NULL DEFAULT 0,
  models JSONB NOT NULL DEFAULT '[]',
  note TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  decided_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS llm_grants_app_idx ON llm_grants(app_id, created_at DESC);

CREATE TABLE IF NOT EXISTS llm_usage (
  id BIGSERIAL PRIMARY KEY,
  user_id TEXT NOT NULL,
  app_id TEXT NOT NULL,
  model TEXT NOT NULL,
  prompt_tokens INT NOT NULL DEFAULT 0,
  completion_tokens INT NOT NULL DEFAULT 0,
  cost_usd NUMERIC NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS llm_usage_app_idx ON llm_usage(app_id, created_at DESC);
CREATE INDEX IF NOT EXISTS llm_usage_user_idx ON llm_usage(user_id, created_at DESC);
