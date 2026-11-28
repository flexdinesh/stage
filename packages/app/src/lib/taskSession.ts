export const REQUESTS_ANCHOR = "requests";

export const taskPath = (taskId: number) => `/tasks/${taskId}`;

export function taskSessionPath(taskId: number, attempt?: number | null): string {
  const path = `${taskPath(taskId)}/session`;
  return attempt == null ? path : `${path}/${attempt}`;
}

export const requestsPath = (taskId: number) => `${taskSessionPath(taskId)}#${REQUESTS_ANCHOR}`;

/** Attempt zero belongs to an adopted session. Undefined selects the current session. */
export function parseAttempt(value: string | undefined): number | undefined | null {
  if (value === undefined) return undefined;
  const attempt = Number(value);
  return /^\d+$/.test(value) && Number.isSafeInteger(attempt) ? attempt : null;
}
