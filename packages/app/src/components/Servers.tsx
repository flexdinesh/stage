// Server switching on desktop (platform.host.servers): the Settings panel,
// Add/Edit drawer, and the picker for screens outside the main app.
// A host without server switching (the browser) renders none of it. Logic: lib/host.ts.
import { useCallback, useId, useState, type FormEvent } from "react";
import { Link } from "react-router";
import { serverStatus, serverTestSteps, serverWhere, type ServerTone } from "../lib/host.ts";
import { useHostServers, useHostState } from "../lib/hostState.ts";
import { usePlatform } from "../platform/context.tsx";
import type { HostServerEntry, HostServers, HostServerTest, HostState } from "../platform/index.ts";
import { IconCheck } from "./icons.tsx";
import { Step, Steps } from "./Steps.tsx";
import {
  Alert,
  Button,
  ConfirmInline,
  Drawer,
  ErrorText,
  Field,
  Hint,
  Input,
  Marker,
  Meta,
  Muted,
  Panel,
  PanelHeader,
  PickerButton,
  PopoverPanel,
  Row,
  RowButton,
  RowList,
  SkeletonRows,
  cx,
  rowButtonClass,
  usePopover,
  CommandBox,
} from "./ui.tsx";

/** Ruled list of servers, with the address and a check on the active one. */
function ServerChoices({ servers, state, onPick }: { servers: HostServerEntry[]; state: HostState | null; onPick: (entry: HostServerEntry) => void }) {
  return (
    <RowList>
      {servers.map((s) => {
        const status = serverStatus(s, state);
        return (
          <Row key={s.id} bare>
            <RowButton selected={s.active} aria-current={s.active || undefined} onClick={() => onPick(s)} className="gap-3">
              <Marker tone={status.tone} />
              <span className="min-w-0 flex-1">
                <span className="block truncate text-small font-medium text-fg">{s.name}</span>
                <span className="block truncate font-mono text-meta text-muted">
                  {serverWhere(s, state)}
                  {status.note === "" || status.tone === "ok" ? null : <span className={status.tone === "bad" ? "text-bad" : undefined}> · {status.note}</span>}
                </span>
              </span>
              <span className="flex size-4 shrink-0 items-center justify-center text-fg">
                {s.active ? (
                  <>
                    <IconCheck size={14} />
                    <span className="sr-only">active</span>
                  </>
                ) : null}
              </span>
            </RowButton>
          </Row>
        );
      })}
    </RowList>
  );
}

/** The servers to switch to, when the host has switching and more than one is saved; null otherwise. */
function useSwitchable(): { api: HostServers; servers: HostServerEntry[] } | null {
  const api = usePlatform().host?.servers;
  const { servers } = useHostServers();
  if (api === undefined || servers === null || servers.length < 2) return null;
  return { api, servers };
}

export function ServerSwitchButton() {
  const canSwitch = useSwitchable() !== null;
  const [open, setOpen] = useState(false);
  if (!canSwitch) return null;
  return (
    <>
      <Button className="self-start" onClick={() => setOpen(true)}>Switch server</Button>
      {open ? <ServerPicker onClose={() => setOpen(false)} /> : null}
    </>
  );
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

/** Server menu button (sidebar bottom row, its panel above the row; phone top bar) naming the active server (only with more than one saved): pick a server or manage them. */
export function ServerMenu({ className }: { className?: string }) {
  const state = useHostState();
  const switchable = useSwitchable();
  const [error, setError] = useState<unknown>(null);
  const popover = usePopover(useCallback(() => setError(null), []));

  if (switchable === null || state === null) return null;
  const { api, servers } = switchable;
  const active = servers.find((s) => s.active);
  const status = active === undefined ? UNKNOWN : serverStatus(active, state);
  const pick = (entry: HostServerEntry) => {
    if (entry.active) {
      popover.close();
      return;
    }
    setError(null);
    api.switch(entry.id).then(popover.close, (err: unknown) => setError(err));
  };
  return (
    <div {...popover.wrapProps} className={cx("relative min-w-0", className)}>
      <PickerButton
        mono
        {...popover.triggerProps}
        title={`Server: ${state.target.name}${status.note === "" ? "" : ` (${status.note})`}`}
        className="w-full max-w-[11rem] px-2 max-md:max-w-[7.5rem] md:gap-2 md:px-2.5"
      >
        <Marker tone={status.tone} />
        <span className="sr-only">Server: </span>
        <span className="min-w-0 flex-1 truncate text-left">{state.target.name}</span>
      </PickerButton>
      <PopoverPanel
        popover={popover}
        label="Servers"
        className="max-md:fixed max-md:inset-x-3 max-md:top-[calc(3.25rem+var(--inset-top)+0.25rem)] md:absolute md:w-[320px] md:max-lg:top-full md:max-lg:right-0 md:max-lg:mt-1 lg:bottom-full lg:left-3 lg:mb-1"
      >
        <PanelHeader title="Servers" meta={servers.length} />
        <ServerChoices servers={servers} state={state} onPick={pick} />
        {error === null ? null : (
          <div className="border-t border-line p-2">
            <ErrorText error={error} />
          </div>
        )}
        <Link to="/settings" onClick={popover.close} className={cx(rowButtonClass(), "border-t border-line text-small text-fg")}>
          Manage servers
        </Link>
      </PopoverPanel>
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
          error === null ? <SkeletonRows rows={2} label="Loading servers" /> : <div className="p-3"><ErrorText error={error} /></div>
        ) : (
          <RowList>
            {servers.map((s) => (
              <ServerRow key={s.id} api={api} entry={s} state={state} onEdit={() => setDrawer({ mode: "edit", entry: s })} />
            ))}
          </RowList>
        )}
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
  return (
    <Row className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <Marker tone={status.tone} />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1">
            <span className="min-w-0 truncate text-body font-medium text-fg">{entry.name}</span>
            {entry.active ? <Meta>active</Meta> : null}
          </div>
          <p className="truncate font-mono text-meta text-muted" title={serverWhere(entry, state)}>
            {serverWhere(entry, state)}
            {status.note === "" ? null : <span className={status.tone === "bad" ? "text-bad" : undefined}> · {status.note}</span>}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-1.5 max-md:w-full max-md:pl-[18px]">
          {entry.active ? null : (
            <Button small aria-label={`Switch to ${entry.name}`} busy={busy === "switch"} disabled={busy !== null} onClick={() => run("switch", () => api.switch(entry.id))}>
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
        <ConfirmInline
          danger
          confirmLabel="Remove"
          busy={busy === "remove"}
          onConfirm={() => run("remove", () => api.remove(entry.id))}
          onCancel={() => setConfirm(false)}
        >
          Remove {entry.name}?{entry.active ? " Stage switches to this machine first." : ""} Its notifications stop on this device; the server keeps its data.
        </ConfirmInline>
      ) : null}
      <ErrorText error={error} />
    </Row>
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
          {entry === null ? <Hint className="ml-auto">saves without switching</Hint> : null}
        </>
      }
    >
      <form id={formId} onSubmit={submit} className="flex flex-col gap-3.5">
        <Muted>Use the address you open in your browser. For shared access, use the server’s configured public URL.</Muted>
        <Field label="Name">
          {(id) => <Input id={id} data-autofocus value={name} maxLength={60} onChange={(e) => setName(e.target.value)} placeholder="Studio" autoComplete="off" />}
        </Field>
        <Field label="URL" hint="http(s)://host[:port], no path">
          {(id) => (
            <Input
              id={id}
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder="https://stage.example.com"
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
