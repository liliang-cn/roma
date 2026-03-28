export interface AgentProfile {
  id: string;
  display_name: string;
  command: string;
  aliases?: string[];
  capabilities?: string[];
  availability: string;
}

export interface StatusResponse {
  queue_items: number;
  sessions: number;
  artifacts: number;
  events: number;
  active_leases: number;
  released_leases: number;
  recovered_leases: number;
  pending_approval_tasks: number;
  recoverable_sessions: number;
  prepared_workspaces: number;
  released_workspaces: number;
  reclaimed_workspaces: number;
  merged_workspaces: number;
  sqlite_enabled: boolean;
  sqlite_path: string;
  sqlite_bytes: number;
}

export interface ACPStatusResponse {
  enabled: boolean;
  port: number;
}

export interface QueueRequest {
  id: string;
  prompt: string;
  mode?: string;
  starter_agent: string;
  delegates?: string[];
  working_dir: string;
  session_id?: string;
  task_id?: string;
  status: string;
  updated_at: string;
  created_at: string;
  artifact_ids?: string[];
  error?: string;
}

export interface BootstrapResponse {
  working_dir: string;
  daemon_available: boolean;
  embedded_daemon: boolean;
  last_daemon_error?: string;
  agent_config_path: string;
  agents: AgentProfile[];
  status: StatusResponse;
  queue: QueueRequest[];
  acp: ACPStatusResponse;
}

export interface SnapshotResponse {
  working_dir: string;
  daemon_available: boolean;
  embedded_daemon: boolean;
  last_daemon_error?: string;
  status: StatusResponse;
  queue: QueueRequest[];
  acp: ACPStatusResponse;
}

export interface RunSubmitRequest {
  prompt: string;
  mode: string;
  starter_agent: string;
  delegates: string[];
  working_dir: string;
  continuous: boolean;
  max_rounds: number;
  policy_override: boolean;
}

export interface SubmitResponse {
  job_id: string;
}

export interface RuntimeLiveSummary {
  state?: string;
  phase?: string;
  participant_count?: number;
  current_round?: number;
  current_task_id?: string;
  current_task_title?: string;
  current_task_state?: string;
  current_agent_id?: string;
  execution_id?: string;
  process_pid?: number;
  workspace_base_dir?: string;
  workspace_path?: string;
  workspace_mode?: string;
  workspace_requested_mode?: string;
  workspace_provider?: string;
  workspace_status?: string;
  started_at?: string;
  last_output_at?: string;
  last_event_at?: string;
  last_heartbeat_at?: string;
  last_event_type?: string;
  last_output_preview?: string;
}

export interface TaskRecord {
  id: string;
  title: string;
  state: string;
  agent_id: string;
  artifact_id?: string;
  updated_at?: string;
}

export interface WorkspacePrepared {
  session_id: string;
  task_id: string;
  base_dir: string;
  effective_dir: string;
  provider: string;
  status: string;
}

export interface SessionRecord {
  id: string;
  task_id: string;
  prompt: string;
  starter: string;
  delegates?: string[];
  status: string;
  working_dir: string;
  final_artifact_id?: string;
}

export interface ArtifactEnvelope {
  id: string;
  kind: string;
  payload_schema?: string;
  payload: unknown;
}

export interface PlanActionSummary {
  artifact_id: string;
  task_id?: string;
  event_type: string;
  reason?: string;
  changed_paths?: string[];
  violations?: string[];
  conflict?: boolean;
  conflict_detail?: string;
}

export interface QueueInspectResponse {
  job: QueueRequest;
  session?: SessionRecord;
  live?: RuntimeLiveSummary;
  artifact_count?: number;
  event_count?: number;
  tasks?: TaskRecord[];
  artifacts?: ArtifactEnvelope[];
  events?: Array<Record<string, unknown>>;
  workspaces?: WorkspacePrepared[];
  plans?: PlanActionSummary[];
}

export interface SessionInspectResponse {
  session: SessionRecord;
  live?: RuntimeLiveSummary;
  tasks?: TaskRecord[];
  artifacts?: ArtifactEnvelope[];
  events?: Array<Record<string, unknown>>;
  workspaces?: WorkspacePrepared[];
  plans?: PlanActionSummary[];
}

export interface ResultShowResponse {
  session: SessionRecord;
  pending?: boolean;
  message?: string;
  artifact: ArtifactEnvelope;
}

export interface PlanInboxEntry {
  artifact_id: string;
  session_id: string;
  task_id: string;
  goal?: string;
  status: string;
  human_approval_required: boolean;
  conflict?: boolean;
  conflict_kind?: string;
  conflict_detail?: string;
  remediation_hint?: string;
  conflict_paths?: string[];
  resolution_options?: string[];
}

export interface PlanPreviewRequest {
  session_id: string;
  task_id: string;
  artifact_id: string;
  policy_override: boolean;
}

export interface PlanApplyResponse {
  artifact_id: string;
  session_id: string;
  task_id: string;
  changed_paths: string[];
  patch_bytes: number;
  dry_run: boolean;
  applied: boolean;
  rolled_back: boolean;
  conflict: boolean;
  conflict_kind?: string;
  conflict_detail?: string;
  conflict_summary?: string;
  conflict_paths?: string[];
  remediation_hint?: string;
  resolution_options?: string[];
}
