-- Historical SQLite fixture for Runtime schema 028. It contains the durable
-- rows asserted by the upgrade matrix and the time-zone aware application header with the definition upgrade receipt ledger and the per-instance approval route table.
CREATE TABLE _audit_events (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, event TEXT NOT NULL,
  object_key TEXT, record_id TEXT, actor_id TEXT, role_key TEXT, summary TEXT,
  metadata_json TEXT NOT NULL, before_json TEXT NOT NULL, after_json TEXT NOT NULL,
  created_at TEXT NOT NULL
);
INSERT INTO _audit_events VALUES ('audit-028', 'workspace-primary', 'fixture.created', '', '', 'system', '', 'v028', '{}', '{}', '{}', '2026-09-07T00:00:00Z');

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
) VALUES ('workspace-primary','workflow-028','fixture','Fixture','manual','completed','{}','{}','{}','2026-09-07T00:00:00Z','2026-09-07T00:00:00Z');

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

CREATE TABLE _application_schema_projection (
  id TEXT PRIMARY KEY, contract_version TEXT NOT NULL DEFAULT '',
  source_hash TEXT NOT NULL DEFAULT '', schema_hash TEXT NOT NULL DEFAULT '',
  artifact_version TEXT NOT NULL DEFAULT '', materializer_version TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT '', template_id TEXT NOT NULL DEFAULT '',
  default_locale TEXT NOT NULL DEFAULT '', name TEXT NOT NULL DEFAULT '',
  materialized_at TEXT NOT NULL DEFAULT '', time_zone TEXT NOT NULL DEFAULT 'UTC'
);
INSERT INTO _application_schema_projection (id,template_id,name) VALUES ('current','legacy-028','Legacy application');
CREATE TABLE _dispatch_callback_receipts (
  id TEXT NOT NULL, workspace_id TEXT NOT NULL, runtime_id TEXT NOT NULL,
  method TEXT NOT NULL, path TEXT NOT NULL, idempotency_key TEXT NOT NULL,
  request_fingerprint TEXT NOT NULL, status TEXT NOT NULL, execution_id TEXT NOT NULL,
  downstream_id TEXT NOT NULL DEFAULT '', downstream_owner TEXT NOT NULL DEFAULT '',
  downstream_status TEXT NOT NULL DEFAULT '', lease_owner TEXT NOT NULL,
  lease_expires_at TEXT NOT NULL, fencing_token BIGINT NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL, expires_at TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (workspace_id, id)
);

CREATE TABLE _application_schema_upgrade_receipts (
  id TEXT PRIMARY KEY, from_version TEXT NOT NULL DEFAULT '', to_version TEXT NOT NULL DEFAULT '',
  object_key TEXT NOT NULL, column_key TEXT NOT NULL DEFAULT '', step_key TEXT NOT NULL,
  status TEXT NOT NULL, error_code TEXT NOT NULL DEFAULT '', backup_id TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL, completed_at TEXT NOT NULL DEFAULT ''
);

CREATE TABLE _workflow_route_steps (
  workspace_id TEXT NOT NULL, id TEXT NOT NULL, process_id TEXT NOT NULL,
  node_id TEXT NOT NULL, step_no INTEGER NOT NULL, step_key TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '', mode TEXT NOT NULL DEFAULT 'any',
  required_approvals INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL,
  assignee_snapshot_json TEXT NOT NULL DEFAULT '[]', configured_by TEXT NOT NULL DEFAULT '',
  configured_at TEXT NOT NULL DEFAULT '', configure_source TEXT NOT NULL DEFAULT '',
  node_instance_id TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  PRIMARY KEY (workspace_id, id)
);
INSERT INTO _report_export_prepare_receipts
  (id, operation_id, workspace_id, requester_user_id, use_case, report_key, object_key, audit_id, idempotency_key,
   request_fingerprint, status, payload_json, business_job_key, job_id, lease_owner, lease_expires_at, created_at, updated_at)
VALUES ('receipt-028', 'operation-028', 'workspace-primary', 'user-028', 'report_export_prepare', 'report-028', 'object-028', 'audit-028', 'key-028',
        'frozen-fingerprint', 'submitted', '{"historical":"payload"}', 'frozen-business-key-028', 'failed-job-028', '', '', '2026-09-07T00:00:00Z', '2026-09-07T00:00:00Z');
