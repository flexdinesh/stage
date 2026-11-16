// Host-managed connection UI (desktop): the connection screen shown until the
// Stage server first answers, the banner for later trouble, and the Settings
// panel for the desktop app. A host without `platform.host` (the browser)
// renders none of it.
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useId, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { ACTION_LABEL, STEPS, gateView, primaryAction, stepNote, stepStatus, type HostAction, type StepStatus } from "../lib/host.ts";
import type { HostServices, HostState } from "../platform/index.ts";
import { usePlatform } from "../platform/context.tsx";
import { useCopy } from "./StageLinks.tsx";
import { Alert, Button, Checkbox, Detail, Details, ErrorText, PageHeader, Panel, cx, toneClass, type ChipTone } from "./ui.tsx";

const unsubscribed = () => {};

/** The host's connection state; null without a host-managed connection. */
export function useHostState(): HostState | null {
  const { host } = usePlatform();
  const subscribe = useCallback(
    (listener: () => void) => (host === undefined ? unsubscribed : host.connection.subscribe(listener)),
    [host],
  );
  const snapshot = useCallback(() => (host === undefined ? null : host.connection.current()), [host]);
  return useSyncExternalStore(subscribe, snapshot);
}

/** Increments each time the host's connection becomes ready again; 0 without a host. Live streams reopen on change. */
export function useHostEpoch(): number {
  return useHostState()?.epoch ?? 0;
}

/** Delay before the starting screen appears, so a fast start shows no flash. */
const STARTING_DELAY_MS = 400;

