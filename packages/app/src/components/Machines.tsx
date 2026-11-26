// Settings > Connections: every machine as a group, its header (presence, role, build; inline rename, remove for a
// worker, Add connection) over its harness connection rows (components/ConnectionRows.tsx). Add machine opens a
// drawer that creates a one-time pairing link and waits for the worker to join. Logic: lib/machines.ts,
// lib/connectionRows.ts. API: docs/api.md "Machines".
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useId, useState, type FormEvent } from "react";
import { ApiError } from "../api/client.ts";
import { keys, useHarnesses, useMachines, useRemoveMachine, useRenameMachine } from "../api/queries.ts";
import type { Machine, MachinePairing, PairingStatus } from "../api/types.ts";
import { machineRole } from "../lib/connectionRows.ts";
import { dateTime } from "../lib/format.ts";
import { countdown, machineSetupAlert, machineStatusLine, machineTone, pairingPhase, removePrompt } from "../lib/machines.ts";
import { useApi } from "../platform/context.tsx";
import { AddConnection, ConnectionRowList, MachineScope, ManageDrawer, useMachineConnections, type ManageTarget } from "./ConnectionRows.tsx";
import { Step, Steps } from "./Steps.tsx";
import { Alert, Button, ConfirmInline, Drawer, ErrorText, Field, Hint, Input, Marker, Meta, Muted, Panel, QueryState, Row, RowList, SubHeading, CommandBox } from "./ui.tsx";

/** The Machines panel: one group per machine, the main machine first. */
export function MachinesPanel({ meta }: { meta?: string }) {
  const machines = useMachines();
  return (
    <Panel title="Machines" {...(meta === undefined ? {} : { meta })} flush>
      <QueryState isPending={machines.isPending} error={machines.error} hasData={machines.data !== undefined}>
        <RowList label="Machines">
          {machines.machines.map((m) => (
            <MachineScope key={m.id} machine={m}>
              <MachineGroup machine={m} />
            </MachineScope>
          ))}
        </RowList>
      </QueryState>
      <Hint className="border-t border-line px-3 py-2">
        Workers run <code className="font-mono text-fg">stage worker</code> and connect out to the main machine. Harness credentials stay on each machine.
      </Hint>
    </Panel>
  );
}

function MachineGroup({ machine: m }: { machine: Machine }) {
  const harnesses = useHarnesses();
  const connections = useMachineConnections(m);
  const [mode, setMode] = useState<"view" | "rename" | "remove">("view");
  const [manage, setManage] = useState<ManageTarget | null>(null);
  const remove = useRemoveMachine();
  const remote = m.kind === "remote";
  const role = machineRole(m);
  // Usage counts come from GET /connections (which lists only connections this server can reach); unknown ones show none.
  const usage = m.connections.map((c) => harnesses.connection(c.id) ?? c);
  const alert = m.status === "negotiating" || m.status === "needs_update" ? machineSetupAlert(m) : null;
  return (
    <Row bare>
      <div className="flex flex-col gap-2 bg-panel px-3 py-2">
        {mode === "rename" ? (
          <RenameForm machine={m} onDone={() => setMode("view")} />
        ) : (
          <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
            <Marker tone={machineTone(m)} />
            <div className="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-2 gap-y-0.5">
              <span className="min-w-0 truncate text-body font-medium text-fg">{m.label}</span>
              <Meta title={role === "main" ? "Stage runs here; workers connect to it" : "Runs harnesses for the main machine"}>{role}</Meta>
              <span className="font-mono text-meta wrap-anywhere text-muted tabular" title={m.lastSeenAt === null ? undefined : `Last seen ${dateTime(m.lastSeenAt)}`}>
                {machineStatusLine(m)}
              </span>
            </div>
            <div className="flex flex-wrap items-center gap-1.5 max-md:w-full max-md:pl-[18px]">
              <Button small variant="ghost" disabled={mode === "remove"} onClick={() => setMode("rename")} aria-label={`Rename ${m.label}`}>
                Rename
              </Button>
              <AddConnection machine={m} connections={connections} onPick={setManage} />
              {remote ? (
                <Button
                  small
                  variant="danger"
                  disabled={mode === "remove"}
                  aria-label={`Remove ${m.label}`}
                  onClick={() => {
                    remove.reset();
                    setMode("remove");
                  }}
                >
                  Remove
                </Button>
              ) : null}
            </div>
          </div>
        )}
        {mode === "remove" ? (
          <ConfirmInline
            danger
            confirmLabel="Remove machine"
            busy={remove.isPending}
            error={remove.error}
            onConfirm={() => remove.mutate(m.id)}
            onCancel={() => setMode("view")}
          >
            {removePrompt(m, usage, harnesses.kindName).map((line, i) => (
              <p key={i} className={i === 0 ? "font-medium" : "mt-1"}>
                {line}
              </p>
            ))}
          </ConfirmInline>
        ) : null}
        {alert === null ? null : <Hint className="wrap-anywhere pl-[18px]">{alert}</Hint>}
      </div>
      <ConnectionRowList machine={m} connections={connections} onManage={setManage} />
      {manage === null ? null : <ManageDrawer machine={m} target={manage} onClose={() => setManage(null)} />}
    </Row>
  );
}

