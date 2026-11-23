// Machines (GET /api/machines): hosts that run harness connections. Machine 1 is local (the main machine, Stage's own process); others
// run `stage worker` and join by pairing. Settings lists them; connection pickers name a connection's machine only when
// more than one exists, so a single-machine install reads exactly as before. Rendering: components/Machines.tsx.
import type { Harness, HarnessConnection, Machine, PairingStatus } from "../api/types.ts";
import type { ComboboxOption } from "./combobox.ts";
import { relativeTime } from "./format.ts";

/** The server's own machine (GET /api/machines lists it first). */
export const LOCAL_MACHINE_ID = 1;

const count = (n: number, noun: string) => `${n} ${noun}${n === 1 ? "" : "s"}`;

/** The machine's marker tone: ok while online, idle otherwise. */
export const machineTone = (m: Pick<Machine, "online">): "ok" | "idle" => (m.online ? "ok" : "idle");

/** True when the machine runs another build than the server (`serverVersion`: the local machine's). */
export function versionDiffers(m: Pick<Machine, "kind" | "version">, serverVersion: string | null): boolean {
  return m.kind === "remote" && m.version !== null && serverVersion !== null && m.version !== serverVersion;
}

/** Mono status line: "online · v1.6.0", "offline · seen 4m ago · v1.5.0", "offline · never seen". */
export function machineStatusLine(m: Pick<Machine, "online" | "lastSeenAt" | "version">, now: number = Date.now()): string {
  const parts = [m.online ? "online" : "offline"];
  if (!m.online) parts.push(m.lastSeenAt === null ? "never seen" : `seen ${relativeTime(m.lastSeenAt, now)}`);
  if (m.version !== null) parts.push(m.version);
  return parts.join(" · ");
}

/** The local machine's build, the one remote machines are compared with. */
export const serverVersion = (machines: readonly Machine[]): string | null => machines.find((m) => m.kind === "local")?.version ?? null;

/** Remaining time on a pairing code as m:ss ("0:00" once past). */
export function countdown(expiresAt: string, now: number = Date.now()): string {
  const left = Math.max(0, Math.ceil((Date.parse(expiresAt) - now) / 1000));
  if (Number.isNaN(left)) return "0:00";
  return `${Math.floor(left / 60)}:${String(left % 60).padStart(2, "0")}`;
}

/** The pairing as the Add machine drawer shows it: the server's state, else expired by the clock while it says waiting. */
export function pairingPhase(status: Pick<PairingStatus, "state"> | undefined, expiresAt: string, now: number = Date.now()): PairingStatus["state"] {
  if (status !== undefined && status.state !== "waiting") return status.state;
  return Date.parse(expiresAt) <= now ? "expired" : "waiting";
}

/** A machine's connection in the remove confirmation; usage counts when GET /connections lists it. */
export type RemovedConnection = Pick<HarnessConnection, "harness" | "label"> & Partial<Pick<HarnessConnection, "tasks" | "bindings">>;

/** "T3 env-a (12 tasks · 2 bindings)", or "T3 env-a" when its counts are unknown. */
function connectionUsage(c: RemovedConnection, kindName: (k: Harness) => string): string {
  const name = c.label === "" ? kindName(c.harness) : `${kindName(c.harness)} ${c.label}`;
  return c.tasks === undefined || c.bindings === undefined ? name : `${name} (${count(c.tasks, "task")} · ${count(c.bindings, "binding")})`;
}

/**
 * The remove confirmation: what happens to the worker, its connections and their tasks (DELETE /api/machines/{id}).
 * `connections`: the machine's active connections, with usage counts where GET /connections has them.
 */
