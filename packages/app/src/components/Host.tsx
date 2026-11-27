// Host-managed connection UI (desktop): the connection screen shown until the
// active Stage server first answers, the banner for later trouble, and the
// desktop app's lines and controls in Settings > General > Info (login
// startup, this machine as a worker of another server: state, leave). A host
// without `platform.host` (the browser) renders none of it. Server switching
// (Servers.tsx) is kept but not offered in the UI.
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { Link } from "react-router";
import { keys } from "../api/queries.ts";
import {
  ACTION_LABEL,
  activeWorker,
  canRestartWorker,
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
import { usePlatform } from "../platform/context.tsx";
import { Step, Steps } from "./Steps.tsx";
import { DesktopAccessGate } from "./DesktopAccess.tsx";
import {
  Alert,
  Button,
  Checkbox,
  ChoiceRow,
  ConfirmInline,
  ErrorText,
  Hint,
  Marker,
  PageHeader,
  Panel,
  Row,
  RowList,
  buttonClass,
  cx,
  CommandBox,
  CopyButton,
} from "./ui.tsx";

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
  if (view === "app" || host === undefined || state === null) {
    if (state?.server?.sharedAccess === true) {
      return <DesktopAccessGate key={state.accountEpoch} state={state} brand={<BrandMark />}>{children}</DesktopAccessGate>;
    }
    return children;
  }
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
        <RowList className="border border-line">
          {state.problems.map((p) => (
            <Row key={p.code} className="flex flex-col gap-0.5">
              <span className="text-small text-fg">{p.message}</span>
              {p.fix === "" ? null : <Hint>{p.fix}</Hint>}
            </Row>
          ))}
        </RowList>
      ) : null}
      {state.command === "" ? null : <CommandBox command={state.command} />}
    </div>
  );
}

function Diagnostics({ host, logTail }: { host: HostServices; logTail: string[] }) {
  const [open, setOpen] = useState(false);
  const report = useQuery({
    queryKey: keys.host.diagnostics,
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
          {report.isPending ? <Hint>Collecting diagnostics…</Hint> : null}
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
        {primary === null && state.phase !== "incompatible" ? null : (
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
          </div>
        )}
        {state.phase === "starting" ? null : <Diagnostics host={host} logTail={remote ? [] : state.logTail} />}
      </div>
    </main>
  );
}

/**
 * This machine's target while it is a worker of a remote: its own server is off. Offers Leave, which brings this
 * machine's server back (server switching is not offered in the UI).
 */
function WorkerScreen({ state, worker, host }: { state: HostState; worker: HostWorker; host: HostServices }) {
  const [leaving, setLeaving] = useState(false);
  const control = host.worker;
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
        {control === undefined ? null : (
          <div className="flex flex-wrap gap-2">
            <Button variant="danger" disabled={leaving} onClick={() => setLeaving(true)}>
              Leave and use this machine
            </Button>
          </div>
        )}
        {leaving && control !== undefined ? <LeaveConfirm worker={worker} control={control} onThisMachine onCancel={() => setLeaving(false)} /> : null}
      </div>
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
    <ConfirmInline
      danger
      confirmLabel={`Leave ${worker.serverName}`}
      busy={busy === "server"}
      disabled={busy !== null}
      onConfirm={() => leave(false)}
      onCancel={onCancel}
      error={error}
      alternative={
        error === null ? null : (
          <Button small busy={busy === "local"} disabled={busy !== null} onClick={() => leave(true)}>
            Forget it on this machine only
          </Button>
        )
      }
    >
      <p className="font-medium">{question}</p>
      <p className="mt-1">{detail}</p>
    </ConfirmInline>
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
 * Info: this machine as a worker of another server (desktop apps are expected to be the main machine; joining is not
 * offered in the UI), with Restart and Leave.
 */
function JoinedWorker({ worker, control }: { worker: HostWorker; control: HostWorkerControl }) {
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
  const [dismissed, setDismissed] = useState<ReadonlySet<string>>(new Set());
  const [actionError, setActionError] = useState<unknown>(null);
  if (host === undefined || state === null || state.serverEpoch === 0) return null;
  const remote = state.target.kind === "remote";
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
          <Button small onClick={() => run((h) => h.connection.retry())}>
            Retry now
          </Button>
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
          primary === null ? undefined : (
            <Button small busy={state.busy} onClick={() => run(ACTIONS[primary])}>
              {ACTION_LABEL[primary]}
            </Button>
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

/** The desktop app's lines in Settings > General > Info; null outside the desktop app. */
export interface DesktopInfo {
  /** "Stage 0.9.4". */
  app: string;
  /** How this machine's server runs ("per-user service", "started from a terminal"); null for a remote. */
  runs: string | null;
  /** The socket it is reached over, or a remote's URL. */
  where: { label: string; value: string };
  /** Diagnostics text for Copy diagnostics; null until collected. */
  diagnostics: string | null;
}

export function useDesktopInfo(): DesktopInfo | null {
  const { host } = usePlatform();
  const state = useHostState();
  const diagnostics = useQuery({
    queryKey: [...keys.host.diagnostics, "settings"],
    queryFn: () => host?.connection.diagnostics() ?? Promise.resolve(""),
    enabled: host !== undefined,
    refetchInterval: false,
    // Collected per Settings visit for the Copy button, not on every window focus.
    refetchOnWindowFocus: false,
  });
  if (host === undefined || state === null) return null;
  const server = state.server;
  const remote = state.target.kind === "remote";
  const current = server !== null && server.id === state.target.id;
  return {
    app: `${state.app.name} ${state.app.version}`,
    runs: remote || !current ? null : server.managed ? "per-user service" : "started from a terminal",
    where: remote ? { label: "URL", value: state.target.url } : { label: "Socket", value: server?.socket ?? "-" },
    diagnostics: diagnostics.data === undefined || diagnostics.data === "" ? null : diagnostics.data,
  };
}

/** The desktop app's controls in Info: Open Stage at login, and this machine's worker when it joined another server. */
export function DesktopControls() {
  const { host } = usePlatform();
  const state = useHostState();
  const qc = useQueryClient();
  const login = useQuery({
    queryKey: keys.host.loginStartup,
    queryFn: () => host?.loginStartup.get() ?? Promise.resolve(false),
    enabled: host !== undefined,
    refetchInterval: false,
  });
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);
  if (host === undefined || state === null) return null;
  const toggle = (enabled: boolean) => {
    setSaving(true);
    setSaveError(null);
    qc.setQueryData(keys.host.loginStartup, enabled);
    host.loginStartup
      .set(enabled)
      .catch((err: unknown) => setSaveError(err))
      .finally(() => {
        setSaving(false);
        void qc.invalidateQueries({ queryKey: keys.host.loginStartup });
      });
  };
  const remote = state.target.kind === "remote";
  const worker = state.worker;
  return (
    <>
      {remote && worker === null ? null : (
        <ChoiceRow
          control={<Checkbox checked={login.data === true} disabled={login.isPending || saving} onChange={(e) => toggle(e.target.checked)} />}
          detail={worker === null ? "starts hidden, with Stage" : "starts hidden, with the worker"}
        >
          Open Stage at login
        </ChoiceRow>
      )}
      <ErrorText error={saveError ?? login.error} />
      {host.worker === undefined || worker === null ? null : (
        // Keyed by the join, so a confirm left open never carries over to a later join.
        <JoinedWorker key={`${worker.url} ${worker.machineId}`} worker={worker} control={host.worker} />
      )}
    </>
  );
}
