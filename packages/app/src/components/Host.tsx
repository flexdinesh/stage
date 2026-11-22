// Host-managed connection UI (desktop): the connection screen shown until the
// active Stage server first answers, the banner for later trouble, and the
// Settings panel for the desktop app, with this machine as a worker of the
// active remote (join, state, leave). A host without `platform.host` (the
// browser) renders none of it. Server switching: Servers.tsx.
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { Link } from "react-router";
import { keys } from "../api/queries.ts";
import {
  ACTION_LABEL,
  activeWorker,
  canRestartWorker,
  canSetUpWorker,
  gateView,
  leavePrompt,
  primaryAction,
  stepNote,
  stepStatus,
  visibleSteps,
  workerBanner,
  workerHeadline,
  workerLine,
  workerTone,
  type HostAction,
} from "../lib/host.ts";
import { useHostState } from "../lib/hostState.ts";
import type { HostServices, HostState, HostWorker, HostWorkerControl } from "../platform/index.ts";
import { useApi, usePlatform } from "../platform/context.tsx";
import { Marker } from "./Machines.tsx";
import { ServerPicker, useCanSwitchServer } from "./Servers.tsx";
import { Step, Steps } from "./Steps.tsx";
import { Alert, Button, Checkbox, Detail, Details, ErrorText, PageHeader, Panel, buttonClass, cx, CommandBox, CopyButton } from "./ui.tsx";

/** Delay before the starting screen appears, so a fast start shows no flash. */
const STARTING_DELAY_MS = 400;

/**
 * Renders the app once the active server has been ready, and the connection
 * screen before that (or while the server speaks another API). Later trouble
 * keeps the app mounted (drafts survive) and shows HostBanner. A server switch
 * starts over (StageApp drops the old server's data and opens `/`).
 */
export function HostGate({ children }: { children: ReactNode }) {
  const { host } = usePlatform();
  const state = useHostState();
  const qc = useQueryClient();
  const epoch = state?.epoch ?? 0;
  const lastEpoch = useRef(epoch);
  useEffect(() => {
    // Back after an outage: refetch now instead of waiting for each poll.
    if (epoch > lastEpoch.current && lastEpoch.current > 0) void qc.invalidateQueries();
    lastEpoch.current = epoch;
  }, [epoch, qc]);

  // The 400ms grace runs again for each server: a switch starts from the blank ground.
  const targetId = state?.target.id ?? "";
  const [late, setLate] = useState(false);
  const [lateTarget, setLateTarget] = useState(targetId);
  if (lateTarget !== targetId) {
    setLateTarget(targetId);
    setLate(false);
  }
  const startingTarget = state?.phase === "starting" && state.serverEpoch === 0 ? targetId : null;
  useEffect(() => {
    if (startingTarget === null) return;
    const t = setTimeout(() => setLate(true), STARTING_DELAY_MS);
    return () => clearTimeout(t);
  }, [startingTarget]);

  const view = host === undefined ? "app" : gateView(state, late);
  if (view === "app" || host === undefined || state === null) return children;
  if (view === "blank") return <div className="min-h-dvh bg-bg" aria-busy="true" />;
  if (state.phase === "worker" && state.worker !== null) return <WorkerScreen state={state} worker={state.worker} host={host} />;
  return <ConnectionScreen state={state} host={host} />;
}

// ---- Connection screen ----

/** Three stepped bars, the brand glyph, with the wordmark (static: no needs-input state before the app runs). */
function BrandMark() {
  return (
    <div className="flex items-center gap-2 font-bold tracking-[-0.02em]">
      <svg width={16} height={16} viewBox="0 0 16 16" aria-hidden className="shrink-0 fill-current">
        <rect x={1} y={11} width={14} height={3} />
        <rect x={3} y={7} width={10} height={3} opacity={0.72} />
        <rect x={5} y={3} width={6} height={3} opacity={0.44} />
      </svg>
      stage
    </div>
  );
}

const ACTIONS: Record<HostAction, (h: HostServices) => Promise<void>> = {
  start: (h) => h.connection.startServer(),
  restart: (h) => h.connection.restartServer(),
  retry: (h) => h.connection.retry(),
};

