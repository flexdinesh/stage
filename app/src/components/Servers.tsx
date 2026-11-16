// Server switching on desktop (platform.host.servers): the Settings SERVERS panel and its Add/Edit drawer, the
// top-bar server menu (more than one server saved) and the Switch server drawer of the connection screen and
// banner. A host without server switching (the browser) renders none of it. Logic: lib/host.ts.
import { useEffect, useId, useRef, useState, type FormEvent } from "react";
import { Link } from "react-router";
import { serverStatus, serverTestSteps, serverWhere, type ServerTone } from "../lib/host.ts";
import { useHostServers, useHostState } from "../lib/hostState.ts";
import { usePlatform } from "../platform/context.tsx";
import type { HostServerEntry, HostServers, HostServerTest, HostState } from "../platform/index.ts";
import { IconCheck, IconChevronDown } from "./icons.tsx";
import { CommandBox, Step, Steps } from "./Steps.tsx";
import { Alert, Button, Chip, Drawer, ErrorText, Field, Input, Muted, Panel, buttonClass, cx, toneClass } from "./ui.tsx";

/** 6px square state marker in the server's tone. */
function Marker({ tone }: { tone: ServerTone }) {
  return <span aria-hidden className={cx("size-1.5 shrink-0 bg-(--c) transition-colors duration-150", toneClass(tone))} />;
}

/** Needs-input count of a server, in the Dashboard tab's count chip. */
function NeedCount({ n }: { n: number | null }) {
  if (n === null || n === 0) return null;
  return (
    <span title={`${n} need input`} className="chip tone-need tabular px-1.5 text-meta-sm leading-4">
      {n}
      <span className="sr-only"> need input</span>
    </span>
  );
}

/** Ruled list of servers to pick from: marker, name, where, needs-input count, a check on the active one. */
function ServerChoices({ servers, state, onPick }: { servers: HostServerEntry[]; state: HostState | null; onPick: (entry: HostServerEntry) => void }) {
  return (
    <ul>
      {servers.map((s) => {
        const status = serverStatus(s, state);
        return (
          <li key={s.id} className="border-b border-line last:border-b-0">
            <button
              type="button"
              aria-current={s.active || undefined}
              onClick={() => onPick(s)}
              className="flex min-h-11 w-full items-center gap-3 px-3 py-2 text-left transition-colors duration-150 hover:bg-panel md:min-h-10 md:py-1.5"
            >
              <Marker tone={status.tone} />
              <span className="min-w-0 flex-1">
                <span className="block truncate text-small font-medium text-fg">{s.name}</span>
                <span className="block truncate font-mono text-meta text-muted">
                  {serverWhere(s, state)}
                  {status.note === "" || status.tone === "ok" ? null : <span className={status.tone === "bad" ? "text-bad" : undefined}> · {status.note}</span>}
                </span>
              </span>
              <NeedCount n={s.needsInput} />
              <span className="flex size-4 shrink-0 items-center justify-center text-fg">
                {s.active ? (
                  <>
                    <IconCheck size={14} />
                    <span className="sr-only">active</span>
                  </>
                ) : null}
              </span>
            </button>
          </li>
        );
      })}
    </ul>
  );
}

/** The servers to switch to, when the host has switching and more than one is saved; null otherwise. */
function useSwitchable(): { api: HostServers; servers: HostServerEntry[] } | null {
  const api = usePlatform().host?.servers;
  const { servers } = useHostServers();
  if (api === undefined || servers === null || servers.length < 2) return null;
  return { api, servers };
}

/** Whether the host can switch to another saved server (connection screen, banner). */
export function useCanSwitchServer(): boolean {
  return useSwitchable() !== null;
}

