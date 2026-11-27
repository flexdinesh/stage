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
export const machineTone = (m: Pick<Machine, "status">): "ok" | "idle" => (m.status === "online" ? "ok" : "idle");

const MACHINE_STATUS_LABEL: Record<Machine["status"], string> = {
  online: "online",
  offline: "offline",
  negotiating: "Negotiating",
  needs_update: "needs update",
};

export const machineStatusLabel = (status: Machine["status"]): string => MACHINE_STATUS_LABEL[status];

/** Mono status line: "online · v1.6.0", "offline · seen 4m ago · v1.5.0", "offline · never seen". */
export function machineStatusLine(m: Pick<Machine, "status" | "lastSeenAt" | "version">, now: number = Date.now()): string {
  const parts = [machineStatusLabel(m.status)];
  if (m.status === "offline") parts.push(m.lastSeenAt === null ? "never seen" : `seen ${relativeTime(m.lastSeenAt, now)}`);
  if (m.version !== null) parts.push(m.version);
  return parts.join(" · ");
}

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
 * machine exists, ordered by machine (local first).
 */
export function connectionOptions(
  connections: readonly Pick<HarnessConnection, "id" | "harness" | "machineId">[],
  machines: readonly Pick<Machine, "id" | "label">[],
  kindName: (k: Harness) => string,
): ComboboxOption[] {
  const rank = (machineId: number) => {
    const i = machines.findIndex((m) => m.id === machineId);
    return i < 0 ? machines.length : i;
  };
  return connections
    .map((c, i) => ({ c, i }))
    .toSorted((a, b) => rank(a.c.machineId) - rank(b.c.machineId) || a.i - b.i)
    .map(({ c }) => ({
      value: String(c.id),
      label: kindName(c.harness),
      tag: machineTag(machines, c.machineId),
    }));
}

/**
 * Where a resume command must run: the session's machine label when it is a remote machine (a worker), so the
 * command copied here is run there; undefined on the local machine or when only one machine exists.
 */
export function resumeMachine(machines: readonly Pick<Machine, "id" | "label">[], machineId: number): string | undefined {
  return machineId === LOCAL_MACHINE_ID ? undefined : machineTag(machines, machineId);
}

/** The resume command's copy label: "Copy resume command", or "… for <machine>" when it runs on another machine. */
export const resumeLabel = (machine: string | undefined): string => (machine === undefined ? "Copy resume command" : `Copy resume command for ${machine}`);

/** A connection's name with its machine when one is given: "T3 · desk", else "T3". */
export const withMachine = (name: string, tag: string | undefined): string => (tag === undefined ? name : `${name} · ${tag}`);

/** A harness query key on a machine: the local machine (null) keeps key; a remote machine's lives under ["machine", id]. */
/** The harness scope id of a machine: null for the local machine (its own routes and keys), else its id. */
export const machineScopeId = (m: Pick<Machine, "id" | "kind">): number | null => (m.kind === "local" ? null : m.id);

export const machineKey = (machineId: number | null, key: readonly unknown[]): readonly unknown[] =>
  machineId === null ? key : ["machine", machineId, ...key];

/** Only the local machine and an online worker can serve harness setup. */
export type MachineSetupState = "ready" | Exclude<Machine["status"], "online">;

export const machineSetupState = (m: Pick<Machine, "kind" | "status">): MachineSetupState =>
  m.kind === "local" || m.status === "online" ? "ready" : m.status;

/** Why a machine's harness setup cannot be driven (Settings Connections); null when it can. */
export function machineSetupAlert(m: Pick<Machine, "kind" | "status" | "label" | "statusReason">): string | null {
  switch (machineSetupState(m)) {
    case "ready":
      return null;
    case "offline":
      return `${m.label} is offline. Its harness setup returns when its worker reconnects (stage worker run, or its service).`;
    case "negotiating":
      return m.statusReason || "Waiting for the worker to finish connecting before harness setup is available.";
    case "needs_update":
      return m.statusReason || "The worker and main machine need compatible Stage versions before harness setup is available.";
  }
}

/** Setup copy that names the machine where its CLIs run (`remote`: a remote machine's label, null for the local machine). */
export function setupCopy(remote: string | null) {
  return {
    /** "this machine", or the remote machine's label. */
    machine: remote ?? "this machine",
  };
}