/** Problems, fix and remedy command the host reported. */
function Remedy({ state }: { state: HostState }) {
  const hasProblems = state.problems.length > 0;
  if (state.fix === "" && state.command === "" && !hasProblems) return null;
  return (
    <div className="flex flex-col gap-3">
      {state.fix === "" ? null : <Alert tone="bad">{state.fix}</Alert>}
      {hasProblems ? (
        <ul className="border border-line">
          {state.problems.map((p) => (
            <li key={p.code} className="flex flex-col gap-0.5 border-b border-line px-3 py-2 last:border-b-0">
              <span className="text-small text-fg">{p.message}</span>
              {p.fix === "" ? null : <span className="font-mono text-meta text-muted">{p.fix}</span>}
            </li>
          ))}
        </ul>
      ) : null}
      {state.command === "" ? null : <CommandBox command={state.command} />}
    </div>
  );
}

function Diagnostics({ host, logTail }: { host: HostServices; logTail: string[] }) {
  const [open, setOpen] = useState(false);
  const report = useQuery({
    queryKey: ["hostDiagnostics"],
    queryFn: () => host.connection.diagnostics(),
    enabled: open,
    refetchInterval: false,
  });
  const panelId = useId();
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <Button variant="ghost" small aria-expanded={open} aria-controls={panelId} onClick={() => setOpen((o) => !o)}>
          {open ? "Hide details" : "Show details"}
        </Button>
        {report.data === undefined ? null : <CopyButton text={report.data} label="Copy diagnostics" />}
      </div>
      {open ? (
        <div id={panelId} className="flex flex-col gap-2">
          {logTail.length > 0 ? (
            <Panel title="Server log" meta={`last ${logTail.length} lines`} flush>
              <pre className="max-h-72 overflow-auto px-3 py-2 font-mono text-meta leading-relaxed whitespace-pre-wrap text-fg">
                {logTail.join("\n")}
              </pre>
            </Panel>
          ) : null}
          {logTail.length === 0 && report.data !== undefined ? (
            <pre className="max-h-72 overflow-auto border border-line bg-panel px-3 py-2 font-mono text-meta leading-relaxed whitespace-pre-wrap text-fg">
              {report.data}
            </pre>
          ) : null}
          {report.isPending ? <p className="font-mono text-meta text-muted">Collecting diagnostics…</p> : null}
          <ErrorText error={report.error} />
        </div>
      ) : null}
    </div>
  );
}

/** Full-window state before the app can run: what is happening, what failed and how to fix it. */
export function ConnectionScreen({ state, host }: { state: HostState; host: HostServices }) {
  const remote = state.target.kind === "remote";
  const primary = primaryAction(state.phase, state.target.kind);
  const canSwitch = useCanSwitchServer();
  const [picker, setPicker] = useState(false);
  const [actionError, setActionError] = useState<unknown>(null);
  const run = (action: (h: HostServices) => Promise<void>) => {
    setActionError(null);
    action(host).catch((err: unknown) => setActionError(err));
  };
  const meta = state.message === "" ? `${state.app.name} ${state.app.version}` : state.message;
  return (
    <main className="min-h-dvh bg-bg text-fg">
      <div className="mx-auto flex w-full max-w-[560px] flex-col gap-3.5 px-4 pt-[max(2.5rem,14vh)] pb-12">
        <BrandMark />
        <div className="mt-6" aria-live="polite">
          <PageHeader title={state.title} meta={meta} />
        </div>
        <Panel title={remote ? state.target.name : "Stage server"} meta={<span className="break-all">{remote ? state.target.url : state.app.identity}</span>} flush>
          <Steps label="Startup steps">
            {visibleSteps(state).map((key) => {
              const status = stepStatus(state, key);
              return <Step key={key} label={key} note={stepNote(state, key, status)} status={status} />;
            })}
          </Steps>
        </Panel>
        <Remedy state={state} />
        <ErrorText error={actionError} />
        {primary === null && state.phase !== "incompatible" && !canSwitch ? null : (
          <div className="flex flex-wrap gap-2">
            {primary === null ? null : (
              <Button variant="primary" busy={state.busy} onClick={() => run(ACTIONS[primary])}>
                {ACTION_LABEL[primary]}
              </Button>
            )}
            {state.phase === "incompatible" && primary !== "retry" ? (
              <Button disabled={state.busy} onClick={() => run((h) => h.connection.retry())}>
                Retry
              </Button>
            ) : null}
            {canSwitch ? <Button onClick={() => setPicker(true)}>Switch server</Button> : null}
          </div>
        )}
        {state.phase === "starting" ? null : <Diagnostics host={host} logTail={remote ? [] : state.logTail} />}
      </div>
      {picker ? <ServerPicker onClose={() => setPicker(false)} /> : null}
    </main>
  );
}

