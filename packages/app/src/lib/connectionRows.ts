// Settings Connections: each machine's connection rows and what Add connection offers there. Rows are a machine's
// active connections that are set up; one planned before setup (configured false) is not a row yet.
import type { Harness, HarnessConnection, HarnessDescriptor, Machine } from "../api/types.ts";
import { machineSetupState } from "./machines.ts";

export interface ConnectionRowModel {
  id: number;
  harness: Harness;
  label: string;
}

/** The machine Stage runs on is `main`; every other machine is a `worker` connected to it. */
export const machineRole = (m: Pick<Machine, "kind">): "main" | "worker" => (m.kind === "local" ? "main" : "worker");

/** True when the machine's harness setup can run now (local, or a remote whose worker is online and compatible). */
export const machineLive = (m: Pick<Machine, "kind" | "status">): boolean => machineSetupState(m) === "ready";

/**
 * A machine's connection rows, from its active connections. `lookup` (GET /connections) knows `configured` for the
 * connections this server can reach; an unknown one counts as set up, since the machine lists it as active.
 */
export function connectionRows(m: Pick<Machine, "connections">, lookup: (id: number) => Pick<HarnessConnection, "configured"> | undefined): ConnectionRowModel[] {
  return m.connections.filter((c) => lookup(c.id)?.configured !== false).map((c) => ({ id: c.id, harness: c.harness, label: c.label }));
}

export interface AddConnectionOption {
  kind: Harness;
  name: string;
  /** Why it can't be added (already connected); null when it can. */
  reason: string | null;
}

/** Every connectable harness on the machine; one it already has a row for is listed disabled. */
export function addConnectionOptions(descriptors: readonly Pick<HarnessDescriptor, "kind" | "name" | "connectable">[], rows: readonly Pick<ConnectionRowModel, "harness">[]): AddConnectionOption[] {
  return descriptors
    .filter((d) => d.connectable)
    .map((d) => ({ kind: d.kind, name: d.name, reason: rows.some((r) => r.harness === d.kind) ? "already connected" : null }));
}

/** Why Add connection is disabled on the machine (offline, needs an update, nothing left to add); null when enabled. */
export function addConnectionReason(m: Pick<Machine, "kind" | "status" | "label">, options: readonly AddConnectionOption[] | null): string | null {
  switch (machineSetupState(m)) {
    case "offline":
      return `${m.label} is offline`;
    case "needs_update":
      return `${m.label} needs an update`;
    case "ready":
      break;
  }
  if (options === null) return "checking harnesses";
  if (options.length === 0) return "no harness to connect";
  return options.every((o) => o.reason !== null) ? "every harness is connected" : null;
}
