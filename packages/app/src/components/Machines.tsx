// Settings MACHINES: every machine with presence, last seen, build and its harness connections; inline rename and
// remove (remote only); a remote machine's Harnesses link opens its page (pages/MachinePage.tsx); Add machine opens a drawer that creates a one-time pairing link and waits for the worker to
// join. Logic: lib/machines.ts. API: docs/api.md "Machines".
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useId, useState, type FormEvent } from "react";
import { Link } from "react-router";
import { ApiError } from "../api/client.ts";
import { keys, useHarnesses, useMachines, useRemoveMachine, useRenameMachine } from "../api/queries.ts";
import type { Machine, MachinePairing, PairingStatus } from "../api/types.ts";
import { dateTime } from "../lib/format.ts";
import { countdown, machineStatusLine, machineTone, pairingPhase, removePrompt, serverVersion, versionDiffers } from "../lib/machines.ts";
import { useApi } from "../platform/context.tsx";
import { Step, Steps } from "./Steps.tsx";
import { Alert, Button, ConfirmInline, Chip, Drawer, ErrorText, Field, Hint, Input, Marker, Muted, Panel, QueryState, Row, RowList, SubHeading, buttonClass, CommandBox } from "./ui.tsx";

export function MachinesCard() {
  const machines = useMachines();
  const [adding, setAdding] = useState(false);
  const server = serverVersion(machines.machines);
  return (
    <>
      <Panel
        title="Machines"
        meta={machines.data === undefined ? undefined : `${machines.machines.length} ${machines.machines.length === 1 ? "machine" : "machines"}`}
        actions={
          <Button small onClick={() => setAdding(true)}>
            Add machine
          </Button>
        }
        flush
      >
        <QueryState isPending={machines.isPending} error={machines.error}>
          <RowList>
            {machines.machines.map((m) => (
              <MachineRow key={m.id} machine={m} serverVersion={server} />
            ))}
          </RowList>
        </QueryState>
        <Hint className="border-t border-line px-3 py-2">
          Remote machines run <code className="font-mono text-fg">stage worker</code> and connect out to this server.
        </Hint>
      </Panel>
      {adding ? <AddMachineDrawer onClose={() => setAdding(false)} /> : null}
    </>
  );
}

function MachineRow({ machine: m, serverVersion: server }: { machine: Machine; serverVersion: string | null }) {
  const harnesses = useHarnesses();
  const [mode, setMode] = useState<"view" | "rename" | "remove">("view");
  const remove = useRemoveMachine();
  const remote = m.kind === "remote";
  // Usage counts come from GET /connections (which lists only connections this server can reach); unknown ones show none.
  const usage = m.connections.map((c) => harnesses.connection(c.id) ?? c);
  return (
    <Row className="flex flex-col gap-2">
      {mode === "rename" ? (
        <RenameForm machine={m} onDone={() => setMode("view")} />
      ) : (
        <div className="flex flex-wrap items-start gap-x-3 gap-y-2">
          <span className="flex h-6 items-center">
            <Marker tone={machineTone(m)} />
          </span>
          <div className="flex min-w-0 flex-1 flex-col gap-0.5">
            <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
              <span className="min-w-0 truncate text-body font-medium text-fg">{m.label}</span>
              {remote ? null : <Chip>this server</Chip>}
            </div>
            <p className="font-mono text-meta text-muted" title={m.lastSeenAt === null ? undefined : `Last seen ${dateTime(m.lastSeenAt)}`}>
              <span className="tabular">{machineStatusLine(m)}</span>
              {versionDiffers(m, server) ? <span className="whitespace-nowrap"> · server {server}</span> : null}
            </p>
            {m.connections.length === 0 ? (
              <p className="font-mono text-meta text-muted">no harness connections</p>
            ) : (
              <ul aria-label={`${m.label} harness connections`} className="flex flex-wrap gap-x-3 gap-y-0.5 font-mono text-meta">
                {m.connections.map((c) => (
                  <li key={c.id} className="min-w-0 break-all">
                    <span className="text-fg">{harnesses.kindName(c.harness)}</span>
                    {c.label === "" ? null : <span className="text-muted"> {c.label}</span>}
                  </li>
                ))}
              </ul>
            )}
          </div>
          <div className="flex flex-wrap items-center gap-1.5 max-md:w-full max-md:pl-[18px]">
            {remote ? (
              <Link to={`/settings/machines/${m.id}`} className={buttonClass("secondary", true)} aria-label={`${m.label} harnesses`}>
                Harnesses
              </Link>
            ) : null}
            <Button small variant="ghost" disabled={mode === "remove"} onClick={() => setMode("rename")}>
              Rename
            </Button>
            {remote ? (
              <Button
                small
                variant="danger"
                disabled={mode === "remove"}
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
function AddMachineDrawer({ onClose }: { onClose: () => void }) {
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
  const joinedId = status.data?.machineId ?? null;

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
          <>
            {joinedId === null ? null : (
              <Link to={`/settings/machines/${joinedId}`} className={buttonClass("primary")} onClick={close}>
                Set up harnesses
              </Link>
            )}
            <Button variant="ghost" onClick={close}>
              Done
            </Button>
          </>
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
        A remote machine runs its harnesses for this server. Stage creates a one-time link; run it with{" "}
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
          <p className="font-medium">This server has no public address.</p>
          <p className="mt-1">
            Set <code className="font-mono text-detail">STAGE_PUBLIC_URL</code> to the address the new machine reaches this server at, restart Stage, then
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
        <Muted>{name} is listed under Machines. It shows online once its worker connects; then set up its harnesses on its page.</Muted>
      ) : null}
      {phase === "expired" ? <Muted>The link was not used in time. Create a new link to try again.</Muted> : null}
    </div>
  );
}
