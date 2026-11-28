import { useQueryClient } from "@tanstack/react-query";
import { useHarnessApi, useScopedKey } from "../api/harnessScope.tsx";
import { invalidateConnectionChange, useHarnesses } from "../api/queries.ts";
import type { ActionResult, HarnessAction, HarnessDescriptor, HarnessStatus, ReplacedConnection } from "../api/types.ts";
import { harnessSlots } from "../harnesses/index.ts";
import { availableSetupActions } from "../lib/connectionSetup.ts";
import { useApi } from "../platform/context.tsx";
import { DeclaredActions } from "./DeclaredActions.tsx";

export function ConnectionDeclaredActions({ descriptor, status, actions, onReplaced }: {
  descriptor: HarnessDescriptor;
  status: HarnessStatus;
  actions?: readonly HarnessAction[];
  onReplaced: (replaced: ReplacedConnection | null) => void;
}) {
  const api = useApi();
  const setup = useHarnessApi();
  const scoped = useScopedKey();
  const qc = useQueryClient();
  const harnesses = useHarnesses();
  const key = harnessSlots(descriptor.kind).statusKey(descriptor.kind);
  const refresh = () => invalidateConnectionChange(qc, scoped, [key]);
  const run = async (id: string, input: Record<string, unknown>, confirmed: boolean): Promise<ActionResult> => {
    if (status.configured && status.connectionId > 0) {
      const result = await api.runConnectionAction(status.connectionId, id, input, confirmed);
      if (result.replaced !== undefined) {
        onReplaced(harnesses.connection(result.replaced) ?? { id: result.replaced, label: "", tasks: 0, bindings: 0 });
      }
      await refresh();
      return result;
    }
    const result = await setup.runHarnessAction(descriptor.kind, id, input, confirmed);
    onReplaced(result.replaced ?? null);
    await refresh();
    return { message: result.message ?? "", ...(result.link ? { link: result.link } : {}) };
  };
  return <DeclaredActions actions={actions ?? availableSetupActions(descriptor.setup.actions, status.actions)} run={run}
    connectionId={status.configured && status.connectionId > 0 ? status.connectionId : undefined} />;
}
