export namespace api {
	
	export class ACPStatusResponse {
	    enabled: boolean;
	    port: number;
	
	    static createFrom(source: any = {}) {
	        return new ACPStatusResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.port = source["port"];
	    }
	}
	export class CuriaReviewerSummary {
	    reviewer_id: string;
	    effective_weight: number;
	    review_count?: number;
	    alignment_count?: number;
	    veto_count?: number;
	    arbitration_count?: number;
	
	    static createFrom(source: any = {}) {
	        return new CuriaReviewerSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.reviewer_id = source["reviewer_id"];
	        this.effective_weight = source["effective_weight"];
	        this.review_count = source["review_count"];
	        this.alignment_count = source["alignment_count"];
	        this.veto_count = source["veto_count"];
	        this.arbitration_count = source["arbitration_count"];
	    }
	}
	export class CuriaScoreSummary {
	    proposal_id: string;
	    raw_score: number;
	    weighted_score: number;
	    veto_count: number;
	    reviewer_count: number;
	
	    static createFrom(source: any = {}) {
	        return new CuriaScoreSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.proposal_id = source["proposal_id"];
	        this.raw_score = source["raw_score"];
	        this.weighted_score = source["weighted_score"];
	        this.veto_count = source["veto_count"];
	        this.reviewer_count = source["reviewer_count"];
	    }
	}
	export class CuriaSummary {
	    dispute: boolean;
	    dispute_class?: string;
	    arbitration_strategy?: string;
	    arbitration_confidence?: string;
	    consensus_strength?: string;
	    arbitrated?: boolean;
	    arbitrator_id?: string;
	    critical_veto: boolean;
	    top_score_gap: number;
	    dispute_reasons?: string[];
	    escalation_reasons?: string[];
	    winning_mode?: string;
	    selected_proposal_ids?: string[];
	    competing_proposal_ids?: string[];
	    approval_reason?: string;
	    risk_flags?: string[];
	    review_questions?: string[];
	    dissent_summary?: string[];
	    candidate_summaries?: artifacts.CuriaCandidateSummary[];
	    reviewer_breakdown?: artifacts.CuriaReviewContribution[];
	    reviewer_weights?: CuriaReviewerSummary[];
	    scoreboard?: CuriaScoreSummary[];
	
