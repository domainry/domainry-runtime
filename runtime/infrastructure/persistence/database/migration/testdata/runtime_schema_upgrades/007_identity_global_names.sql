CREATE TABLE _audit_events (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, event TEXT NOT NULL,
  object_key TEXT, record_id TEXT, actor_id TEXT, role_key TEXT, summary TEXT,
  metadata_json TEXT NOT NULL, before_json TEXT NOT NULL, after_json TEXT NOT NULL,
  created_at TEXT NOT NULL
);
INSERT INTO _audit_events VALUES ('audit-007', 'default', 'fixture.created', '', '', 'system', '', 'v007', '{}', '{}', '{}', '2026-07-25T00:00:00Z');

CREATE TABLE _workflow_executions (
  workspace_id TEXT NOT NULL, id TEXT NOT NULL, workflow_key TEXT NOT NULL,
  name TEXT NOT NULL, trigger TEXT NOT NULL, status TEXT NOT NULL, action_type TEXT,
  action_json TEXT NOT NULL, payload_json TEXT NOT NULL, result_json TEXT NOT NULL,
  process_id TEXT, node_id TEXT, object_key TEXT, record_id TEXT, actor_id TEXT,
  run_as TEXT, idempotency_key TEXT, attempt INTEGER NOT NULL DEFAULT 0,
  max_attempts INTEGER NOT NULL DEFAULT 0, next_run_at TEXT, last_error TEXT,
  lease_owner TEXT NOT NULL DEFAULT '', lease_expires_at TEXT NOT NULL DEFAULT '',
  fencing_token BIGINT NOT NULL DEFAULT 0, message TEXT,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
INSERT INTO _workflow_executions (
  workspace_id,id,workflow_key,name,trigger,status,action_json,payload_json,result_json,created_at,updated_at
) VALUES ('default','workflow-007','fixture','Fixture','manual','completed','{}','{}','{}','2026-07-25T00:00:00Z','2026-07-25T00:00:00Z');

CREATE TABLE _identity_users (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, name TEXT NOT NULL,
  given_name TEXT NOT NULL DEFAULT '', middle_name TEXT NOT NULL DEFAULT '',
  family_name TEXT NOT NULL DEFAULT '', name_prefix TEXT NOT NULL DEFAULT '',
  name_suffix TEXT NOT NULL DEFAULT '', native_name TEXT NOT NULL DEFAULT '',
  name_locale TEXT NOT NULL DEFAULT '', email TEXT NOT NULL,
  phone TEXT NOT NULL DEFAULT '', status TEXT NOT NULL,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
INSERT INTO _identity_users VALUES (
  'fixture-user-007', 'default', '单名', '', '', '', '', '', '', '',
  'account-007@example.com', '', 'active',
  '2026-07-25T00:00:00Z', '2026-07-25T00:00:00Z'
);