/** Drawer listing the saved servers; picking one switches to it. */
export function ServerPicker({ onClose }: { onClose: () => void }) {
  const api = usePlatform().host?.servers;
  const state = useHostState();
  const { servers, error } = useHostServers();
  const [switchError, setSwitchError] = useState<unknown>(null);
  const pick = (entry: HostServerEntry) => {
    if (api === undefined || entry.active) {
      onClose();
      return;
    }
    setSwitchError(null);
    api.switch(entry.id).then(onClose, (err: unknown) => setSwitchError(err));
  };
  return (
    <Drawer title="Switch server" onClose={onClose}>
      <div className="flex flex-col gap-3">
        <Muted>Stage shows one server at a time. Notifications cover every saved server.</Muted>
        {servers === null ? null : (
          <div className="border border-line">
            <ServerChoices servers={servers} state={state} onPick={pick} />
          </div>
        )}
        <ErrorText error={switchError ?? error} />
      </div>
    </Drawer>
  );
}

const UNKNOWN: { tone: ServerTone; note: string } = { tone: "idle", note: "" };

/** Top-bar menu button naming the active server (only with more than one saved): pick a server or manage them. */
export function ServerMenu({ className }: { className?: string }) {
  const state = useHostState();
  const switchable = useSwitchable();
  const [open, setOpen] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const button = useRef<HTMLButtonElement>(null);
  const wrap = useRef<HTMLDivElement>(null);
  const panelId = useId();

  useEffect(() => {
    if (!open) return;
    function onKey(e: KeyboardEvent) {
      if (e.key !== "Escape") return;
      e.stopPropagation();
      setOpen(false);
      button.current?.focus();
    }
    function onPointer(e: PointerEvent) {
      if (e.target instanceof Node && wrap.current?.contains(e.target) === true) return;
      setOpen(false);
    }
    document.addEventListener("keydown", onKey);
    document.addEventListener("pointerdown", onPointer);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("pointerdown", onPointer);
    };
  }, [open]);

  if (switchable === null || state === null) return null;
  const { api, servers } = switchable;
  const active = servers.find((s) => s.active);
  const status = active === undefined ? UNKNOWN : serverStatus(active, state);
  const close = () => {
    setOpen(false);
    setError(null);
  };
  const pick = (entry: HostServerEntry) => {
    if (entry.active) {
      close();
      return;
    }
    setError(null);
    api.switch(entry.id).then(close, (err: unknown) => setError(err));
  };
  return (
    <div
      ref={wrap}
      className={cx("relative min-w-0", className)}
      onBlur={(e) => {
        // Focus moved elsewhere (Tab). A click leaves no related target (WebKit does not focus clicked buttons), so
        // outside clicks are the pointer handler's.
        if (e.relatedTarget instanceof Node && wrap.current?.contains(e.relatedTarget) !== true) setOpen(false);
      }}
    >
      <button
        ref={button}
        type="button"
        aria-expanded={open}
        aria-controls={panelId}
        title={`Server: ${state.target.name}${status.note === "" ? "" : ` (${status.note})`}`}
        onClick={() => setOpen((o) => !o)}
        className={cx(buttonClass("secondary"), "w-full max-w-[11rem] gap-1.5 px-2 font-mono text-meta font-normal max-md:max-w-[7.5rem] md:gap-2 md:px-2.5")}
      >
        <Marker tone={status.tone} />
        <span className="sr-only">Server: </span>
        <span className="min-w-0 truncate">{state.target.name}</span>
        <span className="flex shrink-0 max-md:hidden">
          <IconChevronDown size={12} />
        </span>
      </button>
      {open ? (
        <div
          id={panelId}
          className="z-50 border border-line-strong bg-surface text-fg max-md:fixed max-md:inset-x-3 max-md:top-[calc(3.25rem+env(safe-area-inset-top)+0.25rem)] md:absolute md:top-full md:right-0 md:mt-1 md:w-[320px]"
        >
          <div className="flex min-h-8 items-center gap-2 border-b border-line bg-panel px-3 py-1">
            <span className="label-caps text-fg">Servers</span>
            <span className="font-mono text-meta-sm text-muted">{servers.length}</span>
          </div>
          <ServerChoices servers={servers} state={state} onPick={pick} />
          {error === null ? null : (
            <div className="border-t border-line p-2">
              <ErrorText error={error} />
            </div>
          )}
          <Link
            to="/settings"
            onClick={close}
            className="flex min-h-11 items-center border-t border-line px-3 text-small text-fg transition-colors duration-150 hover:bg-panel md:min-h-9"
          >
            Manage servers
          </Link>
        </div>
      ) : null}
    </div>
  );
}

