import { FormEvent, useEffect, useState } from 'react';
import './App.css';
import {
  approvePlan,
  bootstrapApp,
  cancelJob,
  inspectJob,
  listPlans,
  pickWorkingDir,
  previewPlan,
  rejectPlan,
  resultShow,
  setWorkingDir,
  snapshotApp,
  submitRun,
} from './api';
import type {
  AgentProfile,
  BootstrapResponse,
  PlanApplyResponse,
  PlanInboxEntry,
  QueueInspectResponse,
  QueueRequest,
  ResultShowResponse,
  RunSubmitRequest,
  SnapshotResponse,
} from './types';

const modeOptions = [
  { value: 'fanout', label: 'Fanout' },
  { value: 'caesar', label: 'Caesar' },
  { value: 'senate', label: 'Senate' },
] as const;

const emptyRunForm: RunSubmitRequest = {
  prompt: '',
  mode: 'fanout',
  starter_agent: '',
  delegates: [],
  working_dir: '',
  continuous: false,
  max_rounds: 3,
  policy_override: false,
};

type DetailTab = 'overview' | 'result' | 'plans';

function App() {
  const [boot, setBoot] = useState<BootstrapResponse | null>(null);
  const [snapshot, setSnapshot] = useState<SnapshotResponse | null>(null);
  const [agents, setAgents] = useState<AgentProfile[]>([]);
  const [selectedJobID, setSelectedJobID] = useState('');
  const [inspect, setInspect] = useState<QueueInspectResponse | null>(null);
  const [result, setResult] = useState<ResultShowResponse | null>(null);
  const [plans, setPlans] = useState<PlanInboxEntry[]>([]);
  const [planPreview, setPlanPreview] = useState<PlanApplyResponse | null>(null);
  const [detailTab, setDetailTab] = useState<DetailTab>('overview');
  const [delegatesText, setDelegatesText] = useState('');
  const [runForm, setRunForm] = useState<RunSubmitRequest>(emptyRunForm);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState('Starting desktop control plane...');
  const [error, setError] = useState('');

  useEffect(() => {
    let cancelled = false;
    bootstrapApp()
      .then((data) => {
        if (cancelled) {
          return;
        }
        setBoot(data);
        setSnapshot(toSnapshot(data));
        setAgents(data.agents);
        setRunForm((current) => ({
          ...current,
          starter_agent: current.starter_agent || firstAvailableAgent(data.agents),
          working_dir: data.working_dir,
        }));
        setMessage(data.embedded_daemon ? 'Embedded romad is running.' : 'Connected to existing romad.');
        setSelectedJobID(selectPreferredJob(data.queue));
      })
      .catch((err: Error) => {
        if (!cancelled) {
          setError(err.message || String(err));
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (!snapshot) {
      return;
    }
    const timer = window.setInterval(async () => {
      try {
        const next = await snapshotApp();
        setSnapshot(next);
        setMessage(next.embedded_daemon ? 'Embedded romad is running.' : 'Connected to existing romad.');
        setError('');
        setSelectedJobID((current) => current || selectPreferredJob(next.queue));
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err));
      }
    }, 2500);
    return () => window.clearInterval(timer);
  }, [snapshot]);

  useEffect(() => {
    if (!selectedJobID) {
      setInspect(null);
      setResult(null);
      setPlans([]);
      setPlanPreview(null);
      return;
    }
    let cancelled = false;
    const load = async () => {
      try {
        const jobInspect = await inspectJob(selectedJobID);
        if (cancelled) {
          return;
        }
        setInspect(jobInspect);
        setError('');
        const sessionID = jobInspect.job.session_id || jobInspect.session?.id || '';
        if (!sessionID) {
          setResult(null);
          setPlans([]);
          setPlanPreview(null);
          return;
        }
        const [nextResult, nextPlans] = await Promise.all([resultShow(sessionID), listPlans(sessionID)]);
        if (!cancelled) {
          setResult(nextResult);
          setPlans(nextPlans);
        }
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : String(err));
        }
      }
    };
    load();
    const timer = window.setInterval(load, 2500);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [selectedJobID]);

  async function handlePickDirectory() {
    try {
      const next = await pickWorkingDir();
      if (!next) {
        return;
      }
      const refreshed = await setWorkingDir(next);
      setBoot(refreshed);
      setSnapshot(toSnapshot(refreshed));
      setAgents(refreshed.agents);
      setRunForm((current) => ({
        ...current,
        starter_agent: current.starter_agent || firstAvailableAgent(refreshed.agents),
        working_dir: refreshed.working_dir,
      }));
      setError('');
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    try {
      const payload: RunSubmitRequest = {
        ...runForm,
        delegates: splitDelegates(delegatesText),
      };
      const response = await submitRun(payload);
      setMessage(`Submitted ${response.job_id}.`);
      setSelectedJobID(response.job_id);
      setDetailTab('overview');
      setPlanPreview(null);
      setRunForm((current) => ({ ...current, prompt: '' }));
      setDelegatesText('');
      setSnapshot(await snapshotApp());
      setError('');
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  async function handleCancel(jobID: string) {
    setBusy(true);
    try {
      await cancelJob(jobID);
      setMessage(`Cancelled ${jobID}.`);
      setSnapshot(await snapshotApp());
      if (selectedJobID === jobID) {
        setInspect(await inspectJob(jobID));
      }
      setError('');
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  async function handlePlanPreview(entry: PlanInboxEntry) {
    try {
      const preview = await previewPlan({
        session_id: entry.session_id,
        task_id: entry.task_id,
        artifact_id: entry.artifact_id,
        policy_override: false,
      });
      setPlanPreview(preview);
      setDetailTab('plans');
      setError('');
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  async function handlePlanDecision(kind: 'approve' | 'reject', artifactID: string) {
    setBusy(true);
    try {
      if (kind === 'approve') {
        await approvePlan(artifactID);
        setMessage(`Approved ${artifactID}.`);
      } else {
        await rejectPlan(artifactID);
        setMessage(`Rejected ${artifactID}.`);
      }
      if (inspect?.job.session_id) {
        setPlans(await listPlans(inspect.job.session_id));
      }
      setError('');
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  const queueItems = snapshot?.queue ?? [];
  const live = inspect?.live;
  const selectedSessionID = inspect?.job.session_id || inspect?.session?.id || '';
  const selectedPrompt = inspect?.job.prompt || '';
  const stats = snapshot?.status;

  return (
    <div className="shell">
      <header className="masthead">
        <div>
          <p className="eyebrow">Local orchestration desk</p>
          <h1>ROMA</h1>
          <p className="masthead-copy">Run work, watch the queue, inspect one session at a time.</p>
        </div>
        <div className="masthead-actions">
          <div className={`connection-badge ${boot?.embedded_daemon ? 'connection-badge-embedded' : ''}`}>
            {boot?.embedded_daemon ? 'embedded romad' : 'connected romad'}
          </div>
          <button className="secondary-button" onClick={handlePickDirectory} type="button">
            Change folder
          </button>
        </div>
      </header>

      <section className="summary-strip">
        <div className="summary-item summary-item-wide">
          <span>Working directory</span>
          <strong>{snapshot?.working_dir || boot?.working_dir || 'Not set'}</strong>
        </div>
        <div className="summary-item">
          <span>Queue</span>
          <strong>{stats?.queue_items ?? 0}</strong>
        </div>
        <div className="summary-item">
          <span>Approval</span>
          <strong>{stats?.pending_approval_tasks ?? 0}</strong>
        </div>
        <div className="summary-item">
          <span>Recoverable</span>
          <strong>{stats?.recoverable_sessions ?? 0}</strong>
        </div>
      </section>

      {(message || error) && (
        <section className="notice-stack">
          {message ? <div className="notice notice-info">{message}</div> : null}
          {error ? <div className="notice notice-error">{error}</div> : null}
        </section>
      )}

      <main className="workspace-grid">
        <aside className="sidebar">
          <section className="panel panel-quiet">
            <div className="panel-head">
              <div>
                <p className="section-kicker">Run</p>
                <h2>New task</h2>
              </div>
            </div>
            <form className="run-form" onSubmit={handleSubmit}>
              <textarea
                onChange={(event) => setRunForm((current) => ({ ...current, prompt: event.target.value }))}
                placeholder="What should ROMA work on?"
                value={runForm.prompt}
              />

              <label>
                Starter agent
                <select
                  onChange={(event) =>
                    setRunForm((current) => ({ ...current, starter_agent: event.target.value }))
                  }
                  value={runForm.starter_agent}
                >
                  {agents.length === 0 ? <option value="">No agents configured</option> : null}
                  {agents.map((agent) => (
                    <option key={agent.id} value={agent.id}>
                      {agent.display_name || agent.id} ({agent.availability})
                    </option>
                  ))}
                </select>
              </label>

              <div className="inline-fields">
                <label>
                  Mode
                  <select
                    onChange={(event) => setRunForm((current) => ({ ...current, mode: event.target.value }))}
                    value={runForm.mode}
                  >
                    {modeOptions.map((mode) => (
                      <option key={mode.value} value={mode.value}>
                        {mode.label}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  Rounds
                  <input
                    min={1}
                    onChange={(event) =>
                      setRunForm((current) => ({
                        ...current,
                        max_rounds: Number(event.target.value) || 1,
                      }))
                    }
                    type="number"
                    value={runForm.max_rounds}
                  />
                </label>
              </div>

              <label>
                Delegates
                <input
                  onChange={(event) => setDelegatesText(event.target.value)}
                  placeholder="claude, codex"
                  type="text"
                  value={delegatesText}
                />
              </label>

              <label>
                Working directory
                <input
                  onChange={(event) =>
                    setRunForm((current) => ({ ...current, working_dir: event.target.value }))
                  }
                  type="text"
                  value={runForm.working_dir}
                />
              </label>

              <div className="form-foot">
                <label className="checkbox">
                  <input
                    checked={runForm.continuous}
                    onChange={(event) =>
                      setRunForm((current) => ({ ...current, continuous: event.target.checked }))
                    }
                    type="checkbox"
                  />
                  Continuous
                </label>
                <button className="primary-button" disabled={busy || !runForm.prompt.trim()} type="submit">
                  {busy ? 'Submitting...' : 'Submit'}
                </button>
              </div>
            </form>
          </section>

          <section className="panel queue-panel">
            <div className="panel-head">
              <div>
                <p className="section-kicker">Queue</p>
                <h2>Recent runs</h2>
              </div>
              <span className="panel-count">{queueItems.length}</span>
            </div>
            <div className="job-list">
              {queueItems.length === 0 ? (
                <p className="empty-state">Nothing queued yet.</p>
              ) : (
                queueItems.map((item) => (
                  <button
                    className={`job-row ${selectedJobID === item.id ? 'job-row-active' : ''}`}
                    key={item.id}
                    onClick={() => {
                      setSelectedJobID(item.id);
                      setDetailTab('overview');
                    }}
                    type="button"
                  >
                    <div className="job-row-top">
                      <strong>{trimText(item.prompt, 48) || item.id}</strong>
                      <span className={`pill pill-${item.status}`}>{item.status}</span>
                    </div>
                    <div className="job-row-bottom">
                      <span>{item.starter_agent || 'no-agent'}</span>
                      <span>{item.mode || 'fanout'}</span>
                      <span>{item.id}</span>
                    </div>
                  </button>
                ))
              )}
            </div>
          </section>
        </aside>

        <section className="panel inspector">
          <div className="inspector-head">
            <div>
              <p className="section-kicker">Session</p>
              <h2>{selectedJobID ? 'Inspection' : 'No run selected'}</h2>
            </div>
            {inspect?.job.id ? (
              <button className="secondary-button" onClick={() => handleCancel(inspect.job.id)} type="button">
                Cancel
              </button>
            ) : null}
          </div>

          {inspect ? (
            <>
              <div className="hero-block">
                <div className="hero-top">
                  <span className={`pill pill-${inspect.job.status}`}>{inspect.job.status}</span>
                  <span className="hero-id">{inspect.job.id}</span>
                </div>
                <h3>{selectedPrompt || 'Untitled run'}</h3>
                <div className="hero-meta">
                  <span>agent {inspect.job.starter_agent}</span>
                  <span>session {selectedSessionID || 'pending'}</span>
                  <span>{inspect.artifact_count || inspect.artifacts?.length || 0} artifacts</span>
                  <span>{inspect.event_count || inspect.events?.length || 0} events</span>
                </div>
              </div>

              <div className="tabbar">
                <button
                  className={detailTab === 'overview' ? 'tab-active' : ''}
                  onClick={() => setDetailTab('overview')}
                  type="button"
                >
                  Overview
                </button>
                <button
                  className={detailTab === 'result' ? 'tab-active' : ''}
                  onClick={() => setDetailTab('result')}
                  type="button"
                >
                  Result
                </button>
                <button
                  className={detailTab === 'plans' ? 'tab-active' : ''}
                  onClick={() => setDetailTab('plans')}
                  type="button"
                >
                  Plans {plans.length > 0 ? `(${plans.length})` : ''}
                </button>
              </div>

              {detailTab === 'overview' ? (
                <section className="content-stack">
                  {live ? (
                    <div className="overview-grid">
                      <MetricCard label="Phase" value={live.phase || live.state || 'unknown'} />
                      <MetricCard label="Task" value={live.current_task_title || live.current_task_id || 'n/a'} />
                      <MetricCard label="Workspace" value={live.workspace_mode || 'n/a'} />
                      <MetricCard label="PID" value={String(live.process_pid || 'n/a')} />
                    </div>
                  ) : null}

                  <div className="split-grid">
                    <section>
                      <h4>Tasks</h4>
                      <div className="stack-list">
                        {inspect.tasks?.length ? (
                          inspect.tasks.map((task) => (
                            <div className="stack-row" key={task.id}>
                              <div>
                                <strong>{task.title || task.id}</strong>
                                <p>{task.agent_id || 'system'}</p>
                              </div>
                              <span className={`pill pill-${task.state?.toLowerCase()}`}>{task.state}</span>
                            </div>
                          ))
                        ) : (
                          <p className="empty-state">No task records yet.</p>
                        )}
                      </div>
                    </section>

                    <section>
                      <h4>Workspaces</h4>
                      <div className="stack-list">
                        {inspect.workspaces?.length ? (
                          inspect.workspaces.map((workspace) => (
                            <div className="stack-row" key={`${workspace.session_id}-${workspace.task_id}`}>
                              <div>
                                <strong>{workspace.task_id}</strong>
                                <p>{workspace.effective_dir || workspace.base_dir}</p>
                              </div>
                              <span className="pill pill-neutral">{workspace.status}</span>
                            </div>
                          ))
                        ) : (
                          <p className="empty-state">No workspace metadata yet.</p>
                        )}
                      </div>
                    </section>
                  </div>

                  {live?.last_output_preview ? (
                    <div className="text-block">
                      <h4>Last output</h4>
                      <pre>{live.last_output_preview}</pre>
                    </div>
                  ) : null}
                </section>
              ) : null}

              {detailTab === 'result' ? (
                <section className="content-stack">
                  {result ? (
                    result.pending ? (
                      <div className="text-block">
                        <h4>Pending</h4>
                        <p>{result.message || 'Result artifact is not available yet.'}</p>
                      </div>
                    ) : (
                      <div className="text-block">
                        <h4>{result.artifact.kind || 'artifact'}</h4>
                        <pre>{prettyJSON(result.artifact.payload)}</pre>
                      </div>
                    )
                  ) : (
                    <p className="empty-state">No result available for this run yet.</p>
                  )}
                </section>
              ) : null}

              {detailTab === 'plans' ? (
                <section className="content-stack">
                  {plans.length ? (
                    <div className="stack-list">
                      {plans.map((entry) => (
                        <div className="plan-row" key={entry.artifact_id}>
                          <div className="job-row-top">
                            <strong>{entry.task_id}</strong>
                            <span className={`pill pill-${entry.status}`}>{entry.status}</span>
                          </div>
                          <p>{entry.goal || entry.artifact_id}</p>
                          <div className="plan-actions">
                            <button className="secondary-button" onClick={() => handlePlanPreview(entry)} type="button">
                              Preview
                            </button>
                            <button className="secondary-button" onClick={() => handlePlanDecision('approve', entry.artifact_id)} type="button">
                              Approve
                            </button>
                            <button className="secondary-button" onClick={() => handlePlanDecision('reject', entry.artifact_id)} type="button">
                              Reject
                            </button>
                          </div>
                        </div>
                      ))}
                    </div>
                  ) : (
                    <p className="empty-state">No pending execution plans.</p>
                  )}

                  {planPreview ? (
                    <div className="text-block">
                      <h4>Preview</h4>
                      <pre>{prettyJSON(planPreview)}</pre>
                    </div>
                  ) : null}
                </section>
              ) : null}
            </>
          ) : (
            <div className="empty-view">
              <p>Select a run from the queue, or submit a new task on the left.</p>
            </div>
          )}
        </section>
      </main>
    </div>
  );
}

function MetricCard(props: { label: string; value: string }) {
  return (
    <div className="metric-card">
      <span>{props.label}</span>
      <strong>{props.value}</strong>
    </div>
  );
}

function toSnapshot(data: BootstrapResponse): SnapshotResponse {
  return {
    working_dir: data.working_dir,
    daemon_available: data.daemon_available,
    embedded_daemon: data.embedded_daemon,
    last_daemon_error: data.last_daemon_error,
    status: data.status,
    queue: data.queue,
    acp: data.acp,
  };
}

function firstAvailableAgent(agents: AgentProfile[]) {
  return agents.find((agent) => agent.availability === 'available')?.id || agents[0]?.id || '';
}

function selectPreferredJob(queue: QueueRequest[]) {
  return queue.find((item) => item.status === 'running')?.id || queue[0]?.id || '';
}

function splitDelegates(value: string) {
  return value
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean);
}

function trimText(value: string, max: number) {
  if (!value) {
    return '';
  }
  if (value.length <= max) {
    return value;
  }
  return `${value.slice(0, max - 1)}…`;
}

function prettyJSON(value: unknown) {
  return JSON.stringify(value ?? {}, null, 2);
}

export default App;
