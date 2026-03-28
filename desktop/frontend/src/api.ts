import {
  Bootstrap,
  PlanApprove,
  PlanPreview,
  PlanReject,
  PlansInbox,
  PickWorkingDir,
  QueueCancel,
  QueueInspect,
  ResultShow,
  SessionInspect,
  SetWorkingDir,
  Snapshot,
  SubmitRun,
} from '../wailsjs/go/main/App';
import type {
  BootstrapResponse,
  PlanApplyResponse,
  PlanInboxEntry,
  PlanPreviewRequest,
  QueueInspectResponse,
  QueueRequest,
  ResultShowResponse,
  RunSubmitRequest,
  SessionInspectResponse,
  SnapshotResponse,
  SubmitResponse,
} from './types';

export async function bootstrapApp() {
  return (await Bootstrap()) as BootstrapResponse;
}

export async function snapshotApp() {
  return (await Snapshot()) as SnapshotResponse;
}

export async function pickWorkingDir() {
  return (await PickWorkingDir()) as string;
}

export async function setWorkingDir(dir: string) {
  return (await SetWorkingDir(dir)) as BootstrapResponse;
}

export async function submitRun(payload: RunSubmitRequest) {
  return (await SubmitRun(payload)) as SubmitResponse;
}

export async function inspectJob(jobID: string) {
  return (await QueueInspect(jobID)) as QueueInspectResponse;
}

export async function inspectSession(sessionID: string) {
  return (await SessionInspect(sessionID)) as SessionInspectResponse;
}

export async function resultShow(sessionID: string) {
  return (await ResultShow(sessionID)) as ResultShowResponse;
}

export async function cancelJob(jobID: string) {
  return (await QueueCancel(jobID)) as QueueRequest;
}

export async function listPlans(sessionID: string) {
  return (await PlansInbox(sessionID)) as PlanInboxEntry[];
}

export async function approvePlan(artifactID: string) {
  await PlanApprove(artifactID);
}

export async function rejectPlan(artifactID: string) {
  await PlanReject(artifactID);
}

export async function previewPlan(payload: PlanPreviewRequest) {
  return (await PlanPreview(payload)) as PlanApplyResponse;
}