/**
 * This machine's target while it is a worker of a remote: its own server is off. Offers the remote it serves (when
 * saved) and Leave, which brings this machine's server back.
 */
function WorkerScreen({ state, worker, host }: { state: HostState; worker: HostWorker; host: HostServices }) {
  const canSwitch = useCanSwitchServer();
  const [picker, setPicker] = useState(false);
  const [leaving, setLeaving] = useState(false);
  const [switchError, setSwitchError] = useState<unknown>(null);
  const serverId = worker.serverId;
  const control = host.worker;
  const servers = host.servers;
  return (
    <main className="min-h-dvh bg-bg text-fg">
      <div className="mx-auto flex w-full max-w-[560px] flex-col gap-3.5 px-4 pt-[max(2.5rem,14vh)] pb-12">
        <BrandMark />
        <div className="mt-6" aria-live="polite">
          <PageHeader title={state.title} meta={state.message} />
        </div>
        <Panel title="This machine" meta={state.app.identity} flush>
          <WorkerRow worker={worker} className="px-3 py-2.5" />
        </Panel>
        <ErrorText error={switchError} />
        <div className="flex flex-wrap gap-2">
          {serverId !== null && servers !== undefined ? (
            <Button
              variant="primary"
              onClick={() => {
                setSwitchError(null);
                servers.switch(serverId).catch((err: unknown) => setSwitchError(err));
              }}
            >
              Switch to {worker.serverName}
            </Button>
          ) : canSwitch ? (
            <Button variant="primary" onClick={() => setPicker(true)}>
              Switch server
            </Button>
          ) : null}
          {control === undefined ? null : (
            <Button variant="danger" disabled={leaving} onClick={() => setLeaving(true)}>
              Leave and use this machine
            </Button>
          )}
        </div>
        {leaving && control !== undefined ? <LeaveConfirm worker={worker} control={control} onThisMachine onCancel={() => setLeaving(false)} /> : null}
      </div>
      {picker ? <ServerPicker onClose={() => setPicker(false)} /> : null}
    </main>
  );
}

// ---- Worker ----

/** The worker's marker, server and status line, its main server's URL and message; actions on their own row. */
function WorkerRow({ worker, actions, className }: { worker: HostWorker; actions?: ReactNode; className?: string }) {
  return (
    <div className={cx("flex flex-col gap-2", className)}>
      <div className="flex items-start gap-3">
        <span className="flex h-6 items-center">
          <Marker tone={workerTone(worker.status)} />
        </span>
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span className="min-w-0 truncate text-body font-medium text-fg">Worker of {worker.serverName}</span>
          <p className="font-mono text-meta text-muted">
            <span className="tabular">{workerLine(worker)}</span>
          </p>
          <p className="font-mono text-meta text-muted [overflow-wrap:anywhere]">{worker.url}</p>
          {worker.message === "" ? null : <p className="mt-1 text-small text-muted [overflow-wrap:anywhere]">{worker.message}</p>}
        </div>
      </div>
      {actions === undefined ? null : <div className="flex flex-wrap items-center gap-1.5 pl-[18px]">{actions}</div>}
    </div>
  );
}

/**
 * The inline confirm before leaving (as removing a machine). After a failed leave (the server is unreachable) it
 * offers to forget the worker on this machine only.
 */
