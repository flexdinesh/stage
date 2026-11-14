import type { PullRequest, ThreadRun } from "../api/types.ts";
import type { ChipTone } from "../components/ui.tsx";
import { duration } from "./format.ts";

/** PR state tone: open is in flight (run), merged landed (ok); closed, draft and unknown states are idle. */
export function prTone(state: string): ChipTone {
  switch (state.toLowerCase()) {
    case "open":
      return "run";
    case "merged":
      return "ok";
    default:
      return "idle";
  }
}

/** Compact chip label `repo#num`: the repository name without its owner. */
export function prLabel(pr: PullRequest): string {
  const name = pr.repository.split("/").filter(Boolean).at(-1) ?? pr.repository;
  return `${name}#${pr.number}`;
}

/** Full PR name `owner/repo #num`. */
export const prName = (pr: PullRequest) => `${pr.repository} #${pr.number}`;

/** Row chips: the first `max` PRs, and how many collapse into `+n`. */
export function rowPulls(prs: PullRequest[], max = 2): { shown: PullRequest[]; more: number } {
  return { shown: prs.slice(0, max), more: Math.max(0, prs.length - max) };
}

/** Runs shown before "Show all". */
export const RUNS_COLLAPSED = 5;

/** The model a run executed; "requested → executed" when the provider ran another one. */
export function runModel(run: ThreadRun): string {
  const requested = run.model ?? run.providerInstanceId ?? "T3 default";
  const executed = run.executedModel;
  if (executed === null || executed === "" || executed === run.model) return requested;
  return run.model === null ? executed : `${run.model} → ${executed}`;
}

/** Run time: start to completion, or to `now` while the run has no completion. Null before it starts. */
export function runRan(run: ThreadRun, now: number = Date.now()): string | null {
  if (run.startedAt === null) return null;
  return duration(run.startedAt, run.completedAt ?? new Date(now).toISOString());
}

/** Queue wait (requested to started); null before start and under one second. */
export function runQueued(run: ThreadRun): string | null {
  if (run.startedAt === null) return null;
  const ms = new Date(run.startedAt).getTime() - new Date(run.requestedAt).getTime();
  if (Number.isNaN(ms) || ms < 1000) return null;
  return duration(run.requestedAt, run.startedAt);
}
