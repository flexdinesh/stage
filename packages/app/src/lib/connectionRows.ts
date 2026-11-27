// Settings Connections: each machine's connection rows and what Add connection offers there. Rows are a machine's
// active connections that are set up, and disconnected ones (set up once or holding history) to reconnect or remove;
// one never set up (configured false, nothing on it) is not a row.
import type { Harness, HarnessConnection, HarnessDescriptor, Machine } from "../api/types.ts";
import { isDisconnected } from "./harnesses.ts";
import { machineSetupAlert, machineSetupState } from "./machines.ts";

export interface ConnectionRowModel {
  id: number;
  harness: Harness;
  label: string;
  /** Disconnected: it offers Reconnect and Remove connection instead of Manage and Disconnect. */
  disconnected: boolean;
}

/** The machine Stage runs on is `main`; every other machine is a `worker` connected to it. */
export const machineRole = (m: Pick<Machine, "kind">): "main" | "worker" => (m.kind === "local" ? "main" : "worker");

/** True when the machine's harness setup can run now (local, or a remote whose worker is online and compatible). */
export const machineLive = (m: Pick<Machine, "kind" | "status">): boolean => machineSetupState(m) === "ready";

type RowLookup = (id: number) => Pick<HarnessConnection, "active" | "configured" | "label" | "tasks" | "bindings"> | undefined;

/**
 * A machine's connection rows, from its active connections. `lookup` (GET /connections) knows `configured` for the
 * connections this server can reach; an unknown one counts as set up, since the machine lists it as active.
 */
export function connectionRows(m: Pick<Machine, "connections">, lookup: RowLookup): ConnectionRowModel[] {
  return m.connections.flatMap((c) => {
    const known = lookup(c.id);
    const disconnected = known !== undefined && isDisconnected(known);
    if (known?.configured === false && !disconnected) return [];
    return [{ id: c.id, harness: c.harness, label: c.label, disconnected }];
  });
}

/** Rows that run tasks now (Settings' connection count). */
export const connectedRows = (rows: readonly ConnectionRowModel[]) => rows.filter((r) => !r.disconnected);

export interface AddConnectionOption {
  kind: Harness;
  name: string;
  /** Why it can't be added (already connected, unavailable here), short; null when it can. */
  reason: string | null;
  /** The machine's full reason it cannot run the harness (the server's); null when it can. */
  detail: string | null;
}

/**
 * Connectable harnesses and harnesses that require local processes. Disabled options explain why the machine cannot
 * run or set up the harness, or why its existing row needs to be used.
 */
export function addConnectionOptions(
  descriptors: readonly Pick<HarnessDescriptor, "kind" | "name" | "connectable" | "localProcesses" | "available" | "reason">[],
  rows: readonly (Pick<ConnectionRowModel, "harness"> & Partial<Pick<ConnectionRowModel, "disconnected">>)[],
): AddConnectionOption[] {
  const option = (d: (typeof descriptors)[number]): AddConnectionOption => {
    if (!d.available) return { kind: d.kind, name: d.name, reason: "unavailable here", detail: d.reason === undefined || d.reason === "" ? `${d.name} can't run on this machine.` : d.reason };
    if (!d.connectable) return { kind: d.kind, name: d.name, reason: "no connection setup here", detail: null };
    const row = rows.find((r) => r.harness === d.kind);
    const reason = row === undefined ? null : row.disconnected === true ? "disconnected; reconnect it" : "already connected";
    return { kind: d.kind, name: d.name, reason, detail: null };
  };
  return descriptors.filter((d) => d.connectable || d.localProcesses).map(option);
}

/** The empty machine's shortcuts: a Connect button per harness it can add now. */
export function connectShortcuts(options: readonly AddConnectionOption[] | null): { kind: Harness; label: string }[] {
  return (options ?? []).filter((o) => o.reason === null).map((o) => ({ kind: o.kind, label: `Connect ${o.name}` }));
}

/** Row reasons: the harness already has a connection row on the machine. */
const ROW_REASONS: readonly (string | null)[] = ["already connected", "disconnected; reconnect it"];

/**
 * Why Add connection is disabled; null when enabled.
 * It stays enabled while a harness is listed as unavailable (`detail`), so its reason can be read in the menu.
 */
export function addConnectionReason(m: Pick<Machine, "kind" | "status" | "label" | "statusReason">, options: readonly AddConnectionOption[] | null): string | null {
  switch (machineSetupState(m)) {
    case "offline":
      return `${m.label} is offline`;
    case "negotiating":
    case "needs_update":
      return machineSetupAlert(m);
    case "ready":
      break;
  }
  if (options === null) return "checking harnesses";
  if (options.length === 0) return "no harness to connect";
  if (options.some((o) => o.reason === null || o.detail !== null)) return null;
  return options.every((o) => ROW_REASONS.includes(o.reason)) ? "every harness is connected" : "no harness to connect";
}
