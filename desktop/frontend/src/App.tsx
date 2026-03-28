import { FormEvent, useEffect, useState } from 'react';
import './App.css';
import {
  bootstrapApp,
  cancelJob,
  listPlans,
  pickWorkingDir,
  previewPlan,
  rejectPlan,
  resultShow,
  setWorkingDir,
  snapshotApp,
  submitRun,
  inspectJob,
  approvePlan,
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
];

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

function App() {
  const [boot, setBoot] = useState<BootstrapResponse | null>(null);
  const [snapshot, setSnapshot] = useState<SnapshotResponse | null>(null);
  const [agents, setAgents] = useState<AgentProfile[]>([]);
  const [selectedJobID, setSelectedJobID] = useState('');
  const [inspect, setInspect] = useState<QueueInspectResponse | null>(null);
  const [result, setResult] = useState<ResultShowResponse | null>(null);
  const [plans, setPlans] = useState<PlanInboxEntry[]>([]);
  const [planPreview, setPlanPreview] = useState<PlanApplyResponse | null>(null);
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
        setSnapshot({
          working_dir: data.working_dir,
          daemon_available: data.daemon_available,
          embedded_daemon: data.embedded_daemon,
          last_daemon_error: data.last_daemon_error,
          status: data.status,
          queue: data.queue,
          acp: data.acp,
        });
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
        if (sessionID) {
          const [nextResult, nextPlans] = await Promise.all([
            resultShow(sessionID),
            listPlans(sessionID),
          ]);
          if (!cancelled) {
            setResult(nextResult);
            setPlans(nextPlans);
          }
        } else {
          setResult(null);
          setPlans([]);
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
      setSnapshot({
        working_dir: refreshed.working_dir,
        daemon_available: refreshed.daemon_available,
        embedded_daemon: refreshed.embedded_daemon,
        last_daemon_error: refreshed.last_daemon_error,
        status: refreshed.status,
        queue: refreshed.queue,
        acp: refreshed.acp,
      });
      setAgents(refreshed.agents);
      setRunForm((current) => ({ ...current, working_dir: refreshed.working_dir }));
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
      setRunForm((current) => ({ ...current, prompt: '' }));
      setDelegatesText('');
      const next = await snapshotApp();
      setSnapshot(next);
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
      const next = await snapshotApp();
      setSnapshot(next);
      if (selectedJobID === jobID) {
        const detail = await inspectJob(jobID);
        setInspect(detail);
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

  return (
    <div className="shell">
      <header className="topbar">
        <div>
          <p className="eyebrow">Daemon-first desktop control</p>
          <h1>ROMA Desktop</h1>
        </div>
        <div className="topbar-actions">
          <div className={`badge ${boot?.embedded_daemon ? 'badge-embedded' : 'badge-external'}`}>
            {boot?.embedded_daemon ? 'Embedded romad' : 'External romad'}
          </div>
          <button className="ghost-button" onClick={handlePickDirectory} type="button">
            Choose Folder
          </button>
        </div>
      </header>

      <section className="workspace-strip">
        <div>
          <span className="workspace-label">Working directory</span>
          <strong>{snapshot?.working_dir || boot?.working_dir || 'Not set'}</strong>
        </div>
        <div className="workspace-meta">
          <span>{boot?.agent_config_path || 'No agent config'}</span>
          {snapshot?.acp?.enabled ? <span>ACP :{snapshot.acp.port}</span> : <span>ACP off</span>}
        </div>
      </section>

      <section className="status-grid">
        <StatusCard label="Queue" value={snapshot?.status.queue_items ?? 0} accent="sand" />
        <StatusCard label="Sessions" value={snapshot?.status.sessions ?? 0} accent="teal" />
        <StatusCard label="Pending Approval" value={snapshot?.status.pending_approval_tasks ?? 0} accent="rust" />
        <StatusCard label="Recoverable" value={snapshot?.status.recoverable_sessions ?? 0} accent="olive" />
      </section>

      {(message || error) && (
        <section className="banner-stack">
          {message ? <div className="banner banner-info">{message}</div> : null}
          {error ? <div className="banner banner-error">{error}</div> : null}
        </section>
      )}

      <main className="content-grid">
        <aside className="panel queue-panel">
          <div className="panel-header">
            <div>
              <p className="section-kicker">Activity</p>
              <h2>Queue</h2>
            </div>
            <span className="panel-count">{queueItems.length}</span>
          </div>
          <div className="job-list">
            {queueItems.length === 0 ? (
              <p className="empty-state">No queued jobs yet.</p>
            ) : (
              queueItems.map((item) => (
                <button
                  className={`job-card ${selectedJobID === item.id ? 'job-card-active' : ''}`}
                  key={item.id}
                  onClick={() => setSelectedJobID(item.id)}
                  type="button"
                >
                  <div className="job-card-header">
                    <strong>{item.id}</strong>
                    <span className={`pill pill-${item.status}`}>{item.status}</span>
                  </div>
                  <p>{trimText(item.prompt, 96)}</p>
                  <div className="job-card-meta">
                    <span>{item.starter_agent || 'no-agent'}</span>
                    <span>{item.mode || 'fanout'}</span>
                  </div>
                </button>
              ))
            )}
          </div>
        </aside>

        <section className="main-stack">
          <section className="panel run-panel">
            <div className="panel-header">
              <div>
                <p className="section-kicker">Compose</p>
                <h2>Run a task</h2>
              </div>
            </div>
            <form className="run-form" onSubmit={handleSubmit}>
              <textarea
                onChange={(event) => setRunForm((current) => ({ ...current, prompt: event.target.value }))}
                placeholder="Describe the engineering task you want ROMA to run."
                value={runForm.prompt}
              />
              <div className="form-row">
                <label>
                  Starter
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
                  Max rounds
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
              <div className="form-row">
                <label className="grow">
                  Delegates
                  <input
                    onChange={(event) => setDelegatesText(event.target.value)}
                    placeholder="codex, claude, gemini"
                    type="text"
                    value={delegatesText}
                  />
                </label>
                <label className="grow">
                  Working dir
                  <input
                    onChange={(event) =>
                      setRunForm((current) => ({ ...current, working_dir: event.target.value }))
                    }
                    type="text"
                    value={runForm.working_dir}
                  />
                </label>
              </div>
              <div className="form-actions">
                <label className="checkbox">
                  <input
                    checked={runForm.continuous}
                    onChange={(event) =>
                      setRunForm((current) => ({ ...current, continuous: event.target.checked }))
                    }
                    type="checkbox"
                  />
                  Continuous rounds
                </label>
                <button className="primary-button" disabled={busy || !runForm.prompt.trim()} type="submit">
                  {busy ? 'Submitting...' : 'Submit to romad'}
                </button>
              </div>
            </form>
          </section>

          <section className="detail-grid">
            <section className="panel detail-panel">
              <div className="panel-header">
                <div>
                  <p className="section-kicker">Selected job</p>
                  <h2>{inspect?.job.id || 'Choose a job'}</h2>
                </div>
                {inspect?.job.id ? (
                  <button className="ghost-button" onClick={() => handleCancel(inspect.job.id)} type="button">
                    Cancel job
                  </button>
                ) : null}
              </div>

              {inspect ? (
                <>
                  <div className="detail-hero">
                    <div>
                      <span className={`pill pill-${inspect.job.status}`}>{inspect.job.status}</span>
                      <h3>{trimText(inspect.job.prompt, 180)}</h3>
                    </div>
                    <div className="detail-meta">
                      <span>Agent: {inspect.job.starter_agent}</span>
                      <span>Session: {selectedSessionID || 'pending'}</span>
                      <span>Artifacts: {inspect.artifact_count || inspect.artifacts?.length || 0}</span>
                      <span>Events: {inspect.event_count || inspect.events?.length || 0}</span>
                    </div>
                  </div>

                  {live ? (
                    <div className="live-strip">
                      <div>
                        <span className="muted">Phase</span>
                        <strong>{live.phase || live.state || 'unknown'}</strong>
                      </div>
                      <div>
                        <span className="muted">Task</span>
                        <strong>{live.current_task_title || live.current_task_id || 'n/a'}</strong>
                      </div>
                      <div>
                        <span className="muted">Workspace</span>
                        <strong>{live.workspace_mode || 'n/a'}</strong>
                      </div>
                      <div>
                        <span className="muted">PID</span>
                        <strong>{live.process_pid || 'n/a'}</strong>
                      </div>
                    </div>
                  ) : null}

                  <div className="detail-columns">
                    <div>
                      <h4>Tasks</h4>
                      <div className="stack-list">
                        {inspect.tasks?.map((task) => (
                          <div className="stack-row" key={task.id}>
                            <div>
                              <strong>{task.title || task.id}</strong>
                              <p>{task.agent_id || 'system'}</p>
                            </div>
                            <span className={`pill pill-${task.state?.toLowerCase()}`}>{task.state}</span>
                          </div>
                        ))}
                      </div>
                    </div>
                    <div>
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
                    </div>
                  </div>

                  {live?.last_output_preview ? (
                    <div className="text-block">
                      <h4>Last output</h4>
                      <pre>{live.last_output_preview}</pre>
                    </div>
                  ) : null}
                </>
              ) : (
                <p className="empty-state">Select a queue item to inspect its live state.</p>
              )}
            </section>

            <section className="side-stack">
              <section className="panel result-panel">
                <div className="panel-header">
                  <div>
                    <p className="section-kicker">Outcome</p>
                    <h2>Result</h2>
                  </div>
                </div>
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
                  <p className="empty-state">No result loaded.</p>
                )}
              </section>

              <section className="panel plans-panel">
                <div className="panel-header">
                  <div>
                    <p className="section-kicker">Approval</p>
                    <h2>Plans inbox</h2>
                  </div>
                  <span className="panel-count">{plans.length}</span>
                </div>
                <div className="stack-list">
                  {plans.length ? (
                    plans.map((entry) => (
                      <div className="plan-card" key={entry.artifact_id}>
                        <div className="job-card-header">
                          <strong>{entry.task_id}</strong>
                          <span className={`pill pill-${entry.status}`}>{entry.status}</span>
                        </div>
                        <p>{entry.goal || entry.artifact_id}</p>
                        <div className="plan-actions">
                          <button className="ghost-button" onClick={() => handlePlanPreview(entry)} type="button">
                            Preview
                          </button>
                          <button className="ghost-button" onClick={() => handlePlanDecision('approve', entry.artifact_id)} type="button">
                            Approve
                          </button>
                          <button className="ghost-button" onClick={() => handlePlanDecision('reject', entry.artifact_id)} type="button">
                            Reject
                          </button>
                        </div>
                      </div>
                    ))
                  ) : (
                    <p className="empty-state">No pending execution plans.</p>
                  )}
                </div>

                {planPreview ? (
                  <div className="text-block">
                    <h4>Preview</h4>
                    <pre>{prettyJSON(planPreview)}</pre>
                  </div>
                ) : null}
              </section>
            </section>
          </section>
        </section>
      </main>
    </div>
  );
}

function StatusCard(props: { label: string; value: number; accent: string }) {
  return (
    <div className={`status-card status-card-${props.accent}`}>
      <span>{props.label}</span>
      <strong>{props.value}</strong>
    </div>
  );
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
