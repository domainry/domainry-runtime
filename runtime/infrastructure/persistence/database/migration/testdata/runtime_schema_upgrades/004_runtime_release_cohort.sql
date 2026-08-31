CREATE TABLE _audit_events (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, event TEXT NOT NULL,
  object_key TEXT, record_id TEXT, actor_id TEXT, role_key TEXT, summary TEXT,
  metadata_json TEXT NOT NULL, before_json TEXT NOT NULL, after_json TEXT NOT NULL,
  created_at TEXT NOT NULL
);
INSERT INTO _audit_events VALUES ('audit-004', 'workspace-primary', 'fixture.created', '', '', 'system', '', 'v004', '{}', '{}', '{}', '2026-07-25T00:00:00Z');

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
) VALUES ('workspace-primary','workflow-004','fixture','Fixture','manual','completed','{}','{}','{}','2026-07-25T00:00:00Z','2026-07-25T00:00:00Z');

CREATE TABLE _identity_users (
  id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, name TEXT NOT NULL, email TEXT NOT NULL,
  employee_no TEXT NOT NULL DEFAULT '', phone TEXT NOT NULL DEFAULT '', gender TEXT NOT NULL DEFAULT '',
  hire_date TEXT NOT NULL DEFAULT '', job_title TEXT NOT NULL DEFAULT '', job_level TEXT NOT NULL DEFAULT '',
  employment_type TEXT NOT NULL DEFAULT '', employment_status TEXT NOT NULL DEFAULT 'active',
  department_id TEXT, department_path TEXT, manager_id TEXT, manager_path TEXT NOT NULL DEFAULT '',
  manager_ancestor_ids TEXT NOT NULL DEFAULT '[]', manager_depth INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX idx_identity_users_department ON _identity_users(workspace_id, department_id);
CREATE INDEX idx_identity_users_manager ON _identity_users(workspace_id, manager_id);
CREATE INDEX idx_identity_users_manager_path ON _identity_users(workspace_id, manager_path);
INSERT INTO _identity_users VALUES (
  'fixture-user-004', 'workspace-primary', 'Fixture Worker', 'worker@example.com', 'EMP-004', '', '',
  '2026-01-01', 'Operator', 'L2', 'full_time', 'active', 'operations', '/operations',
  NULL, '', '[]', 0, 'active', '2026-07-25T00:00:00Z', '2026-07-25T00:00:00Z'
);