// ---- Settings ----

type DrawerState = { mode: "add" } | { mode: "edit"; entry: HostServerEntry };

/** Settings SERVERS panel (desktop with server switching only). */
export function ServersCard() {
  const api = usePlatform().host?.servers;
  const state = useHostState();
  const { servers, error } = useHostServers();
  const [drawer, setDrawer] = useState<DrawerState | null>(null);
  if (api === undefined) return null;
  return (
    <>
      <Panel
        title="Servers"
        meta={servers === null ? undefined : `${servers.length} saved`}
        actions={
          <Button small onClick={() => setDrawer({ mode: "add" })}>
            Add server
          </Button>
        }
        flush
      >
        {servers === null ? (
          <div className="p-3">{error === null ? <div aria-busy="true" className="h-9 animate-pulse border border-line bg-panel" /> : <ErrorText error={error} />}</div>
        ) : (
          <ul>
            {servers.map((s) => (
              <ServerRow key={s.id} api={api} entry={s} state={state} onEdit={() => setDrawer({ mode: "edit", entry: s })} />
            ))}
          </ul>
        )}
        <p className="border-t border-line px-3 py-2 font-mono text-meta text-muted">One server at a time. Remotes: trusted network, no sign-in.</p>
      </Panel>
      {drawer === null ? null : (
        <ServerDrawer api={api} entry={drawer.mode === "edit" ? drawer.entry : null} onClose={() => setDrawer(null)} />
      )}
    </>
  );
}

function ServerRow({ api, entry, state, onEdit }: { api: HostServers; entry: HostServerEntry; state: HostState | null; onEdit: () => void }) {
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState<"switch" | "remove" | null>(null);
  const [error, setError] = useState<unknown>(null);
  const status = serverStatus(entry, state);
  const remote = entry.kind === "remote";
  const run = (kind: "switch" | "remove", action: () => Promise<void>) => {
    setBusy(kind);
    setError(null);
    action()
      .catch((err: unknown) => setError(err))
      .finally(() => setBusy(null));
  };
  const needs = entry.needsInput ?? 0;
  return (
    <li className="flex flex-col gap-2 border-b border-line px-3 py-2.5 last:border-b-0 md:py-2">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <Marker tone={status.tone} />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
            <span className="min-w-0 truncate text-body font-medium text-fg">{entry.name}</span>
            {entry.active ? <Chip>active</Chip> : null}
            {needs > 0 ? (
              <Chip tone="need" dot className="tabular">
                {needs} need input
              </Chip>
            ) : null}
          </div>
          <p className="truncate font-mono text-meta text-muted" title={serverWhere(entry, state)}>
            {serverWhere(entry, state)}
            {status.note === "" ? null : <span className={status.tone === "bad" ? "text-bad" : undefined}> · {status.note}</span>}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-1.5 max-md:w-full max-md:pl-[18px]">
          {entry.active ? null : (
            <Button small busy={busy === "switch"} disabled={busy !== null} onClick={() => run("switch", () => api.switch(entry.id))}>
              Switch
            </Button>
          )}
          {remote ? (
            <>
              <Button small variant="ghost" disabled={busy !== null} onClick={onEdit}>
                Edit
              </Button>
              <Button small variant="danger" disabled={busy !== null || confirm} onClick={() => setConfirm(true)}>
                Remove
              </Button>
            </>
          ) : null}
        </div>
      </div>
      {confirm ? (
        <Alert
          tone="bad"
          actions={
            <>
              <Button small variant="danger" busy={busy === "remove"} onClick={() => run("remove", () => api.remove(entry.id))}>
                Remove
              </Button>
              <Button small variant="ghost" disabled={busy !== null} onClick={() => setConfirm(false)}>
                Cancel
              </Button>
            </>
          }
        >
          Remove {entry.name}?{entry.active ? " Stage switches to this machine first." : ""} Its notifications stop on this device; the server keeps its data.
        </Alert>
      ) : null}
      <ErrorText error={error} />
    </li>
  );
}

