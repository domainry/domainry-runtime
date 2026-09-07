-- Historical SQLite fixture for Runtime schema 022. It contains the durable
-- rows asserted by the upgrade matrix and the 022 report prepare receipt shape.
CREATE TABLE _audit_events (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, event TEXT NOT NULL,
  object_key TEXT, record_id TEXT, actor_id TEXT, role_key TEXT, summary TEXT,
  metadata_json TEXT NOT NULL, before_json TEXT NOT NULL, after_json TEXT NOT NULL,
  created_at TEXT NOT NULL
);
INSERT INTO _audit_events VALUES ('audit-022', 'workspace-primary', 'fixture.created', '', '', 'system', '', 'v022', '{}', '{}', '{}', '2026-09-07T00:00:00Z');

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
) VALUES ('workspace-primary','workflow-022','fixture','Fixture','manual','completed','{}','{}','{}','2026-09-07T00:00:00Z','2026-09-07T00:00:00Z');

CREATE TABLE _workspaces (
  id TEXT PRIMARY KEY, canonical_code TEXT NOT NULL, name TEXT NOT NULL,
  status TEXT NOT NULL, initial_installation_identity TEXT NULL,
  revision INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
INSERT INTO _workspaces VALUES ('workspace-primary','primary','Primary','active',NULL,1,'2026-09-07T00:00:00Z','2026-09-07T00:00:00Z');

CREATE TABLE _report_export_prepare_receipts (
  id TEXT NOT NULL, operation_id TEXT NOT NULL, workspace_id TEXT NOT NULL,
  requester_user_id TEXT NOT NULL, use_case TEXT NOT NULL, report_key TEXT NOT NULL,
  object_key TEXT NOT NULL, audit_id TEXT NOT NULL, idempotency_key TEXT NOT NULL,
  request_fingerprint TEXT NOT NULL, status TEXT NOT NULL, payload_json TEXT NOT NULL DEFAULT '',
  business_job_key TEXT NOT NULL DEFAULT '', job_id TEXT NOT NULL DEFAULT '',
  completion_artifact_id TEXT NOT NULL DEFAULT '', completion_fingerprint TEXT NOT NULL DEFAULT '',
  terminal_error_code TEXT NOT NULL DEFAULT '', lease_owner TEXT NOT NULL,
  lease_expires_at TEXT NOT NULL, fencing_token BIGINT NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL, expires_at TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (workspace_id, id)
);