export function removePrompt(
  m: Pick<Machine, "label">,
  connections: readonly RemovedConnection[],
  kindName: (k: Harness) => string,
): string[] {
  const lines = [`Remove ${m.label}? Its worker is signed out at its next request and stops.`];
  if (connections.length === 0) {
    lines.push("It has no harness connections.");
  } else {
    lines.push(
      `${connections.map((c) => connectionUsage(c, kindName)).join(", ")} ${connections.length === 1 ? "becomes" : "become"} read-only history: tasks, sessions and bindings stay, but Stage no longer launches on or follows ${connections.length === 1 ? "it" : "them"}, including tasks running there. A connection with nothing recorded is deleted.`,
      "Backlog tasks and schedules on these connections stay until you move them to another harness.",
    );
  }
  lines.push("Pair the machine again to bring it back as a new machine.");
  return lines;
}

/** The connection's machine label when more than one machine exists (the picker's tag); undefined otherwise. */
export function machineTag(machines: readonly Pick<Machine, "id" | "label">[], machineId: number): string | undefined {
  if (machines.length < 2) return undefined;
  return machines.find((m) => m.id === machineId)?.label;
}

/**
 * Options for a connection picker: each connection by its harness name, its machine as the tag when more than one
 * machine exists, ordered by machine (local first). `bound` (a chosen project's active bindings) lists those
 * connections first and marks the rest "not bound"; null leaves the order and adds no chip. `defaultId` (the default
 * connection) is marked "default".
 */
export function connectionOptions(
  connections: readonly Pick<HarnessConnection, "id" | "harness" | "machineId">[],
  machines: readonly Pick<Machine, "id" | "label">[],
  kindName: (k: Harness) => string,
  bound: ReadonlySet<number> | null = null,
  defaultId: number | null = null,
): ComboboxOption[] {
  const rank = (machineId: number) => {
    const i = machines.findIndex((m) => m.id === machineId);
    return i < 0 ? machines.length : i;
  };
  const group = (id: number) => (bound === null || bound.has(id) ? 0 : 1);
  const chip = (id: number) => {
    const chips = [id === defaultId ? "default" : null, bound === null || bound.has(id) ? null : "not bound"].filter((c) => c !== null);
    return chips.length === 0 ? undefined : chips.join(" · ");
  };
  return connections
    .map((c, i) => ({ c, i }))
    .toSorted((a, b) => group(a.c.id) - group(b.c.id) || rank(a.c.machineId) - rank(b.c.machineId) || a.i - b.i)
    .map(({ c }) => ({
      value: String(c.id),
      label: kindName(c.harness),
      tag: machineTag(machines, c.machineId),
      chip: chip(c.id),
    }));
}

/** A connection's name with its machine when one is given: "T3 · desk", else "T3". */
export const withMachine = (name: string, tag: string | undefined): string => (tag === undefined ? name : `${name} · ${tag}`);

/** A harness query key on a machine: the local machine (null) keeps key; a remote machine's lives under ["machine", id]. */
export const machineKey = (machineId: number | null, key: readonly unknown[]): readonly unknown[] =>
  machineId === null ? key : ["machine", machineId, ...key];

/** Where a machine's harness setup stands: ready (online), offline, or needs_update (another API version). */
export type MachineSetupState = "ready" | "offline" | "needs_update";

export const machineSetupState = (m: Pick<Machine, "kind" | "status">): MachineSetupState =>
  m.kind === "local" || m.status === "online" ? "ready" : m.status;

/** Why a machine's harness setup cannot be driven (Settings Connections); null when it can. */
export function machineSetupAlert(m: Pick<Machine, "kind" | "status" | "label" | "version">, server: string | null): string | null {
  switch (machineSetupState(m)) {
    case "ready":
      return null;
    case "offline":
      return `${m.label} is offline. Its harness setup returns when its worker reconnects (stage worker run, or its service).`;
    case "needs_update": {
      const versions = m.version !== null && server !== null ? ` It runs ${m.version}; the main machine runs ${server}.` : "";
      return `${m.label} needs an update: its worker speaks another API version.${versions} Install the same Stage version there.`;
    }
  }
}

/** Setup copy that names the machine where its CLIs run (`remote`: a remote machine's label, null for the local machine). */
export function setupCopy(remote: string | null) {
  return {
    /** "this machine", or the remote machine's label. */
    machine: remote ?? "this machine",
  };
}
