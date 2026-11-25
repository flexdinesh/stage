// A Config panel's Harness row: the harness name, its connection's live state chip (the Settings Connections row's
// tones, read on the connection's machine) and, while Settings can fix a problem there, a link to it.
// Logic: lib/connections.ts (connectionGate, liveChip).
import type { ReactNode } from "react";
import { useCodexStatus, useHarnesses, useMachines, useOpenCodeStatus, useT3Status } from "../api/queries.ts";
import type { Harness } from "../api/types.ts";
import {
  codexErrorText,
  codexRowDetail,
  codexSummary,
  connectionGate,
  harnessSummary,
  liveChip,
  openCodeErrorText,
  openCodeRowDetail,
  openCodeSummary,
  statusRead,
  t3ErrorText,
  t3RowDetail,
  t3Summary,
  type ConnectionChip,
} from "../lib/connections.ts";
import { SETTINGS_PATH } from "../lib/settings.ts";
import { MachineScope } from "./ConnectionRows.tsx";
import { useHarnessStatus } from "./HarnessConnections.tsx";
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

function LiveState({ harness, name }: { harness: Harness; name: string }) {
  switch (harness) {
    case "t3":
      return <T3State />;
    case "opencode":
      return <OpenCodeState />;
    case "codex":
      return <CodexState />;
    default:
      return <GenericState harness={harness} name={name} />;
  }
}

function T3State() {
  const read = statusRead(useT3Status());
  return <State chip={liveChip(t3Summary(read), read.data === undefined ? "" : t3RowDetail(read.data), t3ErrorText(read))} />;
}

function OpenCodeState() {
  const read = statusRead(useOpenCodeStatus());
  return <State chip={liveChip(openCodeSummary(read), read.data === undefined ? "" : openCodeRowDetail(read.data), openCodeErrorText(read))} />;
}

function CodexState() {
  const read = statusRead(useCodexStatus());
  return <State chip={liveChip(codexSummary(read), read.data === undefined ? "" : codexRowDetail(read.data), codexErrorText(read))} />;
}

function GenericState({ harness, name }: { harness: Harness; name: string }) {
  const read = statusRead(useHarnessStatus(harness));
  const s = read.data;
  return <State chip={liveChip(harnessSummary(read, harness, name), s?.reason ?? s?.lastError ?? "", read.error)} />;
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
