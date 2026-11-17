// Host-managed connection UI (desktop): the connection screen shown until the
// active Stage server first answers, the banner for later trouble, and the
// Settings panel for the desktop app. A host without `platform.host` (the
// browser) renders none of it. Server switching: Servers.tsx.
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { ACTION_LABEL, gateView, primaryAction, stepNote, stepStatus, visibleSteps, type HostAction } from "../lib/host.ts";
import { useHostState } from "../lib/hostState.ts";
import type { HostServices, HostState } from "../platform/index.ts";
import { usePlatform } from "../platform/context.tsx";
import { ServerPicker, useCanSwitchServer } from "./Servers.tsx";
import { CommandBox, CopyAction, Step, Steps } from "./Steps.tsx";
import { Alert, Button, Checkbox, Detail, Details, ErrorText, PageHeader, Panel } from "./ui.tsx";

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
        {report.data === undefined ? null : <CopyAction text={report.data} label="Copy diagnostics" />}
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

// ---- Settings ----

const LOGIN_KEY = ["hostLoginStartup"];

/** Settings panel of the desktop app: login startup (this machine's server only), versions, diagnostics. */
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
  return (
    <Panel title="Desktop app" meta={`${state.app.name} ${state.app.version}`}>
      <div className="flex flex-col gap-4">
        {remote ? null : (
          <label className="flex min-h-11 cursor-pointer items-center gap-3 border-y border-line transition-colors duration-150 hover:bg-panel md:min-h-8">
            <Checkbox
              checked={login.data === true}
              disabled={login.isPending || saving}
              onChange={(e) => toggle(e.target.checked)}
            />
            <span className="text-small text-fg">Open Stage at login</span>
            <span className="ml-auto font-mono text-meta text-muted">starts hidden, with the server</span>
          </label>
        )}
        <ErrorText error={saveError ?? login.error} />
        <Details>
          <Detail label="App">{`${state.app.name} ${state.app.version}`}</Detail>
          <Detail label="Server">{serverLine}</Detail>
          {remote ? <Detail label="URL">{state.target.url}</Detail> : <Detail label="Socket">{server?.socket ?? "-"}</Detail>}
          <Detail label="Identity">{state.app.identity}</Detail>
        </Details>
        {diagnostics.data === undefined || diagnostics.data === "" ? null : (
          <div>
            <CopyAction text={diagnostics.data} label="Copy diagnostics" small={false} />
          </div>
        )}
      </div>
    </Panel>
  );
}