	    static createFrom(source: any = {}) {
	        return new CuriaSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.dispute = source["dispute"];
	        this.dispute_class = source["dispute_class"];
	        this.arbitration_strategy = source["arbitration_strategy"];
	        this.arbitration_confidence = source["arbitration_confidence"];
	        this.consensus_strength = source["consensus_strength"];
	        this.arbitrated = source["arbitrated"];
	        this.arbitrator_id = source["arbitrator_id"];
	        this.critical_veto = source["critical_veto"];
	        this.top_score_gap = source["top_score_gap"];
	        this.dispute_reasons = source["dispute_reasons"];
	        this.escalation_reasons = source["escalation_reasons"];
	        this.winning_mode = source["winning_mode"];
	        this.selected_proposal_ids = source["selected_proposal_ids"];
	        this.competing_proposal_ids = source["competing_proposal_ids"];
	        this.approval_reason = source["approval_reason"];
	        this.risk_flags = source["risk_flags"];
	        this.review_questions = source["review_questions"];
	        this.dissent_summary = source["dissent_summary"];
	        this.candidate_summaries = this.convertValues(source["candidate_summaries"], artifacts.CuriaCandidateSummary);
	        this.reviewer_breakdown = this.convertValues(source["reviewer_breakdown"], artifacts.CuriaReviewContribution);
	        this.reviewer_weights = this.convertValues(source["reviewer_weights"], CuriaReviewerSummary);
	        this.scoreboard = this.convertValues(source["scoreboard"], CuriaScoreSummary);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class PlanActionSummary {
	    artifact_id: string;
	    task_id?: string;
	    event_type: string;
	    reason?: string;
	    changed_paths?: string[];
	    violations?: string[];
	    conflict?: boolean;
	    conflict_detail?: string;
	    conflict_paths?: string[];
	    conflict_context?: workspace.ConflictSnippet[];
	    required_checks?: string[];
	    occurred_at: string;
	
	    static createFrom(source: any = {}) {
	        return new PlanActionSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.artifact_id = source["artifact_id"];
	        this.task_id = source["task_id"];
	        this.event_type = source["event_type"];
	        this.reason = source["reason"];
	        this.changed_paths = source["changed_paths"];
	        this.violations = source["violations"];
	        this.conflict = source["conflict"];
	        this.conflict_detail = source["conflict_detail"];
	        this.conflict_paths = source["conflict_paths"];
	        this.conflict_context = this.convertValues(source["conflict_context"], workspace.ConflictSnippet);
	        this.required_checks = source["required_checks"];
	        this.occurred_at = source["occurred_at"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class PlanInboxEntry {
	    artifact_id: string;
	    session_id: string;
	    task_id: string;
	    goal?: string;
	    status: string;
	    human_approval_required: boolean;
	    expected_files?: string[];
	    forbidden_paths?: string[];
	    last_event_type?: string;
	    last_reason?: string;
	    last_occurred_at?: string;
	    violations?: string[];
	    conflict?: boolean;
	    conflict_kind?: string;
	    conflict_detail?: string;
	    conflict_summary?: string;
	    conflict_paths?: string[];
	    conflict_context?: workspace.ConflictSnippet[];
	    remediation_hint?: string;
	    resolution_options?: string[];
	    resolution_steps?: plans.ResolutionStep[];
	
	    static createFrom(source: any = {}) {
	        return new PlanInboxEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.artifact_id = source["artifact_id"];
	        this.session_id = source["session_id"];
	        this.task_id = source["task_id"];
	        this.goal = source["goal"];
	        this.status = source["status"];
	        this.human_approval_required = source["human_approval_required"];
	        this.expected_files = source["expected_files"];
	        this.forbidden_paths = source["forbidden_paths"];
	        this.last_event_type = source["last_event_type"];
	        this.last_reason = source["last_reason"];
	        this.last_occurred_at = source["last_occurred_at"];
	        this.violations = source["violations"];
	        this.conflict = source["conflict"];
	        this.conflict_kind = source["conflict_kind"];
	        this.conflict_detail = source["conflict_detail"];
	        this.conflict_summary = source["conflict_summary"];
	        this.conflict_paths = source["conflict_paths"];
	        this.conflict_context = this.convertValues(source["conflict_context"], workspace.ConflictSnippet);
	        this.remediation_hint = source["remediation_hint"];
	        this.resolution_options = source["resolution_options"];
	        this.resolution_steps = this.convertValues(source["resolution_steps"], plans.ResolutionStep);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SemanticSummary {
	    intent?: string;
	    risk?: string;
	    needs_approval?: boolean;
	    recommend_curia?: boolean;
	    summary?: string;
	    classifier_agent_id?: string;
	    source_signal?: string;
	    source_reason?: string;
	    source_confidence?: string;
	    artifact_id?: string;
	
	    static createFrom(source: any = {}) {
	        return new SemanticSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.intent = source["intent"];
	        this.risk = source["risk"];
	        this.needs_approval = source["needs_approval"];
	        this.recommend_curia = source["recommend_curia"];
	        this.summary = source["summary"];
	        this.classifier_agent_id = source["classifier_agent_id"];
	        this.source_signal = source["source_signal"];
	        this.source_reason = source["source_reason"];
	        this.source_confidence = source["source_confidence"];
	        this.artifact_id = source["artifact_id"];
	    }
	}
	export class RuntimeLiveSummary {
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
	    // Go type: time
	    started_at?: any;
	    // Go type: time
	    last_output_at?: any;
	    // Go type: time
	    last_event_at?: any;
	    // Go type: time
	    last_heartbeat_at?: any;
	    last_event_type?: string;
	    last_output_preview?: string;
	
	    static createFrom(source: any = {}) {
	        return new RuntimeLiveSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.phase = source["phase"];
	        this.participant_count = source["participant_count"];
	        this.current_round = source["current_round"];
	        this.current_task_id = source["current_task_id"];
	        this.current_task_title = source["current_task_title"];
	        this.current_task_state = source["current_task_state"];
	        this.current_agent_id = source["current_agent_id"];
	        this.execution_id = source["execution_id"];
	        this.process_pid = source["process_pid"];
	        this.workspace_base_dir = source["workspace_base_dir"];
	        this.workspace_path = source["workspace_path"];
	        this.workspace_mode = source["workspace_mode"];
	        this.workspace_requested_mode = source["workspace_requested_mode"];
	        this.workspace_provider = source["workspace_provider"];
	        this.workspace_status = source["workspace_status"];
	        this.started_at = this.convertValues(source["started_at"], null);
	        this.last_output_at = this.convertValues(source["last_output_at"], null);
	        this.last_event_at = this.convertValues(source["last_event_at"], null);
	        this.last_heartbeat_at = this.convertValues(source["last_heartbeat_at"], null);
	        this.last_event_type = source["last_event_type"];
	        this.last_output_preview = source["last_output_preview"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class QueueInspectResponse {
	    job: queue.Request;
	    session?: history.SessionRecord;
	    lease?: scheduler.LeaseRecord;
	    pending_approval_task_ids?: string[];
	    approval_resume_ready: boolean;
	    live?: RuntimeLiveSummary;
	    artifact_count?: number;
	    event_count?: number;
	    tasks?: domain.TaskRecord[];
	    artifacts?: domain.ArtifactEnvelope[];
	    events?: events.Record[];
	    workspaces?: workspace.Prepared[];
	    plans?: PlanActionSummary[];
	    curia?: CuriaSummary;
	    semantic?: SemanticSummary;
	
	    static createFrom(source: any = {}) {
	        return new QueueInspectResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.job = this.convertValues(source["job"], queue.Request);
	        this.session = this.convertValues(source["session"], history.SessionRecord);
	        this.lease = this.convertValues(source["lease"], scheduler.LeaseRecord);
	        this.pending_approval_task_ids = source["pending_approval_task_ids"];
	        this.approval_resume_ready = source["approval_resume_ready"];
	        this.live = this.convertValues(source["live"], RuntimeLiveSummary);
	        this.artifact_count = source["artifact_count"];
	        this.event_count = source["event_count"];
	        this.tasks = this.convertValues(source["tasks"], domain.TaskRecord);
	        this.artifacts = this.convertValues(source["artifacts"], domain.ArtifactEnvelope);
	        this.events = this.convertValues(source["events"], events.Record);
	        this.workspaces = this.convertValues(source["workspaces"], workspace.Prepared);
	        this.plans = this.convertValues(source["plans"], PlanActionSummary);
	        this.curia = this.convertValues(source["curia"], CuriaSummary);
	        this.semantic = this.convertValues(source["semantic"], SemanticSummary);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ResultShowResponse {
	    session: history.SessionRecord;
	    pending?: boolean;
	    message?: string;
	    artifact?: domain.ArtifactEnvelope;
	
	    static createFrom(source: any = {}) {
	        return new ResultShowResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.session = this.convertValues(source["session"], history.SessionRecord);
	        this.pending = source["pending"];
	        this.message = source["message"];
	        this.artifact = this.convertValues(source["artifact"], domain.ArtifactEnvelope);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	export class SessionInspectResponse {
	    session: history.SessionRecord;
	    lease?: scheduler.LeaseRecord;
	    pending_approval_task_ids?: string[];
	    approval_resume_ready: boolean;
	    live?: RuntimeLiveSummary;
	    tasks?: domain.TaskRecord[];
	    artifacts?: domain.ArtifactEnvelope[];
	    events?: events.Record[];
	    workspaces?: workspace.Prepared[];
	    plans?: PlanActionSummary[];
	    curia?: CuriaSummary;
	    semantic?: SemanticSummary;
	
	    static createFrom(source: any = {}) {
	        return new SessionInspectResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.session = this.convertValues(source["session"], history.SessionRecord);
	        this.lease = this.convertValues(source["lease"], scheduler.LeaseRecord);
	        this.pending_approval_task_ids = source["pending_approval_task_ids"];
	        this.approval_resume_ready = source["approval_resume_ready"];
	        this.live = this.convertValues(source["live"], RuntimeLiveSummary);
	        this.tasks = this.convertValues(source["tasks"], domain.TaskRecord);
	        this.artifacts = this.convertValues(source["artifacts"], domain.ArtifactEnvelope);
	        this.events = this.convertValues(source["events"], events.Record);
	        this.workspaces = this.convertValues(source["workspaces"], workspace.Prepared);
	        this.plans = this.convertValues(source["plans"], PlanActionSummary);
	        this.curia = this.convertValues(source["curia"], CuriaSummary);
	        this.semantic = this.convertValues(source["semantic"], SemanticSummary);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class StatusResponse {
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
	
	    static createFrom(source: any = {}) {
	        return new StatusResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.queue_items = source["queue_items"];
	        this.sessions = source["sessions"];
	        this.artifacts = source["artifacts"];
	        this.events = source["events"];
	        this.active_leases = source["active_leases"];
	        this.released_leases = source["released_leases"];
	        this.recovered_leases = source["recovered_leases"];
	        this.pending_approval_tasks = source["pending_approval_tasks"];
	        this.recoverable_sessions = source["recoverable_sessions"];
	        this.prepared_workspaces = source["prepared_workspaces"];
	        this.released_workspaces = source["released_workspaces"];
	        this.reclaimed_workspaces = source["reclaimed_workspaces"];
	        this.merged_workspaces = source["merged_workspaces"];
	        this.sqlite_enabled = source["sqlite_enabled"];
	        this.sqlite_path = source["sqlite_path"];
	        this.sqlite_bytes = source["sqlite_bytes"];
	    }
	}
	export class SubmitResponse {
	    job_id: string;
	
	    static createFrom(source: any = {}) {
	        return new SubmitResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.job_id = source["job_id"];
	    }
	}

}

export namespace artifacts {
	
	export class CuriaCandidateSummary {
	    proposal_id: string;
	    summary: string;
	    raw_score: number;
	    weighted_score: number;
	    veto_count: number;
	
	    static createFrom(source: any = {}) {
	        return new CuriaCandidateSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.proposal_id = source["proposal_id"];
	        this.summary = source["summary"];
	        this.raw_score = source["raw_score"];
	        this.weighted_score = source["weighted_score"];
	        this.veto_count = source["veto_count"];
	    }
	}
	export class CuriaReviewContribution {
	    reviewer_id: string;
	    target_proposal_id: string;
	    raw_score: number;
	    reviewer_weight: number;
	    weighted_score: number;
	    veto: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CuriaReviewContribution(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.reviewer_id = source["reviewer_id"];
	        this.target_proposal_id = source["target_proposal_id"];
	        this.raw_score = source["raw_score"];
	        this.reviewer_weight = source["reviewer_weight"];
	        this.weighted_score = source["weighted_score"];
	        this.veto = source["veto"];
	    }
	}

}

export namespace domain {
	
	export class AgentProfile {
	    id: string;
	    display_name: string;
	    command: string;
	    args?: string[];
	    healthcheck_args?: string[];
	    aliases?: string[];
	    use_pty?: boolean;
	    supports_mcp: boolean;
	    supports_json_output: boolean;
	    capabilities?: string[];
	    metadata?: Record<string, string>;
	    availability: string;
	
	    static createFrom(source: any = {}) {
	        return new AgentProfile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.display_name = source["display_name"];
	        this.command = source["command"];
	        this.args = source["args"];
	        this.healthcheck_args = source["healthcheck_args"];
	        this.aliases = source["aliases"];
	        this.use_pty = source["use_pty"];
	        this.supports_mcp = source["supports_mcp"];
	        this.supports_json_output = source["supports_json_output"];
	        this.capabilities = source["capabilities"];
	        this.metadata = source["metadata"];
	        this.availability = source["availability"];
	    }
	}
	export class AttachmentRef {
	    name: string;
	    media_type: string;
	    blob_ref: string;
	    size_bytes: number;
	    checksum: string;
	    purpose: string;
	
	    static createFrom(source: any = {}) {
	        return new AttachmentRef(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.media_type = source["media_type"];
	        this.blob_ref = source["blob_ref"];
	        this.size_bytes = source["size_bytes"];
	        this.checksum = source["checksum"];
	        this.purpose = source["purpose"];
	    }
	}
	export class Producer {
	    agent_id: string;
	    role: string;
	    run_id?: string;
	
	    static createFrom(source: any = {}) {
	        return new Producer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.agent_id = source["agent_id"];
	        this.role = source["role"];
	        this.run_id = source["run_id"];
	    }
	}
	export class ArtifactEnvelope {
	    id: string;
	    kind: string;
	    schema_version: string;
	    producer: Producer;
	    session_id: string;
	    task_id: string;
	    // Go type: time
	    created_at: any;
	    payload_schema: string;
	    payload: any;
	    attachments?: AttachmentRef[];
	    checksum: string;
	
	    static createFrom(source: any = {}) {
	        return new ArtifactEnvelope(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.schema_version = source["schema_version"];
	        this.producer = this.convertValues(source["producer"], Producer);
	        this.session_id = source["session_id"];
	        this.task_id = source["task_id"];
	        this.created_at = this.convertValues(source["created_at"], null);
	        this.payload_schema = source["payload_schema"];
	        this.payload = source["payload"];
	        this.attachments = this.convertValues(source["attachments"], AttachmentRef);
	        this.checksum = source["checksum"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	export class TaskRecord {
	    id: string;
	    session_id: string;
	    title: string;
	    strategy: string;
	    state: string;
	    agent_id?: string;
	    approval_granted?: boolean;
	    dependencies?: string[];
	    artifact_id?: string;
	    // Go type: time
	    created_at: any;
	    // Go type: time
	    updated_at: any;
	
	    static createFrom(source: any = {}) {
	        return new TaskRecord(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.session_id = source["session_id"];
	        this.title = source["title"];
	        this.strategy = source["strategy"];
	        this.state = source["state"];
	        this.agent_id = source["agent_id"];
	        this.approval_granted = source["approval_granted"];
	        this.dependencies = source["dependencies"];
	        this.artifact_id = source["artifact_id"];
	        this.created_at = this.convertValues(source["created_at"], null);
	        this.updated_at = this.convertValues(source["updated_at"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace events {
	
	export class Record {
	    id: string;
	    session_id?: string;
	    task_id?: string;
	    type: string;
	    actor_type: string;
	    // Go type: time
	    occurred_at: any;
	    reason_code?: string;
	    payload?: { [key: string]: any };
	
	    static createFrom(source: any = {}) {
	        return new Record(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.session_id = source["session_id"];
	        this.task_id = source["task_id"];
	        this.type = source["type"];
	        this.actor_type = source["actor_type"];
	        this.occurred_at = this.convertValues(source["occurred_at"], null);
	        this.reason_code = source["reason_code"];
	        this.payload = source["payload"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace history {
	
	export class SessionRecord {
	    id: string;
	    task_id: string;
	    prompt: string;
	    starter: string;
	    delegates?: string[];
	    working_dir: string;
	    status: string;
	    artifact_ids?: string[];
	    final_artifact_id?: string;
	    // Go type: time
	    created_at: any;
	    // Go type: time
	    updated_at: any;
	
	    static createFrom(source: any = {}) {
	        return new SessionRecord(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.task_id = source["task_id"];
	        this.prompt = source["prompt"];
	        this.starter = source["starter"];
	        this.delegates = source["delegates"];
	        this.working_dir = source["working_dir"];
	        this.status = source["status"];
	        this.artifact_ids = source["artifact_ids"];
	        this.final_artifact_id = source["final_artifact_id"];
	        this.created_at = this.convertValues(source["created_at"], null);
	        this.updated_at = this.convertValues(source["updated_at"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace main {
	
	export class BootstrapResponse {
	    working_dir: string;
	    daemon_available: boolean;
	    embedded_daemon: boolean;
	    last_daemon_error?: string;
	    agent_config_path: string;
	    agents: domain.AgentProfile[];
	    status: api.StatusResponse;
	    queue: queue.Request[];
	    acp: api.ACPStatusResponse;
	
	    static createFrom(source: any = {}) {
	        return new BootstrapResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.working_dir = source["working_dir"];
	        this.daemon_available = source["daemon_available"];
	        this.embedded_daemon = source["embedded_daemon"];
	        this.last_daemon_error = source["last_daemon_error"];
	        this.agent_config_path = source["agent_config_path"];
	        this.agents = this.convertValues(source["agents"], domain.AgentProfile);
	        this.status = this.convertValues(source["status"], api.StatusResponse);
	        this.queue = this.convertValues(source["queue"], queue.Request);
	        this.acp = this.convertValues(source["acp"], api.ACPStatusResponse);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class PlanPreviewRequest {
	    session_id: string;
	    task_id: string;
	    artifact_id: string;
	    policy_override: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PlanPreviewRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.session_id = source["session_id"];
	        this.task_id = source["task_id"];
	        this.artifact_id = source["artifact_id"];
	        this.policy_override = source["policy_override"];
	    }
	}
	export class RunSubmitRequest {
	    prompt: string;
	    mode: string;
	    starter_agent: string;
	    delegates: string[];
	    working_dir: string;
	    continuous: boolean;
	    max_rounds: number;
	    policy_override: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RunSubmitRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.prompt = source["prompt"];
	        this.mode = source["mode"];
	        this.starter_agent = source["starter_agent"];
	        this.delegates = source["delegates"];
	        this.working_dir = source["working_dir"];
	        this.continuous = source["continuous"];
	        this.max_rounds = source["max_rounds"];
	        this.policy_override = source["policy_override"];
	    }
	}
	export class SnapshotResponse {
	    working_dir: string;
	    daemon_available: boolean;
	    embedded_daemon: boolean;
	    last_daemon_error?: string;
	    status: api.StatusResponse;
	    queue: queue.Request[];
	    acp: api.ACPStatusResponse;
	
	    static createFrom(source: any = {}) {
	        return new SnapshotResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.working_dir = source["working_dir"];
	        this.daemon_available = source["daemon_available"];
	        this.embedded_daemon = source["embedded_daemon"];
	        this.last_daemon_error = source["last_daemon_error"];
	        this.status = this.convertValues(source["status"], api.StatusResponse);
	        this.queue = this.convertValues(source["queue"], queue.Request);
	        this.acp = this.convertValues(source["acp"], api.ACPStatusResponse);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace plans {
	
	export class ResolutionStep {
	    kind: string;
	    title: string;
	    detail?: string;
	    command?: string;
	    path?: string;
	
	    static createFrom(source: any = {}) {
	        return new ResolutionStep(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.title = source["title"];
	        this.detail = source["detail"];
	        this.command = source["command"];
	        this.path = source["path"];
	    }
	}
	export class ApplyResult {
	    artifact_id: string;
	    session_id: string;
	    task_id: string;
	    workspace: workspace.Prepared;
	    preview: workspace.MergePreview;
	    changed_paths: string[];
	    patch_bytes: number;
	    dry_run: boolean;
	    applied: boolean;
	    rolled_back: boolean;
	    rollback_hint?: string;
	    required_checks?: string[];
	    violations?: string[];
	    conflict: boolean;
	    conflict_kind?: string;
	    conflict_detail?: string;
	    conflict_summary?: string;
	    conflict_paths?: string[];
	    conflict_context?: workspace.ConflictSnippet[];
	    remediation_hint?: string;
	    resolution_options?: string[];
	    resolution_steps?: ResolutionStep[];
	
	    static createFrom(source: any = {}) {
	        return new ApplyResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.artifact_id = source["artifact_id"];
	        this.session_id = source["session_id"];
	        this.task_id = source["task_id"];
	        this.workspace = this.convertValues(source["workspace"], workspace.Prepared);
	        this.preview = this.convertValues(source["preview"], workspace.MergePreview);
	        this.changed_paths = source["changed_paths"];
	        this.patch_bytes = source["patch_bytes"];
	        this.dry_run = source["dry_run"];
	        this.applied = source["applied"];
	        this.rolled_back = source["rolled_back"];
	        this.rollback_hint = source["rollback_hint"];
	        this.required_checks = source["required_checks"];
	        this.violations = source["violations"];
	        this.conflict = source["conflict"];
	        this.conflict_kind = source["conflict_kind"];
	        this.conflict_detail = source["conflict_detail"];
	        this.conflict_summary = source["conflict_summary"];
	        this.conflict_paths = source["conflict_paths"];
	        this.conflict_context = this.convertValues(source["conflict_context"], workspace.ConflictSnippet);
	        this.remediation_hint = source["remediation_hint"];
	        this.resolution_options = source["resolution_options"];
	        this.resolution_steps = this.convertValues(source["resolution_steps"], ResolutionStep);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace queue {
	
	export class GraphNode {
	    id: string;
	    title: string;
	    agent: string;
	    strategy: string;
	    dependencies?: string[];
	    senators?: string[];
	    quorum?: number;
	    arbitration_mode?: string;
	    arbitrator?: string;
	
	    static createFrom(source: any = {}) {
	        return new GraphNode(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.agent = source["agent"];
	        this.strategy = source["strategy"];
	        this.dependencies = source["dependencies"];
	        this.senators = source["senators"];
	        this.quorum = source["quorum"];
	        this.arbitration_mode = source["arbitration_mode"];
	        this.arbitrator = source["arbitrator"];
	    }
	}
	export class GraphSpec {
	    prompt: string;
	    nodes: GraphNode[];
	
	    static createFrom(source: any = {}) {
	        return new GraphSpec(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.prompt = source["prompt"];
	        this.nodes = this.convertValues(source["nodes"], GraphNode);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Request {
	    id: string;
	    graph_file?: string;
	    graph?: GraphSpec;
	    prompt: string;
	    mode?: string;
	    starter_agent: string;
	    delegates?: string[];
	    working_dir: string;
	    continuous?: boolean;
	    max_rounds?: number;
	    session_id?: string;
	    task_id?: string;
	    artifact_ids?: string[];
	    policy_override?: boolean;
	    policy_override_actor?: string;
	    status: string;
	    // Go type: time
	    created_at: any;
	    // Go type: time
	    updated_at: any;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new Request(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.graph_file = source["graph_file"];
	        this.graph = this.convertValues(source["graph"], GraphSpec);
	        this.prompt = source["prompt"];
	        this.mode = source["mode"];
	        this.starter_agent = source["starter_agent"];
	        this.delegates = source["delegates"];
	        this.working_dir = source["working_dir"];
	        this.continuous = source["continuous"];
	        this.max_rounds = source["max_rounds"];
	        this.session_id = source["session_id"];
	        this.task_id = source["task_id"];
	        this.artifact_ids = source["artifact_ids"];
	        this.policy_override = source["policy_override"];
	        this.policy_override_actor = source["policy_override_actor"];
	        this.status = source["status"];
	        this.created_at = this.convertValues(source["created_at"], null);
	        this.updated_at = this.convertValues(source["updated_at"], null);
	        this.error = source["error"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace scheduler {
	
	export class WorkspaceRef {
	    task_id: string;
	    effective_dir: string;
	    provider: string;
	    effective_mode: string;
	
	    static createFrom(source: any = {}) {
	        return new WorkspaceRef(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.task_id = source["task_id"];
	        this.effective_dir = source["effective_dir"];
	        this.provider = source["provider"];
	        this.effective_mode = source["effective_mode"];
	    }
	}
	export class LeaseRecord {
	    session_id: string;
	    owner_id: string;
	    status: string;
	    ready_task_ids?: string[];
	    workspace_refs?: WorkspaceRef[];
	    pending_approval_task_ids?: string[];
	    completed_task_ids?: string[];
	    // Go type: time
	    created_at: any;
	    // Go type: time
	    updated_at: any;
	
	    static createFrom(source: any = {}) {
	        return new LeaseRecord(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.session_id = source["session_id"];
	        this.owner_id = source["owner_id"];
	        this.status = source["status"];
	        this.ready_task_ids = source["ready_task_ids"];
	        this.workspace_refs = this.convertValues(source["workspace_refs"], WorkspaceRef);
	        this.pending_approval_task_ids = source["pending_approval_task_ids"];
	        this.completed_task_ids = source["completed_task_ids"];
	        this.created_at = this.convertValues(source["created_at"], null);
	        this.updated_at = this.convertValues(source["updated_at"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace workspace {
	
	export class ConflictSnippet {
	    path: string;
	    snippet: string;
	
	    static createFrom(source: any = {}) {
	        return new ConflictSnippet(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.snippet = source["snippet"];
	    }
	}
	export class MergePreview {
	    can_apply: boolean;
	    conflict: boolean;
	    conflict_detail?: string;
	    conflict_paths?: string[];
	    conflict_context?: ConflictSnippet[];
	    changed_paths?: string[];
	    patch_bytes: number;
	
	    static createFrom(source: any = {}) {
	        return new MergePreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.can_apply = source["can_apply"];
	        this.conflict = source["conflict"];
	        this.conflict_detail = source["conflict_detail"];
	        this.conflict_paths = source["conflict_paths"];
	        this.conflict_context = this.convertValues(source["conflict_context"], ConflictSnippet);
	        this.changed_paths = source["changed_paths"];
	        this.patch_bytes = source["patch_bytes"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Prepared {
	    session_id: string;
	    task_id: string;
	    requested_mode: string;
	    effective_mode: string;
	    provider: string;
	    base_dir: string;
	    effective_dir: string;
	    fallback?: string;
	    // Go type: time
	    prepared_at: any;
	    status: string;
	    // Go type: time
	    released_at?: any;
	    // Go type: time
	    reclaimed_at?: any;
	    // Go type: time
	    merged_at?: any;
	    // Go type: time
	    rolled_back_at?: any;
	
	    static createFrom(source: any = {}) {
	        return new Prepared(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.session_id = source["session_id"];
	        this.task_id = source["task_id"];
	        this.requested_mode = source["requested_mode"];
	        this.effective_mode = source["effective_mode"];
	        this.provider = source["provider"];
	        this.base_dir = source["base_dir"];
	        this.effective_dir = source["effective_dir"];
	        this.fallback = source["fallback"];
	        this.prepared_at = this.convertValues(source["prepared_at"], null);
	        this.status = source["status"];
	        this.released_at = this.convertValues(source["released_at"], null);
	        this.reclaimed_at = this.convertValues(source["reclaimed_at"], null);
	        this.merged_at = this.convertValues(source["merged_at"], null);
	        this.rolled_back_at = this.convertValues(source["rolled_back_at"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}
