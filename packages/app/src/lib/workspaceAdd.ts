// Adding a harness workspace from Stage (ADR 0012): which connections and sources can add one, the request a person's
// input becomes, its retry key, and how a failure reads. Everything comes from the harness's declarations
// (`workspaceAdd` and `workspaceSources`); nothing here names a harness.
import { ApiError } from "../api/client.ts";
import type { AddedWorkspace, AddWorkspaceRequest, Capabilities, HarnessConnection, SetupField, WorkspaceSource } from "../api/types.ts";
import { actionBody, type SetupValues } from "./connectionSetup.ts";

/** One connection's capabilities read, as a query reports it. */
export interface CapabilityRead {
  data: Capabilities | undefined;
  error: unknown;
}

/** A connection in the Connection picker: whether it can add a workspace now, and the harness's reason when not. */
export interface AddConnection {
  connection: HarnessConnection;
  available: boolean;
  /** Why it cannot add (the harness's own words when it gave them); null while available. */
  reason: string | null;
  /** Still reading its capabilities. */
  checking: boolean;
  sources: readonly WorkspaceSource[];
}

const NO_SOURCES: readonly WorkspaceSource[] = [];

export function addConnection(connection: HarnessConnection, read: CapabilityRead): AddConnection {
  const off = (reason: string, checking = false): AddConnection => ({ connection, available: false, reason, checking, sources: NO_SOURCES });
  if (!connection.configured) return off("Not connected.");
  if (read.data === undefined) {
    if (read.error == null) return off("Checking…", true);
    return off(read.error instanceof Error && read.error.message !== "" ? read.error.message : "Couldn’t read what this connection supports.");
  }
  const op = read.data.operations.workspaceAdd;
  if (op?.available !== true) return off(op?.reason || "This harness can’t add workspaces from Stage.");
  const sources = read.data.workspaceSources ?? NO_SOURCES;
  if (!sources.some((source) => source.availability.available)) {
    return off(sources[0]?.availability.reason || "This harness declares no way to add a workspace.");
  }
  return { connection, available: true, reason: null, checking: false, sources };
}

/** How the Source choice renders: nothing for one source, joined buttons for two to four, a dropdown for more. */
export type SourceControl = "hidden" | "segmented" | "select";

export function sourceControl(sources: readonly WorkspaceSource[]): SourceControl {
  return sources.length <= 1 ? "hidden" : sources.length <= 4 ? "segmented" : "select";
}

/** The chosen source when it is still offered, else the first available one. */
export function pickSource(sources: readonly WorkspaceSource[], chosen: string | null): WorkspaceSource | undefined {
  const usable = sources.filter((source) => source.availability.available);
  return usable.find((source) => source.id === chosen) ?? usable[0];
}

const NO_FIELDS: readonly SetupField[] = [];

export const sourceFields = (source: WorkspaceSource | undefined): readonly SetupField[] => source?.input ?? NO_FIELDS;

/** A required field still empty (a flag counts as answered either way). */
export function missingInput(fields: readonly SetupField[], values: SetupValues): boolean {
  return fields.some((field) => field.required === true && field.kind !== "bool" && String(values[field.id] ?? "").trim() === "");
}

/** Ready to send: an available source, every required field filled, and its confirmation agreed to when it has one. */
export function canSubmit(source: WorkspaceSource | undefined, values: SetupValues, confirmed: boolean): boolean {
  if (source === undefined || !source.availability.available) return false;
  if (missingInput(sourceFields(source), values)) return false;
  return source.confirm === undefined || source.confirm === "" || confirmed;
}

/** The POST body: the source, its entered input, the retry key, the project to join and the confirmation. */
export function addRequest(source: WorkspaceSource, values: SetupValues, opts: {
  requestId: string;
  projectId?: number | undefined;
  confirmed: boolean;
}): AddWorkspaceRequest {
  const needsConfirm = source.confirm !== undefined && source.confirm !== "";
  return {
    sourceId: source.id,
    input: actionBody([...sourceFields(source)], values),
    requestId: opts.requestId,
    ...(opts.projectId === undefined ? {} : { projectId: opts.projectId }),
    ...(needsConfirm && opts.confirmed ? { confirmed: true } : {}),
  };
}

/** What makes two sends the same request: the connection and the body without its key. */
export function requestSignature(connectionId: number, body: Omit<AddWorkspaceRequest, "requestId">): string {
  const input = Object.keys(body.input).toSorted().map((key) => [key, body.input[key]]);
  return JSON.stringify([connectionId, body.sourceId, input, body.projectId ?? null, body.confirmed === true]);
}

export interface RequestKey {
  signature: string;
  id: string;
}

/** Resending the same request keeps its key, so an uncertain outcome is retried safely; any change mints a new one. */
export function nextRequestKey(previous: RequestKey | null, signature: string, mint: () => string): RequestKey {
  return previous !== null && previous.signature === signature ? previous : { signature, id: mint() };
}

/** A random retry key. `getRandomValues` (not `randomUUID`) also works on plain-http LAN origins. */
export function mintRequestId(fill: (bytes: Uint8Array<ArrayBuffer>) => void = (bytes) => { crypto.getRandomValues(bytes); }, prefix = "ws-add"): string {
  const bytes = new Uint8Array(16);
  fill(bytes);
  return `${prefix}-${Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("")}`;
}

/**
 * How a failed add reads. `uncertain`: the harness did not confirm (504), so Check again resends the same request.
 * `membership`: the workspace was added but not moved into the project (409), so Try again is safe. `error`: the
 * harness or Stage refused it; the fields stay for a fix.
 */
export type AddFailure = { kind: "uncertain" | "membership" | "error"; message: string };

export function addFailure(error: unknown): AddFailure | null {
  if (error == null) return null;
  if (error instanceof ApiError) {
    if (error.status === 504 || error.code === "outcome_uncertain") return { kind: "uncertain", message: error.message };
    if (error.code === "membership_failed") return { kind: "membership", message: error.message };
    return { kind: "error", message: error.message };
  }
  return { kind: "error", message: error instanceof Error && error.message !== "" ? error.message : "Couldn’t add the workspace." };
}

/** The task form's new selection: the destination the harness now lists. */
export function addedChoice(result: AddedWorkspace): { id: string; label: string; detail: string } {
  const { workspace } = result;
  const detail = workspace.path || workspace.workspaceId;
  return { id: workspace.workspaceId, label: workspace.title || detail, detail };
}