function LeaveConfirm({
  worker,
  control,
  onCancel,
  onThisMachine = false,
}: {
  worker: HostWorker;
  control: HostWorkerControl;
  onCancel: () => void;
  onThisMachine?: boolean;
}) {
  const [busy, setBusy] = useState<"server" | "local" | null>(null);
  const [error, setError] = useState<unknown>(null);
  const leave = (local: boolean) => {
    setBusy(local ? "local" : "server");
    setError(null);
    control
      .leave({ local })
      .catch((err: unknown) => setError(err))
      .finally(() => setBusy(null));
  };
  const [question, detail] = leavePrompt(worker, onThisMachine);
  return (
    <div className="flex flex-col gap-2">
      <Alert tone="bad">
        <p className="font-medium">{question}</p>
        <p className="mt-1">{detail}</p>
        <div className="mt-2 flex flex-wrap gap-2">
          <Button small variant="danger" busy={busy === "server"} disabled={busy !== null} onClick={() => leave(false)}>
            Leave {worker.serverName}
          </Button>
          {error === null ? null : (
            <Button small busy={busy === "local"} disabled={busy !== null} onClick={() => leave(true)}>
              Forget it on this machine only
            </Button>
          )}
          <Button small variant="ghost" autoFocus disabled={busy !== null} onClick={onCancel}>
            Cancel
          </Button>
        </div>
      </Alert>
      <ErrorText error={error} />
    </div>
  );
}

/** Restart worker as a small button with its own busy state and error. */
function useRestartWorker(control: HostWorkerControl | undefined) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const restart = () => {
    if (control === undefined) return;
    setBusy(true);
    setError(null);
    control
      .restart()
      .catch((err: unknown) => setError(err))
      .finally(() => setBusy(false));
  };
  return { busy, error, restart };
}

/**
 * Settings > Desktop app: join the active remote as a worker, or this machine's worker state with Set up harnesses,
 * Restart and Leave. Joining creates a pairing on the active server (as Settings > Machines > Add does), then the
 * host joins with its link; the code is revoked when the join fails.
 */
function WorkerSection({ state, control }: { state: HostState; control: HostWorkerControl }) {
  const worker = state.worker;
  if (worker !== null) {
    // Keyed by the join, so a confirm left open never carries over to a later join.
    return <JoinedWorker key={`${worker.url} ${worker.machineId}`} worker={worker} ofActive={activeWorker(state) !== null} control={control} />;
  }
  return state.target.kind === "remote" ? <JoinRemote serverName={state.target.name} control={control} /> : null;
}

function JoinRemote({ serverName, control }: { serverName: string; control: HostWorkerControl }) {
  const api = useApi();
  const qc = useQueryClient();
  const [joining, setJoining] = useState(false);
  const [joinError, setJoinError] = useState<unknown>(null);
  const join = async () => {
    setJoining(true);
    setJoinError(null);
    try {
      const pairing = await api.createPairing("");
      try {
        await control.join(pairing.link);
      } catch (err) {
        api.revokePairing(pairing.id).catch(() => undefined);
        throw err;
      }
      void qc.invalidateQueries({ queryKey: keys.machines });
    } catch (err) {
      setJoinError(err);
    } finally {
      setJoining(false);
    }
  };
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2 border-y border-line py-row">
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span className="text-small text-fg">Run {serverName}'s tasks on this machine</span>
          <span className="font-mono text-meta text-muted">its harnesses join as a worker; this machine's own server stops, its data stays</span>
        </div>
        <Button small busy={joining} onClick={() => void join()}>
          Join as worker
        </Button>
      </div>
      <ErrorText error={joinError} />
    </div>
  );
}

function JoinedWorker({ worker, ofActive, control }: { worker: HostWorker; ofActive: boolean; control: HostWorkerControl }) {
  const [leaving, setLeaving] = useState(false);
  const restart = useRestartWorker(control);
  return (
    <div className="flex flex-col gap-2">
      <div className="border-y border-line">
        <WorkerRow
          worker={worker}
          className="py-2.5"
          actions={
            <>
              {canSetUpWorker(worker, ofActive) ? (
                <Link to={`/settings/machines/${worker.machineId}`} className={buttonClass("secondary", true)}>
                  Set up harnesses
                </Link>
              ) : null}
              {canRestartWorker(worker) ? (
                <Button small busy={restart.busy} onClick={restart.restart}>
                  Restart worker
                </Button>
              ) : null}
              <Button small variant="danger" disabled={leaving} onClick={() => setLeaving(true)}>
                Leave
              </Button>
            </>
          }
        />
      </div>
      <ErrorText error={restart.error} />
      {leaving ? <LeaveConfirm worker={worker} control={control} onCancel={() => setLeaving(false)} /> : null}
    </div>
  );
}