/**
 * Renders the app once the host's connection has been ready, and the
 * connection screen before that (or while the server speaks another API).
 * Later trouble keeps the app mounted (drafts survive) and shows HostBanner.
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

  const [late, setLate] = useState(false);
  const starting = state?.phase === "starting" && epoch === 0;
  useEffect(() => {
    if (!starting) return;
    const t = setTimeout(() => setLate(true), STARTING_DELAY_MS);
    return () => clearTimeout(t);
  }, [starting]);

  const view = host === undefined ? "app" : gateView(state, late);
  if (view === "app" || host === undefined || state === null) return children;
  if (view === "blank") return <div className="min-h-dvh bg-bg" aria-busy="true" />;
  return <ConnectionScreen state={state} host={host} />;
}

// ---- Connection screen ----

const STEP_TONE: Record<StepStatus, ChipTone> = { idle: "idle", active: "run", ok: "ok", bad: "bad" };

function Step({ label, note, status }: { label: string; note: string; status: StepStatus }) {
  return (
    <li className="flex min-h-10 items-center gap-3 border-b border-line px-3 last:border-b-0 md:min-h-8">
      <span aria-hidden className="flex size-2 shrink-0 items-center justify-center">
        {status === "active" ? (
          <span className="size-2 animate-spin border-[1.5px] border-run border-t-transparent motion-reduce:animate-none" />
        ) : (
          <span className={cx("size-1.5 bg-(--c) transition-colors duration-150", toneClass(STEP_TONE[status]))} />
        )}
      </span>
      <span className="font-mono text-meta text-fg">{label}</span>
      <span className={cx("ml-auto truncate font-mono text-meta", status === "bad" ? "text-bad" : "text-muted")}>{note}</span>
    </li>
  );
}

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

function CopyButton({ text, label, small = true }: { text: string; label: string; small?: boolean }) {
  const [status, copy] = useCopy();
  return (
    <>
      <Button small={small} onClick={() => copy(text)}>
        {status === "copied" ? "Copied" : status === "failed" ? "Copy failed" : label}
      </Button>
      <span className="sr-only" aria-live="polite">
        {status === "copied" ? `${label}: copied` : status === "failed" ? `${label}: copy failed` : ""}
      </span>
    </>
  );
}

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
      {state.command === "" ? null : (
        <div className="flex items-start gap-2 border border-line bg-panel px-3 py-2">
          <code className="min-w-0 flex-1 font-mono text-detail break-all text-fg">{state.command}</code>
          <CopyButton text={state.command} label="Copy" />
        </div>
      )}
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
  const primary = primaryAction(state.phase);
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
        <Panel title="Stage server" meta={state.app.identity} flush>
          <ol aria-label="Startup steps">
            {STEPS.map((key) => {
              const status = stepStatus(state, key);
              return <Step key={key} label={key} note={stepNote(state, key, status)} status={status} />;
            })}
          </ol>
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
            {state.phase === "incompatible" ? (
              <Button disabled={state.busy} onClick={() => run((h) => h.connection.retry())}>
                Retry
              </Button>
            ) : null}
          </div>
        )}
        {state.phase === "starting" ? null : <Diagnostics host={host} logTail={state.logTail} />}
      </div>
    </main>
  );
}

// ---- Banner (after the app first ran) ----

/** Connection trouble and server notes above the page while the app stays usable. */
export function HostBanner() {
  const { host } = usePlatform();
  const state = useHostState();
  const [dismissed, setDismissed] = useState<ReadonlySet<string>>(new Set());
  const [actionError, setActionError] = useState<unknown>(null);
  if (host === undefined || state === null || state.epoch === 0) return null;
  const run = (action: (h: HostServices) => Promise<void>) => {
    setActionError(null);
    action(host).catch((err: unknown) => setActionError(err));
  };
  const dismiss = (key: string) => setDismissed((d) => new Set(d).add(key));
  const items: ReactNode[] = [];

  if (state.phase === "reconnecting") {
    items.push(
      <Alert key="conn" tone="plain" actions={<Button small onClick={() => run((h) => h.connection.retry())}>Retry now</Button>}>
        <span className="font-medium">{state.title}</span>
        {state.message === "" ? null : <span className="text-muted"> {state.message}</span>}
      </Alert>,
    );
  } else if (state.phase !== "ready" && state.phase !== "starting") {
    const primary = primaryAction(state.phase);
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
      </Alert>,
    );
  }

  if (state.phase === "ready" && state.foreground && !dismissed.has("foreground")) {
    const pid = state.server?.pid ?? null;
    items.push(
      <Alert key="foreground" tone="plain" actions={<Button small variant="ghost" onClick={() => dismiss("foreground")}>Dismiss</Button>}>
        Connected to a Stage server started from a terminal{pid === null ? "" : ` (pid ${pid})`}. It stops when that terminal does.
      </Alert>,
    );
  }

  const skewKey = `skew:${state.server?.version ?? ""}`;
  if (state.phase === "ready" && state.versionSkew && !state.foreground && !dismissed.has(skewKey)) {
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

// ---- Settings ----

const LOGIN_KEY = ["hostLoginStartup"];

/** Settings panel of the desktop app: login startup, versions, diagnostics. */
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
  return (
    <Panel title="Desktop app" meta={`${state.app.name} ${state.app.version}`}>
      <div className="flex flex-col gap-4">
        <label className="flex min-h-11 cursor-pointer items-center gap-3 border-y border-line transition-colors duration-150 hover:bg-panel md:min-h-8">
          <Checkbox
            checked={login.data === true}
            disabled={login.isPending || saving}
            onChange={(e) => toggle(e.target.checked)}
          />
          <span className="text-small text-fg">Open Stage at login</span>
          <span className="ml-auto font-mono text-meta text-muted">starts hidden, with the server</span>
        </label>
        <ErrorText error={saveError ?? login.error} />
        <Details>
          <Detail label="App">{`${state.app.name} ${state.app.version}`}</Detail>
          <Detail label="Server">
            {server === null ? "-" : `${server.version} · ${server.managed ? "per-user service" : "started from a terminal"}`}
          </Detail>
          <Detail label="Socket">{server?.socket ?? "-"}</Detail>
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
