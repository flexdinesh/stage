// The machine whose harness connections a setup panel drives: the local
// machine (Settings) or a remote machine through its worker (the machine
// page). Panels read their API calls, query keys and copy from here.
import { createContext, useContext, useMemo, type ReactNode } from "react";
import { useApi } from "../platform/context.tsx";
import { LOCAL_MACHINE_ID, machineKey, setupCopy } from "../lib/machines.ts";
import type { HarnessApi } from "./client.ts";

export interface HarnessScope {
  /** null: the local machine (its existing /api/harnesses routes and query keys). */
  machineId: number | null;
  /** How copy names the machine: "this machine" locally, else its label. */
  label: string;
}

export const LOCAL_SCOPE: HarnessScope = { machineId: null, label: "this machine" };

const ScopeContext = createContext<HarnessScope>(LOCAL_SCOPE);

export function HarnessScopeProvider({ scope, children }: { scope: HarnessScope; children: ReactNode }) {
  return <ScopeContext.Provider value={scope}>{children}</ScopeContext.Provider>;
}

export const useHarnessScope = (): HarnessScope => useContext(ScopeContext);

/** The scope's machine id (the local machine's for the local scope). */
export const useScopeMachineId = (): number => useHarnessScope().machineId ?? LOCAL_MACHINE_ID;

/** The scope's harness setup calls. */
export function useHarnessApi(): HarnessApi {
  const api = useApi();
  const { machineId } = useHarnessScope();
  return useMemo(() => (machineId === null ? api : api.forMachine(machineId)), [api, machineId]);
}

/** Harness query keys in the scope (lib/machines.ts machineKey). */
export function useScopedKey(): (key: readonly unknown[]) => readonly unknown[] {
  const { machineId } = useHarnessScope();
  return useMemo(() => (key: readonly unknown[]) => machineKey(machineId, key), [machineId]);
}

/** Setup copy naming the scope's machine (lib/machines.ts setupCopy). */
export function useSetupCopy(): ReturnType<typeof setupCopy> {
  const scope = useHarnessScope();
  return useMemo(() => setupCopy(scope.machineId === null ? null : scope.label), [scope]);
}