// ---- Banner (after the app first ran) ----

/** Connection trouble and server notes above the page while the app stays usable. */
export function HostBanner() {
  const { host } = usePlatform();
  const state = useHostState();
  const canSwitch = useCanSwitchServer();
  const [picker, setPicker] = useState(false);
  const [dismissed, setDismissed] = useState<ReadonlySet<string>>(new Set());
  const [actionError, setActionError] = useState<unknown>(null);
  if (host === undefined || state === null || state.serverEpoch === 0) return null;
  const remote = state.target.kind === "remote";
  const switchButton =
    remote && canSwitch ? (
      <Button small variant="ghost" onClick={() => setPicker(true)}>
        Switch server
      </Button>
    ) : null;
  const run = (action: (h: HostServices) => Promise<void>) => {
    setActionError(null);
    action(host).catch((err: unknown) => setActionError(err));
  };
  const dismiss = (key: string) => setDismissed((d) => new Set(d).add(key));
  const items: ReactNode[] = [];

  if (state.phase === "reconnecting") {
    items.push(
      <Alert
        key="conn"
        tone="plain"
        actions={
          <>
            <Button small onClick={() => run((h) => h.connection.retry())}>
              Retry now
            </Button>
            {switchButton}
          </>
        }
      >
        <span className="font-medium">{state.title}</span>
        {state.message === "" ? null : <span className="text-muted"> {state.message}</span>}
      </Alert>,
    );
  } else if (state.phase !== "ready" && state.phase !== "starting") {
    const primary = primaryAction(state.phase, state.target.kind);
    items.push(
      <Alert
        key="conn"
        tone="bad"
        actions={
          primary === null && switchButton === null ? undefined : (
            <>
              {primary === null ? null : (
                <Button small busy={state.busy} onClick={() => run(ACTIONS[primary])}>
                  {ACTION_LABEL[primary]}
                </Button>
              )}
              {switchButton}
            </>
          )
        }
      >
        <span className="font-medium">{state.title}</span>
        {state.message === "" ? null : <span> {state.message}</span>}
        {state.fix === "" ? null : <span className="block font-mono text-meta">{state.fix}</span>}
        {state.command === "" ? null : <code className="mt-1 block font-mono text-meta break-all">{state.command}</code>}
      </Alert>,
    );
  }

  if (state.phase === "ready" && !remote && state.foreground && !dismissed.has("foreground")) {
    const pid = state.server?.pid ?? null;
    items.push(
      <Alert key="foreground" tone="plain" actions={<Button small variant="ghost" onClick={() => dismiss("foreground")}>Dismiss</Button>}>
        Connected to a Stage server started from a terminal{pid === null ? "" : ` (pid ${pid})`}. It stops when that terminal does.
      </Alert>,
    );
  }

  const worker = activeWorker(state);
  const bannerTone = workerBanner(worker);
  if (state.phase === "ready" && worker !== null && bannerTone !== null) {
    items.push(<WorkerAlert key="worker" worker={worker} tone={bannerTone} control={host.worker} />);
  }

  const skewKey = `skew:${state.server?.version ?? ""}`;
  if (state.phase === "ready" && !remote && state.versionSkew && !state.foreground && !dismissed.has(skewKey)) {
    items.push(
      <Alert
        key="skew"
        tone="plain"
        actions={
          <>
            <Button small busy={state.busy} onClick={() => run((h) => h.connection.restartServer())}>
              Restart Stage server
            </Button>
            <Button small variant="ghost" onClick={() => dismiss(skewKey)}>
              Dismiss
            </Button>
          </>
        }
      >
        Stage server {state.server?.version ?? "unknown"} differs from this app's {state.app.version}. Restart it to run the app's version.
      </Alert>,
    );
  }

  if (items.length === 0 && actionError === null) return null;
  return (
    <div className="mb-3.5 flex flex-col gap-2" role="status">
      {items}
      <ErrorText error={actionError} />
      {picker ? <ServerPicker onClose={() => setPicker(false)} /> : null}
    </div>
  );
}

