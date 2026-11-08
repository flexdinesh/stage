import type { Health } from "./types.ts";

/** Non-2xx API response; `message` is the server's `{ error }` text. */
export class ApiError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

type QueryValue = string | number | boolean | readonly (string | number)[] | undefined;

/** Builds `?a=1&b=x,y`, skipping undefined and empty values. Arrays join with commas. */
export function toQuery(params: Record<string, QueryValue>): string {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined) continue;
    const text = Array.isArray(value) ? value.join(",") : String(value);
    if (text !== "") search.set(key, text);
  }
  const query = search.toString();
  return query === "" ? "" : `?${query}`;
}

function errorMessage(body: unknown, fallback: string): string {
  if (typeof body === "object" && body !== null && "error" in body && typeof body.error === "string") {
    return body.error;
  }
  return fallback;
}

type RequestOptions = { method?: string; body?: unknown };

async function request(path: string, init: RequestOptions): Promise<Response> {
  const headers: Record<string, string> = { Accept: "application/json" };
  const req: RequestInit = { method: init.method ?? "GET", headers };
  if (init.body !== undefined) {
    headers["Content-Type"] = "application/json";
    req.body = JSON.stringify(init.body);
  }
  const res = await fetch(`/api${path}`, req);
  if (!res.ok) {
    const body: unknown = await res.json().catch(() => null);
    throw new ApiError(res.status, errorMessage(body, `${res.status} ${res.statusText}`));
  }
  return res;
}

/** Fetches `/api{path}` and returns the JSON body typed as `T` (trusted contract: docs/api.md). */
export async function apiFetch<T>(path: string, init: RequestOptions = {}): Promise<T> {
  const res = await request(path, init);
  return res.json();
}

/** For endpoints that return 204 No Content. */
export async function apiSend(path: string, init: RequestOptions = {}): Promise<void> {
  await request(path, init);
}

export const getHealth = () => apiFetch<Health>("/health");