function RenameForm({ machine, onDone }: { machine: Machine; onDone: () => void }) {
  const [label, setLabel] = useState(machine.label);
  const rename = useRenameMachine();
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (label.trim() === machine.label) {
      onDone();
      return;
    }
    rename.mutate({ id: machine.id, label }, { onSuccess: onDone });
  };
  return (
    <form onSubmit={submit} className="flex flex-col gap-1.5">
      <div className="flex flex-wrap items-center gap-2">
        <Input
          aria-label="Machine name"
          className="min-w-0 flex-1 basis-48"
          value={label}
          maxLength={100}
          autoFocus
          autoComplete="off"
          spellCheck={false}
          onChange={(e) => setLabel(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Escape") {
              e.stopPropagation();
              onDone();
            }
          }}
        />
        <Button small type="submit" variant="primary" busy={rename.isPending} disabled={label.trim() === ""}>
          Save
        </Button>
        <Button small variant="ghost" disabled={rename.isPending} onClick={onDone}>
          Cancel
        </Button>
      </div>
      <ErrorText error={rename.error} />
    </form>
  );
}

const PAIRING_POLL_MS = 1500;

/** Add machine: name (optional), then the one-time link and command, a live countdown and the worker's join. */
export function AddMachineDrawer({ onClose }: { onClose: () => void }) {
  const api = useApi();
  const [pairing, setPairing] = useState<MachinePairing | null>(null);
  const [now, setNow] = useState(Date.now);
  const status = useQuery({
    queryKey: [...keys.pairing, pairing?.id ?? 0],
    enabled: pairing !== null,
    queryFn: () => (pairing === null ? Promise.reject(new Error("no pairing")) : api.pairingStatus(pairing.id)),
    refetchInterval: (q) => (q.state.data === undefined || q.state.data.state === "waiting" ? PAIRING_POLL_MS : false),
    retry: false,
  });
  // A revoked or pruned code (404) can no longer join: shown as expired.
  const gone = status.error instanceof ApiError && status.error.status === 404;
  const phase = pairing === null ? null : gone ? "expired" : pairingPhase(status.data, pairing.expiresAt, now);
  const waiting = phase === "waiting";

  useEffect(() => {
    if (!waiting) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [waiting]);

  // Closing while the code still works revokes it (best effort: it expires on its own within 10 minutes).
  const close = () => {
    if (waiting && pairing !== null) api.revokePairing(pairing.id).catch(() => undefined);
    onClose();
  };

  const formId = useId();
  return (
    <Drawer
      title="Add machine"
      onClose={close}
      footer={
        pairing === null ? (
          <>
            <Button variant="primary" type="submit" form={formId}>
              Create pairing link
            </Button>
            <Button variant="ghost" onClick={close}>
              Cancel
            </Button>
          </>
        ) : phase === "joined" ? (
          <Button variant="primary" onClick={close}>
            Done
          </Button>
        ) : phase === "expired" ? (
          <>
            <Button variant="primary" onClick={() => setPairing(null)}>
              Create new link
            </Button>
            <Button variant="ghost" onClick={close}>
              Close
            </Button>
          </>
        ) : (
          <Button variant="ghost" onClick={close}>
            Cancel
          </Button>
        )
      }
    >
      {pairing === null || phase === null ? (
        <PairingForm
          id={formId}
          onCreated={(p) => {
            setNow(Date.now());
            setPairing(p);
          }}
        />
      ) : (
        <PairingView pairing={pairing} status={status.data} phase={phase} now={now} />
      )}
    </Drawer>
  );
}

function PairingForm({ id, onCreated }: { id: string; onCreated: (p: MachinePairing) => void }) {
  const api = useApi();
  const [label, setLabel] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setError(null);
    api
      .createPairing(label.trim())
      .then(onCreated, (err: unknown) => setError(err))
      .finally(() => setBusy(false));
  };
  const needsPublicURL = error instanceof ApiError && error.code === "public_url_required";
  return (
    <form id={id} onSubmit={submit} aria-busy={busy} className="flex flex-col gap-4">
      <p className="text-small text-fg">
        A worker runs its harnesses for the main machine. Stage creates a one-time link; run it with{" "}
        <code className="font-mono text-detail">stage worker join</code> on the machine to add.
      </p>
      <Field label="Name" hint="Optional. Empty = the name its worker suggests.">
        {(fieldId) => (
          <Input
            id={fieldId}
            data-autofocus
            value={label}
            maxLength={100}
            placeholder="work laptop"
            autoComplete="off"
            spellCheck={false}
            onChange={(e) => setLabel(e.target.value)}
          />
        )}
      </Field>
      {needsPublicURL ? (
        <Alert tone="plain">
          <p className="font-medium">Stage has no public address.</p>
          <p className="mt-1">
            Set <code className="font-mono text-detail">STAGE_PUBLIC_URL</code> to the address the new machine reaches Stage at, restart Stage, then
            add the machine again.
          </p>
        </Alert>
      ) : (
        <ErrorText error={error} />
      )}
      {busy ? <Muted>Creating link…</Muted> : null}
    </form>
  );
}

