// Harness connections: one per harness instance, at most one active per harness kind. Ids are not fixed: name and
// branch by `harness`, never by id. Names, closure copy and availability come from the server's harness descriptors
// (GET /harnesses), never from a list here. Retired connections keep their tasks and bindings as read-only history.
import type { ExecutionLinks } from "../platform/index.ts";
import type { Harness, HarnessConnection, HarnessDescriptor, Project, Task } from "../api/types.ts";

/** Lookups over GET /connections (every row, active first) and GET /harnesses (descriptors). */
export interface Harnesses {
  list: readonly HarnessConnection[];
  /** Active connections only, connected or not. */
  activeList: readonly HarnessConnection[];
  /** Active, set-up connections: the ones new work, adopted sessions and project workspaces pick from. */
  connectedList: readonly HarnessConnection[];
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
  /** True for a loaded active connection that is not set up (disconnected, or never connected). */
  unconfigured: (id: number) => boolean;
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
    connectedList: list.filter((c) => c.active && c.configured),
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
    unconfigured: (id) => {
      const c = byId.get(id);
      return c !== undefined && c.active && !c.configured;
    },
    label: (id) => byId.get(id)?.label ?? "",
    activeId: (h) => activeConnection(h)?.id ?? null,
    activeConnection,
  };
}

/**
 * Disconnected: active but not set up, after being set up once (a label names its instance) or holding tasks or
 * bindings. One never set up holds nothing and is a planned placeholder, not disconnected.
 */
export const isDisconnected = (c: Pick<HarnessConnection, "active" | "configured" | "label" | "tasks" | "bindings">): boolean =>
  c.active && !c.configured && (c.label !== "" || c.tasks > 0 || c.bindings > 0);

/** The task ran on a connection another instance replaced: read-only history. */
export const isRetired = (t: { connectionActive?: boolean }) => t.connectionActive === false;

const quoted = (label: string) => (label === "" ? "" : ` '${label}'`);
const count = (n: number, noun: string) => `${n} ${noun}${n === 1 ? "" : "s"}`;

/** Where "Open in <harness>" may go: none for a task on a replaced connection (Stage no longer follows it). */
export function sessionLinks(links: { links?: { native?: string; web?: string; command?: string }; connectionActive?: boolean }): ExecutionLinks | null {
  if (isRetired(links)) return null;
  return { native: links.links?.native ?? null, web: links.links?.web ?? null, command: links.links?.command ?? null };
}

/** The copyable resume command, offered when the host has no destination to open; none on a replaced connection. */
export const resumeCommand = (links: Parameters<typeof sessionLinks>[0]): string | null => sessionLinks(links)?.command ?? null;

/**
 * What a session's rows, toasts and alerts offer: open its destination, else copy the command that resumes it. Null:
 * neither (the session is answered where it runs, e.g. a Claude Code terminal), or a replaced connection.
 */
export type SessionAction = { kind: "open"; href: string } | { kind: "resume"; command: string };

/** `href`: the host's destination for the session (useSessionDestination); the resume command only without one. */
export function sessionAction(href: string | null, links: Parameters<typeof sessionLinks>[0]): SessionAction | null {
  if (href !== null && href !== "") return { kind: "open", href };
  const command = resumeCommand(links);
  return command === null || command === "" ? null : { kind: "resume", command };
}

/** A task row's session action (sessionAction), primary while the task needs input. */
export type RowSessionAction = SessionAction & { primary: boolean };

/**
 * The task row's session action: the host's destination (`destination`) when it has one, else the session's resume
 * command; primary while the task needs input. Null when there is neither, or while `hidden` (the row is confirming a
 * completion or was just completed).
 */
export function rowSessionAction(
  destination: string | null,
  task: Parameters<typeof sessionLinks>[0],
  { needsInput, hidden }: { needsInput: boolean; hidden: boolean },
): RowSessionAction | null {
  const action = hidden ? null : sessionAction(destination, task);
  return action === null ? null : { ...action, primary: needsInput };
}

/** The last segment of a folder path ("/src/app/" is "app"); null for none. */
export function folderName(directory: string | null | undefined): string | null {
  const name = directory?.split("/").filter((part) => part !== "").at(-1);
  return name === undefined || name === "" ? null : name;
}

/** Task detail Alert for a task on a retired (replaced or forgotten) connection; `name` is its harness's name. */
export function retiredNotice(t: Pick<Task, "connectionLabel">, name: string): string {
  return `Ran on ${name}${quoted(t.connectionLabel)}, which Stage no longer uses. Its history and result stay here.`;
}

/** Hint on Start / Retry / Stop for a task on a retired (replaced or forgotten) connection; `name` is its harness's name. */
export function retiredHint(name: string): string {
  return `Stage no longer uses its ${name} connection. Duplicate it to run again.`;
}

const RESERVED_LINK_SCHEMES = new Set([
  "http", "https", "ftp", "ftps", "mailto", "tel", "javascript", "data", "vbscript", "file", "blob", "about", "stage",
]);

/** Every descriptor's link schemes: lower case, valid, deduplicated, sorted. */
export function linkSchemes(descriptors: readonly Pick<HarnessDescriptor, "linkSchemes">[]): string[] {
  const schemes = new Set<string>();
  for (const descriptor of descriptors) {
    if (!Array.isArray(descriptor.linkSchemes)) continue;
    for (const scheme of descriptor.linkSchemes) {
      if (typeof scheme === "string" && /^[a-z][a-z0-9+.-]*$/.test(scheme) && !RESERVED_LINK_SCHEMES.has(scheme) && !scheme.startsWith("stage-")) {
        schemes.add(scheme);
      }
    }
  }
  return [...schemes].toSorted();
}

/** The harness's active connection on a machine. */
export const activeOn = (list: readonly HarnessConnection[], h: Harness, machineId: number): HarnessConnection | undefined =>
  list.find((c) => c.harness === h && c.active && c.machineId === machineId);

export const usageText = (c: Pick<HarnessConnection, "tasks" | "bindings">) =>
  `${count(c.tasks, "task")} · ${count(c.bindings, "binding")}`;

export function forgetPrompt(c: Pick<HarnessConnection, "label">): string {
  return `Forget ${c.label || "this connection"}? Its tasks keep their history and stay read-only. Its project bindings stop asking for repair, and pairing this instance again starts a new connection.`;
}

/** Workspace paths of the project's replaced bindings on a harness. */
export const replacedPaths = (project: Pick<Project, "bindings">, h: Harness): Set<string> =>
  new Set(project.bindings.filter((b) => b.harness === h && !b.connectionActive).map((b) => b.workspace));
