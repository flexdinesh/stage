// A Config panel's Harness row: the harness name, its connection's live state chip (the Settings Connections row's
// tones, read on the connection's machine) and, while Settings can fix a problem there, a link to it.
// Logic: lib/connections.ts (connectionGate, liveChip).
import type { ReactNode } from "react";
import { useHarnesses, useMachines } from "../api/queries.ts";
import type { Harness } from "../api/types.ts";
import { harnessSlots } from "../harnesses/index.ts";
import { connectionGate, type ConnectionChip } from "../lib/connections.ts";
import { SETTINGS_PATH } from "../lib/settings.ts";
import { MachineScope } from "./ConnectionRow.tsx";
import { Chip, TextLink } from "./ui.tsx";

/** The Harness row's value: `children` (the name) with the connection's state chip, and "Fix in Settings" under it on a problem. */
export function HarnessConnectionState({ connectionId, children }: { connectionId: number; children: ReactNode }) {
  const harnesses = useHarnesses();
  const { machines } = useMachines();
  const connection = harnesses.connection(connectionId);
  const machine = connection === undefined ? undefined : machines.find((m) => m.id === connection.machineId);
  const gate = connectionGate(connection, machine);
  let state: ReactNode = null;
  if (gate !== null && gate !== "live") state = <State chip={gate} />;
  else if (gate === "live" && connection !== undefined && machine !== undefined) {
    state = (
      <MachineScope machine={machine}>
        <LiveState harness={connection.harness} name={harnesses.kindName(connection.harness)} />
      </MachineScope>
    );
  }
  return (
    <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
      <span>{children}</span>
      {state}
    </span>
  );
}

/** The live chip from the harness's status reader (its registry entry), on the connection's machine. */
function LiveState({ harness, name }: { harness: Harness; name: string }) {
  const { LiveChip } = harnessSlots(harness);
  return (
    <LiveChip kind={harness} name={name}>
      {(chip) => <State chip={chip} />}
    </LiveChip>
  );
}

function State({ chip }: { chip: ConnectionChip }) {
  return (
    <>
      <Chip tone={chip.tone} dot title={chip.title} className="shrink-0">
        {chip.label}
      </Chip>
      {chip.fixable ? (
        <TextLink to={SETTINGS_PATH.connections} className="basis-full font-sans text-small text-fg">
          Fix in Settings
        </TextLink>
      ) : null}
    </>
  );
}
