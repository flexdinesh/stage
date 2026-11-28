// OpenCode connection: what the Settings card reads from the status and the
// pairing-link-or-address field, and its replace and reconnect copy. Rendering lives in Setup.tsx.
import { ApiError } from "../../api/client.ts";
import type { HarnessConnection } from "../../api/types.ts";
import { shortDate } from "../../lib/format.ts";
import type { OpenCodeSettings } from "./api.ts";
import type { OpenCodeStatus } from "./types.ts";

const count = (n: number, noun: string) => `${n} ${noun}${n === 1 ? "" : "s"}`;

/** What the pairing-link-or-address field holds: a link from `opencode pair`, a plain service address, or neither. */
export type OpenCodeTarget = { kind: "link"; service: string } | { kind: "address"; service: string } | null;

function httpUrl(text: string): URL | null {
  if (!URL.canParse(text)) return null;
  const url = new URL(text);
  return url.protocol === "http:" || url.protocol === "https:" ? url : null;
}

/** A pairing link is an http(s) URL whose path is `/auth/connect/<code>`; any other http(s) URL is an address. */
export function parseTarget(text: string): OpenCodeTarget {
  const url = httpUrl(text.trim());
  if (url === null || url.host === "") return null;
  return /^\/auth\/connect\/[^/]+$/.test(url.pathname) ? { kind: "link", service: url.host } : { kind: "address", service: url.host };
}

/** Mono read-back under the field before submit. */
export function targetReadBack(target: OpenCodeTarget): string | null {
  if (target === null) return null;
  return target.kind === "link" ? `pairing link · service ${target.service} · single use, 5 min` : `address · ${target.service}`;
}

/** Saving an address failed because OpenCode demands a password: the field reveals one. */
export const isAuthRequired = (e: unknown): boolean => e instanceof ApiError && e.code === "opencode_auth_required";

/** `host:port` of a service URL, or the URL itself when it does not parse. */
export function serviceHost(endpoint: string): string {
  return httpUrl(endpoint)?.host || endpoint;
}

const pairingExpired = (s: OpenCodeStatus, now: number) =>
  s.auth === "paired" && s.expiresAt !== undefined && Date.parse(s.expiresAt) <= now;

/** Header chip: sign-in lost waits on the user (`need`); unreachable is `bad`. */
export function openCodeChip(s: OpenCodeStatus, now = Date.now()): { label: string; tone: "ok" | "idle" | "bad" | "need" } {
  switch (s.state) {
    case "connected":
      return { label: "connected", tone: "ok" };
    case "unconfigured":
      return { label: "not connected", tone: "idle" };
    case "unavailable":
      return { label: "unavailable", tone: "bad" };
    case "rejected":
      return { label: pairingExpired(s, now) ? "expired" : "rejected", tone: "need" };
  }
}

const versionText = (v: string) => (v === "" ? "" : v.startsWith("v") ? v : `v${v}`);

function withLocalVersion(s: OpenCodeStatus, text: string): string {
  const version = s.local.url !== "" && s.local.url === s.endpoint ? versionText(s.local.version) : "";
  return version === "" ? text : `${text} · ${version}`;
}

/** Service row: the endpoint, with the local service's version when it is the one connected. */
export const serviceText = (s: OpenCodeStatus): string => withLocalVersion(s, s.endpoint);

/** Harness status card Service row: `host:port`, with the local version when it is the one connected. */
export const serviceHostText = (s: OpenCodeStatus): string => withLocalVersion(s, serviceHost(s.endpoint));

/** Sign-in row. */
export function signInText(s: OpenCodeStatus, now = Date.now()): string {
  switch (s.auth) {
    case "local":
      return "local service";
    case "paired":
      return s.expiresAt === undefined ? "paired · renews automatically" : `paired · renews automatically, expires ${shortDate(s.expiresAt, now)}`;
    case "basic":
      return "password";
    case "none":
      return "none";
  }
}

/** Line shown above the pairing field when no local service can be connected (none when the server did not look). */
export function localNote(s: OpenCodeStatus): string | null {
  return s.local.available || s.local.reason === "" ? null : "No OpenCode service on this machine.";
}

export type AdvancedDraft = { endpoint: string; browserEndpoint: string; webClient: string };

const urlFields: readonly (keyof AdvancedDraft)[] = ["endpoint", "browserEndpoint", "webClient"];

/**
 * Advanced drawer save body: only the URL fields that changed (trimmed),
 * plus credentials when replaced, so an untouched
 * service URL keeps the server's sign-in mode.
 */
export function advancedChanges(s: OpenCodeStatus, draft: AdvancedDraft, credentials: { username: string; password: string } | null): OpenCodeSettings {
  const body: OpenCodeSettings = {};
  for (const key of urlFields) {
    const value = draft[key].trim();
    if (value !== s[key]) body[key] = value;
  }
  if (credentials !== null) {
    body.username = credentials.username.trim();
    body.password = credentials.password;
  }
  return body;
}

/** Saving OpenCode settings hit a different service (instance_changed) or a replaced one returning (instance_returned): the user confirms before it replaces the current one. */
export const isInstanceChanged = (err: unknown) =>
  err instanceof ApiError && err.status === 409 && (err.code === "instance_changed" || err.code === "instance_returned");

/** The address serves a replaced OpenCode service: confirming revives its connection. */
export const isInstanceReturned = (err: unknown) => err instanceof ApiError && err.status === 409 && err.code === "instance_returned";

/** Confirm before reconnecting a replaced OpenCode service (save answered 409 instance_returned). */
export function returnedServicePrompt(address: string, old: Pick<HarnessConnection, "label"> | undefined): string {
  const who = old === undefined || old.label === "" ? "the current service" : old.label;
  return `${address} is the OpenCode service Stage used before. Connect it again? Its tasks and project bindings come back, and ${who} becomes read-only.`;
}

/** Confirm before connecting a different OpenCode service (save answered 409 instance_changed). */
export function newServicePrompt(address: string, old: Pick<HarnessConnection, "label" | "tasks"> | undefined): string {
  const who = old === undefined || old.label === "" ? "The current service" : old.label;
  const tasks = old === undefined ? "its tasks keep" : `its ${count(old.tasks, "task")} ${old.tasks === 1 ? "keeps its" : "keep their"}`;
  return `${address} is a different OpenCode service. Connect it as a new service? ${who} becomes read-only: ${tasks} last state and its project bindings need rebinding.`;
}

/**
 * The connected card's notes on answering and messaging OpenCode sessions from Stage, in OpenCode's words, one per
 * operation the connection offers (`respond`, `send`); none while neither is offered.
 */
export function interactionNotes(offered: { respond: boolean; send: boolean }): string[] {
  const notes: string[] = [];
  if (offered.respond) {
    notes.push("Permissions and questions of OpenCode sessions can be answered on the task, or in OpenCode. Forms with conditional or external fields are answered in OpenCode.");
  }
  if (offered.send) notes.push("A follow-up sent while OpenCode works waits for its run to end.");
  return notes;
}