/** Add or edit a remote server: name, URL, Test connection, Save (saving never switches). */
function ServerDrawer({ api, entry, onClose }: { api: HostServers; entry: HostServerEntry | null; onClose: () => void }) {
  const formId = useId();
  const [name, setName] = useState(entry?.name ?? "");
  const [url, setUrl] = useState(entry?.url ?? "");
  const [tested, setTested] = useState<{ url: string; result: HostServerTest } | null>(null);
  const [testing, setTesting] = useState(false);
  const [testError, setTestError] = useState<unknown>(null);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);
  const target = url.trim();
  // A result belongs to the URL it tested; editing the URL drops it.
  const result = tested !== null && tested.url === target ? tested.result : null;
  const valid = name.trim() !== "" && target !== "";

  const test = () => {
    setTesting(true);
    setTestError(null);
    api
      .test(target)
      .then((r) => setTested({ url: target, result: r }))
      .catch((err: unknown) => setTestError(err))
      .finally(() => setTesting(false));
  };
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!valid) return;
    const input = { name: name.trim(), url: target };
    setSaving(true);
    setSaveError(null);
    (entry === null ? api.add(input) : api.update(entry.id, input))
      .then(() => onClose())
      .catch((err: unknown) => {
        setSaveError(err);
        setSaving(false);
      });
  };
  const failed = result !== null && (!result.reachable || result.error !== "");

  return (
    <Drawer
      title={entry === null ? "Add server" : `Edit ${entry.name}`}
      onClose={onClose}
      footer={
        <>
          <Button type="submit" form={formId} variant="primary" busy={saving} disabled={!valid}>
            {entry === null ? "Save server" : "Save"}
          </Button>
          <Button onClick={onClose}>Cancel</Button>
          {entry === null ? <span className="ml-auto font-mono text-meta-sm text-muted">saves without switching</span> : null}
        </>
      }
    >
      <form id={formId} onSubmit={submit} className="flex flex-col gap-3.5">
        <Muted>
          A Stage server on another machine of a trusted network: no sign-in, plain HTTP. On that machine it listens on TCP, for example{" "}
          <code className="font-mono text-detail text-fg">stage serve --listen 0.0.0.0:7380</code>.
        </Muted>
        <Field label="Name">
          {(id) => <Input id={id} data-autofocus value={name} maxLength={60} onChange={(e) => setName(e.target.value)} placeholder="Studio" autoComplete="off" />}
        </Field>
        <Field label="URL" hint="http(s)://host[:port], no path">
          {(id) => (
            <Input
              id={id}
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder="http://192.168.1.20:7380"
              inputMode="url"
              autoComplete="off"
              autoCapitalize="off"
              spellCheck={false}
              className="font-mono"
            />
          )}
        </Field>
        <div>
          <Button busy={testing} disabled={target === ""} onClick={test}>
            Test connection
          </Button>
        </div>
        <Panel title="Connection" meta={<span className="break-all">{result === null ? "not tested" : target}</span>} flush>
          <Steps label="Connection test">
            {serverTestSteps(result, testing).map((s) => (
              <Step key={s.key} label={s.key} note={s.note} status={s.status} />
            ))}
          </Steps>
        </Panel>
        {failed ? (
          <div className="flex flex-col gap-3" aria-live="polite">
            <Alert tone="bad">
              {result.hint === "" ? result.error : result.hint}
              {result.hint === "" || result.error === "" ? null : <span className="mt-1 block font-mono text-meta break-words">{result.error}</span>}
            </Alert>
            {result.command === "" ? null : <CommandBox command={result.command} />}
          </div>
        ) : null}
        <ErrorText error={testError ?? saveError} />
      </form>
    </Drawer>
  );
}
