// The shape of one harness's entry in the slot registry (index.ts): the harness's own UI and status reader behind
// one boundary. Everything outside src/harnesses/ reads a harness through its entry, its descriptor and its declared
// capabilities, never its kind (ADR 0007 decision 4; tests/boundaries.test.mjs).
import type { QueryClient, UseQueryOptions } from "@tanstack/react-query";
import type { ComponentType, ReactNode } from "react";
import type { HarnessApi } from "../api/client.ts";
import type { Harness, HarnessDescriptor, HarnessStatus, Machine } from "../api/types.ts";
import type { ConnectionChip, StatusRead, StatusView } from "../lib/connections.ts";

/** A machine's harness calls and its scoped query key. */
export type MachineCalls = { api: HarnessApi; key: (k: readonly unknown[]) => readonly unknown[] };

/** A connection whose status is read on its machine. */
export interface MachineConnection {
  harness: Harness;
  machine: Pick<Machine, "id" | "kind">;
}

/** A connection's live status view, paired with the connection. */
export interface ConnectionStatus<C> {
  connection: C;
  view: StatusView;
}

/** `refetchInterval: false`: read what another observer keeps fresh, without a second poll of the same status. */
export type PollOverride = { refetchInterval?: false };

/**
 * How one harness's status is read: its query on a machine (the one its Settings row runs there, so the cache is
 * shared), its view for the status button and launch gate, and its Settings row's one-line detail.
 */
export interface StatusReader<S> {
  query: (m: MachineCalls, kind: Harness, qc: QueryClient) => UseQueryOptions<S, Error, S, readonly unknown[]>;
  view: (read: StatusRead<S>, kind: Harness, name: string) => StatusView;
  rowDetail: (s: S) => string;
  presentation: (s: S) => HarnessStatus | undefined;
}

/** A Settings > Connections row for a live connection (components/ConnectionRow.tsx draws it). */
export interface ConnectionRowProps {
  descriptor: HarnessDescriptor;
  machine: Machine;
  onManage: () => void;
  overflow?: ReactNode;
  /** New tasks start on this connection (Preferences.defaultConnectionId). */
  isDefault: boolean;
}

/** One harness's slots. A kind without an entry gets the generic one (generic.tsx). */
export interface HarnessSlots {
  /** The Manage drawer's setup panel(s) on the harness scope's machine; each places its NewTaskDefaults. */
  Setup: ComponentType<{ descriptor: HarnessDescriptor }>;
  /** Its Settings > Connections row (name, where, state, Manage, Disconnect). */
  Row: ComponentType<ConnectionRowProps>;
  /** Each connection's live status view on its own machine, in input order (one query per connection; call with a stable count). */
  useStatuses: <C extends MachineConnection>(connections: readonly C[], poll: PollOverride, name: (kind: Harness) => string) => ConnectionStatus<C>[];
  /** A Config panel Harness row's chip, read on the harness scope's machine, handed to `children`. */
  LiveChip: ComponentType<{ kind: Harness; name: string; children: (chip: ConnectionChip) => ReactNode }>;
  /** The status query's key prefix, refreshed after Sync now. */
  statusKey: (kind: Harness) => readonly unknown[];
  /** Read the full status envelope through the setup panel’s existing cache. */
  Presentation: ComponentType<{ kind: Harness; enabled: boolean; children: (status: HarnessStatus | undefined) => ReactNode }>;
  /** Actions currently rendered by the setup panel itself. */
  handledSetupActions: (id: string) => boolean;
}
