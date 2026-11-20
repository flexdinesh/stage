// Harness connections: one per harness instance, at most one active per harness kind. Ids are not fixed: name and
// branch by `harness`, never by id. Names, closure copy and availability come from the server's harness descriptors
// (GET /harnesses), never from a list here. Retired connections keep their tasks and bindings as read-only history.
import { ApiError } from "../api/client.ts";
import type { ExecutionLinks } from "../platform/index.ts";
import type { ExternalProject, Harness, HarnessConnection, HarnessDescriptor, Project, ProjectBinding, ReplacedConnection, Task } from "../api/types.ts";

/** Lookups over GET /connections (every row, active first) and GET /harnesses (descriptors). */
export interface Harnesses {
  list: readonly HarnessConnection[];
  /** Active connections only: the ones new work, bindings and settings pick from. */
  activeList: readonly HarnessConnection[];
  /** Every harness kind the server knows, in its order. */
  descriptors: readonly HarnessDescriptor[];
  descriptor: (kind: Harness) => HarnessDescriptor | undefined;
  /** A kind's display name ("T3"); the kind itself while descriptors load. */
  kindName: (kind: Harness) => string;
  connection: (id: number) => HarnessConnection | undefined;
  /** The connection's harness name; "Harness" for an id not (yet) loaded. */
  name: (id: number) => string;
  harness: (id: number) => Harness | null;
  /** False for retired and unknown ids. */
  active: (id: number) => boolean;
  /** True only for a loaded connection that was replaced (unknown ids are not retired). */
  retired: (id: number) => boolean;
  label: (id: number) => string;
  /** The active connection of a harness, or null when none. */
  activeId: (harness: Harness) => number | null;
  activeConnection: (harness: Harness) => HarnessConnection | undefined;
}

export function harnessLookup(list: readonly HarnessConnection[], descriptors: readonly HarnessDescriptor[] = []): Harnesses {
  const byId = new Map(list.map((c) => [c.id, c]));
  const byKind = new Map(descriptors.map((d) => [d.kind, d]));
  const activeConnection = (h: Harness) => list.find((c) => c.harness === h && c.active);
  const kindName = (kind: Harness) => byKind.get(kind)?.name ?? kind;
  return {
    list,
    activeList: list.filter((c) => c.active),
    descriptors,
    descriptor: (kind) => byKind.get(kind),
    kindName,
    connection: (id) => byId.get(id),
    name: (id) => {
      const c = byId.get(id);
      return c === undefined ? "Harness" : kindName(c.harness);
    },
    harness: (id) => byId.get(id)?.harness ?? null,
    active: (id) => byId.get(id)?.active ?? false,
    retired: (id) => byId.get(id)?.active === false,
    label: (id) => byId.get(id)?.label ?? "",
    activeId: (h) => activeConnection(h)?.id ?? null,
    activeConnection,
  };
}

/** The task ran on a connection another instance replaced: read-only history. */
export const isRetired = (t: { connectionActive?: boolean }) => t.connectionActive === false;

const quoted = (label: string) => (label === "" ? "" : ` '${label}'`);
const count = (n: number, noun: string) => `${n} ${noun}${n === 1 ? "" : "s"}`;

/** Where "Open in <harness>" may go: none for a task on a replaced connection (Stage no longer follows it). */
export function sessionLinks(links: {
  t3DeepLink: string | null;
  t3WebUrl?: string | null;
  links?: { native?: string; web?: string; command?: string };
  connectionActive?: boolean;
}): ExecutionLinks | null {
  if (isRetired(links)) return null;
  return { native: links.links?.native ?? links.t3DeepLink, web: links.links?.web ?? links.t3WebUrl ?? null, command: links.links?.command ?? null };
}

/** The copyable resume command, offered when the host has no destination to open; none on a replaced connection. */
export const resumeCommand = (links: Parameters<typeof sessionLinks>[0]): string | null => sessionLinks(links)?.command ?? null;

/** Task detail Alert for a task on a replaced connection; `name` is its harness's name. */
export function retiredNotice(t: Pick<Task, "connectionLabel">, name: string): string {
  return `Ran on ${name}${quoted(t.connectionLabel)}, which was replaced. Its history and result stay here; Stage no longer follows it.`;
}

/** Hint on Start / Retry / Stop for a task on a replaced connection; `name` is its harness's name. */
export function retiredHint(name: string): string {
  return `Its ${name} connection was replaced. Duplicate it to run again.`;
}

/** Settings PREVIOUS list: retired, not forgotten connections of one harness. */
export const previousConnections = (list: readonly HarnessConnection[], h: Harness) =>
  list.filter((c) => c.harness === h && !c.active && c.forgottenAt === null);

export const usageText = (c: Pick<HarnessConnection, "tasks" | "bindings">) =>
  `${count(c.tasks, "task")} · ${count(c.bindings, "binding")}`;

export function forgetPrompt(c: Pick<HarnessConnection, "label">): string {
  return `Forget ${c.label || "this connection"}? Its tasks keep their history and stay read-only. Its project bindings stop asking for repair, and pairing this instance again starts a new connection.`;
}

/** After a pairing replaced the previous T3 connection. */
export function replacedNotice(r: ReplacedConnection): string {
  return `Paired. T3${quoted(r.label)} was replaced; its ${count(r.tasks, "task")} stay read-only. Bind projects to continue.`;
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

/** Projects list chip: current (active-connection) bindings only; replaced ones never ask for repair. */
export function bindingsSummary(bindings: readonly ProjectBinding[], kindName: (kind: Harness) => string): string {
  const current = bindings.filter((b) => b.connectionActive);
  if (current.length === 0) return "not bound";
  if (current.some((b) => b.status !== "available")) return "check workspace";
  return current.map((b) => kindName(b.harness)).join(" · ");
}

/** The project's bindings on replaced connections of a harness. */
export const retiredBindings = (project: Pick<Project, "bindings">, h: Harness): ProjectBinding[] =>
  project.bindings.filter((b) => b.harness === h && !b.connectionActive);

/** Workspace paths of the project's replaced bindings on a harness. */
export const replacedPaths = (project: Pick<Project, "bindings">, h: Harness): Set<string> =>
  new Set(retiredBindings(project, h).map((b) => b.workspace));

/**
 * One-click rebind: the active connection's workspace at exactly a replaced binding's path, while the project has
 * no binding on that connection and no other project owns the workspace. Null otherwise (the drawer stays the way).
 */
export function rebindCandidate(
  project: Pick<Project, "id" | "bindings">,
  connection: Pick<HarnessConnection, "id" | "harness">,
  external: readonly ExternalProject[],
): ExternalProject | null {
  if (project.bindings.some((b) => b.connectionId === connection.id)) return null;
  const paths = replacedPaths(project, connection.harness);
  if (paths.size === 0) return null;
  return external.find((e) => paths.has(e.workspace) && (e.projectId === null || e.projectId === project.id)) ?? null;
}
