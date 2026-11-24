import type { Task } from "../api/types.ts";
import { relativeTime } from "./format.ts";

/** Copy for the task detail banner of a launch Stage has not confirmed; `name` is the harness's name. */
export interface LaunchNotice {
  title: string;
  /** The session the harness returned ("title · state"); null when none was seen. */
  session: string | null;
  /** Why the last check failed; null when none failed yet. */
  reason: string | null;
  /** Failed checks, the next retry and when the launch started; null when nothing failed yet. */
  meta: string | null;
  body: string;
}

export function launchNotice(t: Pick<Task, "launch" | "stateSince">, name: string, now: number = Date.now()): LaunchNotice {
  const launch = t.launch;
  const seen = launch?.session ?? null;
  const reason = launch === undefined || launch.reason === "" ? null : launch.reason;
  const meta = launch === undefined || launch.retryCount === 0 ? null : retryMeta(launch.retryCount, launch.retryAt, t.stateSince, now);
  if (seen === null) {
    return { title: "Launch unconfirmed", session: null, reason, meta, body: "Stage is checking the existing attempt. New work stays blocked until its outcome is known." };
  }
  return {
    title: `Launched in ${name}; Stage can't record it`,
    session: `${seen.title === "" ? seen.externalSessionId : seen.title} · ${seen.state.replaceAll("_", " ")}`,
    reason: reason === null ? null : `Recording failed: ${reason}`,
    meta,
    body: "New work on this task stays blocked until Stage records the session. Stage keeps retrying.",
  };
}

function retryMeta(failed: number, retryAt: string | null, since: string | null, now: number): string {
  const parts = [`${failed} failed ${failed === 1 ? "check" : "checks"}`];
  if (retryAt !== null) parts.push(new Date(retryAt).getTime() > now ? `next retry ${relativeTime(retryAt, now)}` : "retrying now");
  if (since !== null) parts.push(`started ${relativeTime(since, now)}`);
  return parts.join(" · ");
}