const PHASE_ANNOUNCE: Record<PairingStatus["state"], string> = {
  waiting: "Waiting for the worker to join.",
  joined: "The machine joined.",
  expired: "The pairing link expired.",
};

function PairingView({ pairing, status, phase, now }: { pairing: MachinePairing; status: PairingStatus | undefined; phase: PairingStatus["state"]; now: number }) {
  const qc = useQueryClient();
  const { machines } = useMachines();
  const joinedId = status?.machineId ?? null;
  useEffect(() => {
    if (phase === "joined") void qc.invalidateQueries({ queryKey: keys.machines });
  }, [phase, qc]);
  const joined = joinedId === null ? undefined : machines.find((m) => m.id === joinedId);
  const name = joined?.label ?? (pairing.label === "" ? `machine #${joinedId ?? ""}` : pairing.label);
  return (
    <div className="flex flex-col gap-4">
      <div className="border border-line">
        <Steps label="Pairing">
          <Step label="link" status="ok" note={pairing.label === "" ? "created · one use" : `created for ${pairing.label} · one use`} />
          <Step
            label="worker"
            status={phase === "waiting" ? "active" : phase === "joined" ? "ok" : "bad"}
            note={phase === "waiting" ? "waiting for stage worker join" : phase === "joined" ? `joined as ${name}` : "not joined"}
          />
          <Step
            label="expires"
            status={phase === "waiting" ? "idle" : phase === "joined" ? "ok" : "bad"}
            note={phase === "waiting" ? `in ${countdown(pairing.expiresAt, now)}` : phase === "joined" ? "used" : "expired"}
            tabular
          />
        </Steps>
      </div>
      <p className="sr-only" aria-live="polite">
        {phase === "joined" ? `${name} joined.` : PHASE_ANNOUNCE[phase]}
      </p>
      {phase === "waiting" ? (
        <>
          <section className="flex flex-col gap-2" aria-label="Command">
            <SubHeading title="On the machine to add, run" />
            <CommandBox command={pairing.command} />
          </section>
          <section className="flex flex-col gap-2" aria-label="Pairing link">
            <SubHeading title="Or give it the link" />
            <CommandBox command={pairing.link} />
          </section>
          <Muted>Shown once. The link adds one machine and works for 10 minutes. Closing this drawer revokes it.</Muted>
        </>
      ) : null}
      {phase === "joined" ? (
        <Muted>{name} is listed under Machines. It shows online once its worker connects; then add its harness connections there.</Muted>
      ) : null}
      {phase === "expired" ? <Muted>The link was not used in time. Create a new link to try again.</Muted> : null}
    </div>
  );
}
