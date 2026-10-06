CREATE TABLE IF NOT EXISTS hosts (
 id text PRIMARY KEY, name text NOT NULL, token_hash text UNIQUE, enrollment_hash text UNIQUE,
 enrollment_expires timestamptz, revoked boolean NOT NULL DEFAULT false,
 last_seen timestamptz, capabilities jsonb NOT NULL DEFAULT '{}', snapshot jsonb
);
CREATE TABLE IF NOT EXISTS sessions (token_hash text PRIMARY KEY, expires timestamptz NOT NULL);
CREATE TABLE IF NOT EXISTS tasks (
 id text PRIMARY KEY, host_id text NOT NULL REFERENCES hosts(id), key text NOT NULL,
 digest text NOT NULL, body jsonb NOT NULL, state text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(host_id,key)
);
CREATE INDEX IF NOT EXISTS tasks_dispatch ON tasks(host_id,state);
CREATE TABLE IF NOT EXISTS audit (
 id bigserial PRIMARY KEY, at timestamptz NOT NULL DEFAULT now(), source text NOT NULL,
 host_id text NOT NULL, action text NOT NULL, request_id text NOT NULL, state text NOT NULL
);
CREATE TABLE IF NOT EXISTS projects (
 id text PRIMARY KEY, host_id text NOT NULL REFERENCES hosts(id), path text NOT NULL, name text NOT NULL,
 UNIQUE(host_id,path)
);
CREATE TABLE IF NOT EXISTS analyses (
 id text PRIMARY KEY, created_at timestamptz NOT NULL DEFAULT now(), body jsonb NOT NULL
);
ALTER TABLE hosts ADD COLUMN IF NOT EXISTS identity_generation integer NOT NULL DEFAULT 1;
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS identity_generation integer NOT NULL DEFAULT 1;