/** The active server's worker in trouble: what happened, its fix, and Restart worker when that helps. */
function WorkerAlert({ worker, tone, control }: { worker: HostWorker; tone: "plain" | "bad"; control: HostWorkerControl | undefined }) {
  const restart = useRestartWorker(control);
  return (
    <>
      <Alert
        tone={tone}
        actions={
          <>
            {control !== undefined && canRestartWorker(worker) ? (
              <Button small busy={restart.busy} onClick={restart.restart}>
                Restart worker
              </Button>
            ) : null}
            <Link to="/settings" className={buttonClass("ghost", true)}>
              Settings
            </Link>
          </>
        }
      >
        <span className="font-medium">{workerHeadline(worker)}</span>
        {worker.message === "" ? null : <span> {worker.message}</span>}
        {worker.fix === "" ? null : <span className="block text-meta">{worker.fix}</span>}
      </Alert>
      <ErrorText error={restart.error} />
    </>
  );
}

// ---- Settings ----

const LOGIN_KEY = ["hostLoginStartup"];

/** Settings panel of the desktop app: login startup (this machine's server or worker), the worker, versions, diagnostics. */
export function DesktopAppCard() {
  const { host } = usePlatform();
  const state = useHostState();
  const qc = useQueryClient();
  const login = useQuery({
    queryKey: LOGIN_KEY,
    queryFn: () => host?.loginStartup.get() ?? Promise.resolve(false),
    enabled: host !== undefined,
    refetchInterval: false,
  });
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);
  const diagnostics = useQuery({
    queryKey: ["hostDiagnostics", "settings"],
    queryFn: () => host?.connection.diagnostics() ?? Promise.resolve(""),
    enabled: host !== undefined,
    refetchInterval: false,
  });
  if (host === undefined || state === null) return null;
  const toggle = (enabled: boolean) => {
    setSaving(true);
    setSaveError(null);
    qc.setQueryData(LOGIN_KEY, enabled);
    host.loginStartup
      .set(enabled)
      .catch((err: unknown) => setSaveError(err))
      .finally(() => {
        setSaving(false);
        void qc.invalidateQueries({ queryKey: LOGIN_KEY });
      });
  };
  const server = state.server;
  const remote = state.target.kind === "remote";
  const serverLine =
    server === null || server.id !== state.target.id
      ? state.target.name
      : `${state.target.name} · ${server.version}${remote ? "" : ` · ${server.managed ? "per-user service" : "started from a terminal"}`}`;
  const joined = state.worker !== null;
  return (
    <Panel title="Desktop app" meta={`${state.app.name} ${state.app.version}`}>
      <div className="flex flex-col gap-4">
        {remote && !joined ? null : (
          <label className="flex min-h-11 cursor-pointer items-center gap-3 border-y border-line transition-colors duration-150 hover:bg-panel md:min-h-8">
            <Checkbox
              checked={login.data === true}
              disabled={login.isPending || saving}
              onChange={(e) => toggle(e.target.checked)}
            />
            <span className="text-small text-fg">Open Stage at login</span>
            <span className="ml-auto font-mono text-meta text-muted">{joined ? "starts hidden, with the worker" : "starts hidden, with the server"}</span>
          </label>
        )}
        <ErrorText error={saveError ?? login.error} />
        {host.worker === undefined ? null : <WorkerSection state={state} control={host.worker} />}
        <Details>
          <Detail label="App">{`${state.app.name} ${state.app.version}`}</Detail>
          <Detail label="Server">{serverLine}</Detail>
          {remote ? <Detail label="URL">{state.target.url}</Detail> : <Detail label="Socket">{server?.socket ?? "-"}</Detail>}
          <Detail label="Identity">{state.app.identity}</Detail>
        </Details>
        {diagnostics.data === undefined || diagnostics.data === "" ? null : (
          <div>
            <CopyButton text={diagnostics.data} label="Copy diagnostics" small={false} />
          </div>
        )}
      </div>
    </Panel>
  );
}
