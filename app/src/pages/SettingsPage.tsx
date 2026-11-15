import { useIsMutating, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useId, useState, type FormEvent } from "react";
import { useApi, usePlatform } from "../platform/context.tsx";
import { invalidateWork, keys, usePreferences, useT3Status } from "../api/queries.ts";
import {
  INTERACTION_MODES,
  type InteractionMode,
  type ModelDefaults,
  type NotificationEvent,
  type Preferences,
  type T3Status,
} from "../api/types.ts";
import { ModelFields, type ModelConfig } from "../components/ModelFields.tsx";
import { taskStateTone } from "../components/StateBadge.tsx";
import { Alert, Button, Checkbox, Chip, Detail, Details, ErrorText, ExternalButton, Field, Input, Muted, PageHeader, Panel, QueryState, Segmented, cx, toneClass, type ChipTone } from "../components/ui.tsx";
import { HTTPS_DOCS_URL, type PushState } from "../lib/push.ts";
import { THEMES, useTheme } from "../lib/theme.ts";
import { dateTime, relativeTime } from "../lib/format.ts";

const when = (iso: string | null) => (iso === null ? "-" : `${dateTime(iso)} (${relativeTime(iso)})`);

function StatusCard({ status }: { status: T3Status }) {
  return (
    <Panel
      title="T3 connection"
      actions={
        status.connected ? (
          <Chip tone="ok" dot>
            connected
          </Chip>
        ) : (
          <Chip tone="bad" dot>
            not connected
          </Chip>
        )
      }
    >
      <Details>
        <Detail label="T3 URL">{status.t3Url}</Detail>
        <Detail label="Public URL">{status.publicUrl}</Detail>
        <Detail label="Environment">{status.environmentId ?? "-"}</Detail>
        <Detail label="Client id">{status.clientId ?? "-"}</Detail>
        <Detail label="Token expires">{when(status.tokenExpiresAt)}</Detail>
        <Detail label="Admin token">
          {status.adminTokenConfigured ? "configured (auto-renew)" : "not configured (pair manually)"}
        </Detail>
        <Detail label="Last full sync">{when(status.lastFullSyncAt)}</Detail>
        <Detail label="Last error">
          {status.lastError === null ? "-" : <span className="text-bad">{status.lastError}</span>}
        </Detail>
      </Details>
    </Panel>
  );
}

function PairCard() {
  const api = useApi();
  const qc = useQueryClient();
  const [code, setCode] = useState("");
  const pair = useMutation({
    mutationFn: () => api.pair({ code: code.trim() }),
    onSuccess: (status) => {
      setCode("");
      qc.setQueryData(keys.t3Status, status);
    },
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (code.trim() !== "") pair.mutate();
  };
  return (
    <Panel title="Pair manually" meta="fallback">
      <form onSubmit={submit} className="flex flex-col gap-3">
        <Muted>
          Use when auto-renew is unavailable: run <code className="font-mono text-fg">t3 pair</code> on the T3 host and paste the code.
        </Muted>
        <div className="flex flex-wrap items-end gap-2">
          <Field label="Pairing code" className="min-w-52 flex-1">
            {(id) => (
              <Input id={id} value={code} onChange={(e) => setCode(e.target.value)} placeholder="ABCD-1234" className="font-mono" />
            )}
          </Field>
          <Button type="submit" variant="primary" busy={pair.isPending} disabled={code.trim() === ""}>
            Pair
          </Button>
        </div>
        {pair.isSuccess ? <Alert tone="plain">Paired.</Alert> : null}
        <ErrorText error={pair.error} />
      </form>
    </Panel>
  );
}

function SyncCard() {
  const api = useApi();
  const qc = useQueryClient();
  const sync = useMutation({
    mutationFn: api.sync,
    onSuccess: async () => {
      await Promise.all([
        invalidateWork(qc),
        qc.invalidateQueries({ queryKey: keys.projects }),
        qc.invalidateQueries({ queryKey: keys.t3Status }),
      ]);
    },
  });
  return (
    <Panel title="Sync" meta="every 5 min">
      <div className="flex flex-col gap-3">
        <Muted>Full sync of all T3 projects and threads runs every 5 minutes. Run it now to refresh history immediately.</Muted>
        <div>
          <Button variant="primary" busy={sync.isPending} onClick={() => sync.mutate()}>
            Sync now
          </Button>
        </div>
        {sync.data === undefined ? null : (
          <Alert tone="plain">
            Synced <span className="tabular font-mono">{sync.data.projects}</span> projects,{" "}
            <span className="tabular font-mono">{sync.data.threads}</span> threads at{" "}
            <span className="font-mono">{dateTime(sync.data.finishedAt)}</span>.
          </Alert>
        )}
        <ErrorText error={sync.error} />
      </div>
    </Panel>
  );
}

const INTERACTION_OPTIONS = INTERACTION_MODES.map((m) => ({ value: m, label: m }));

const FALLBACK: ModelDefaults = {
  providerInstanceId: null,
  model: null,
  modelOptions: [],
  runtimeMode: "full-access",
  interactionMode: "default",
};

function DefaultModelCard() {
  const prefs = usePreferences();
  const pinned = prefs.data?.pinned != null;
  return (
    <Panel title="Default model" {...(prefs.data === undefined ? {} : { meta: pinned ? "pinned" : "last used" })}>
      <QueryState isPending={prefs.isPending} error={prefs.error}>
        {prefs.data === undefined ? null : <DefaultModelForm prefs={prefs.data} />}
      </QueryState>
    </Panel>
  );
}

function DefaultModelForm({ prefs }: { prefs: Preferences }) {
  const api = useApi();
  const qc = useQueryClient();
  const start = prefs.pinned ?? prefs.lastUsed ?? FALLBACK;
  const [config, setConfig] = useState<ModelConfig>({
    providerInstanceId: start.providerInstanceId,
    model: start.model,
    modelOptions: start.modelOptions,
    runtimeMode: start.runtimeMode,
  });
  const [mode, setMode] = useState<InteractionMode>(start.interactionMode);
  const save = useMutation({
    mutationFn: (d: ModelDefaults | null) => api.setPinned(d),
    onSuccess: (p) => qc.setQueryData(keys.preferences, p),
  });
  const pinned = prefs.pinned !== null;
  const note = pinned
    ? "New tasks start from this pinned model and mode."
    : prefs.lastUsed === null
      ? "Not pinned. New tasks start from the T3 default until you create one; after that, from your last task."
      : "Not pinned. New tasks start from your last task's model and mode (shown). Pin to keep them fixed.";
  return (
    <div className="flex flex-col gap-3">
      <Muted>{note}</Muted>
      <ModelFields value={config} onChange={setConfig} />
      <div className="flex flex-col items-start gap-1.5">
        <span className="label-caps">Mode</span>
        <Segmented label="Interaction mode" options={INTERACTION_OPTIONS} value={mode} onChange={setMode} />
      </div>
      <div className="flex flex-wrap gap-2">
        <Button
          variant="primary"
          busy={save.isPending && save.variables !== null}
          disabled={save.isPending}
          onClick={() => save.mutate({ ...config, interactionMode: mode })}
        >
          {pinned ? "Save pinned default" : "Pin as default"}
        </Button>
        {pinned ? (
          <Button busy={save.isPending && save.variables === null} disabled={save.isPending} onClick={() => save.mutate(null)}>
            Unpin
          </Button>
        ) : null}
      </div>
      <ErrorText error={save.error} />
    </div>
  );
}

const PUSH_KEY = ["pushState"];

const PUSH_CHIP: Record<PushState, { tone: ChipTone; label: string }> = {
  insecure: { tone: "idle", label: "needs https" },
  unsupported: { tone: "idle", label: "unsupported" },
  "ios-install": { tone: "idle", label: "install first" },
  blocked: { tone: "bad", label: "blocked" },
  off: { tone: "idle", label: "off" },
  on: { tone: "ok", label: "on" },
};

/** Display order and copy for the global notification filter. */
const EVENTS: readonly { event: NotificationEvent; label: string; hint: string }[] = [
  { event: "needs_input", label: "Needs input", hint: "waits on you" },
  { event: "failed", label: "Failed", hint: "run failed" },
  { event: "done", label: "Done", hint: "run finished" },
  { event: "settled", label: "Settled", hint: "settled in T3" },
];

const NOTIFY_EVENTS_KEY = ["setNotifyEvents"];

/** Global (all devices) event filter: each toggle saves at once, optimistically; a failed save reverts. */
function EventFilter() {
  const api = useApi();
  const qc = useQueryClient();
  const prefs = usePreferences();
  const pending = useIsMutating({ mutationKey: NOTIFY_EVENTS_KEY });
  const save = useMutation({
    mutationKey: NOTIFY_EVENTS_KEY,
    mutationFn: api.setNotifyEvents,
    onMutate: async (events) => {
      await qc.cancelQueries({ queryKey: keys.preferences });
      const previous = qc.getQueryData<Preferences>(keys.preferences);
      if (previous !== undefined) qc.setQueryData<Preferences>(keys.preferences, { ...previous, notifyEvents: events });
      return { previous };
    },
    onError: (_err, _events, ctx) => {
      if (ctx?.previous !== undefined) qc.setQueryData(keys.preferences, ctx.previous);
    },
    // Only the last of overlapping saves writes the server copy (earlier responses are stale).
    onSuccess: (p) => {
      if (qc.isMutating({ mutationKey: NOTIFY_EVENTS_KEY }) <= 1) qc.setQueryData(keys.preferences, p);
    },
  });
  const enabled = prefs.data?.notifyEvents ?? [];
  const toggle = (event: NotificationEvent, on: boolean) =>
    save.mutate(EVENTS.map((e) => e.event).filter((e) => (e === event ? on : enabled.includes(e))));
  return (
    <fieldset className="flex min-w-0 flex-col gap-2" aria-busy={pending > 0}>
      <legend className="mb-2 flex items-baseline gap-2">
        <span className="label-caps">Events</span>
        <span className="font-mono text-meta-sm text-muted">all devices</span>
      </legend>
      <QueryState isPending={prefs.isPending} error={prefs.error}>
        <Muted>
          {enabled.length === 0
            ? "All events off. Nothing is recorded or pushed."
            : "Checked events push to every device and appear in the inbox. Unchecked events are dropped."}
        </Muted>
        <ul className="border-y border-line">
          {EVENTS.map(({ event, label, hint }) => (
            <li key={event} className="border-b border-line last:border-b-0">
              <label className="flex min-h-11 cursor-pointer items-center gap-3 transition-colors duration-150 hover:bg-panel md:min-h-8">
                <Checkbox checked={enabled.includes(event)} onChange={(e) => toggle(event, e.target.checked)} />
                <span aria-hidden className={cx("size-1.5 shrink-0 bg-(--c)", toneClass(taskStateTone(event)))} />
                <span className="text-small text-fg">{label}</span>
                <span className="ml-auto font-mono text-meta text-muted">{hint}</span>
              </label>
            </li>
          ))}
        </ul>
      </QueryState>
      <ErrorText error={save.error} />
    </fieldset>
  );
}

function NotificationsCard() {
  const { notifications } = usePlatform();
  const qc = useQueryClient();
  // Device state, not server state: no polling; rechecked on focus (permission may change in system settings).
  const state = useQuery({ queryKey: PUSH_KEY, queryFn: () => notifications.state(), refetchInterval: false });
  const refresh = () => qc.invalidateQueries({ queryKey: PUSH_KEY });
  const enable = useMutation({ mutationFn: () => notifications.enable(), onSettled: refresh });
  const disable = useMutation({ mutationFn: () => notifications.disable(), onSettled: refresh });
  const test = useMutation({ mutationFn: () => notifications.test(), onSettled: refresh });
  const busy = enable.isPending || disable.isPending || test.isPending;
  const chip = state.data === undefined ? null : PUSH_CHIP[state.data];
  const deviceId = useId();
  return (
    <Panel
      title="Notifications"
      {...(chip === null ? {} : { actions: <Chip tone={chip.tone} dot>{chip.label}</Chip> })}
    >
      <div className="flex flex-col gap-4">
        <EventFilter />
        <section className="flex flex-col gap-3 border-t border-line pt-4" aria-labelledby={deviceId}>
          <h3 id={deviceId} className="label-caps">
            This device
          </h3>
          <Muted>Web Push to this browser for the checked events.</Muted>
          <QueryState isPending={state.isPending} error={state.error}>
            {state.data === "insecure" ? (
              <Alert
                tone="plain"
                actions={
                  <ExternalButton href={HTTPS_DOCS_URL} small>
                    HTTPS setup
                  </ExternalButton>
                }
              >
                Push needs HTTPS, or http://localhost on this machine. This page is served over plain HTTP.
              </Alert>
            ) : null}
            {state.data === "unsupported" ? <Muted>This browser does not support Web Push notifications.</Muted> : null}
            {state.data === "ios-install" ? (
              <Alert tone="plain">
                Add to Home Screen first: Share → Add to Home Screen, then open stage from the Home Screen.
              </Alert>
            ) : null}
            {state.data === "blocked" ? (
              <Alert tone="plain">Notifications are blocked for this site. Allow them in the browser or system settings, then reload.</Alert>
            ) : null}
            {state.data === "off" ? (
              <div>
                <Button variant="primary" busy={enable.isPending} disabled={busy} onClick={() => enable.mutate()}>
                  Enable notifications
                </Button>
              </div>
            ) : null}
            {state.data === "on" ? (
              <div className="flex flex-wrap gap-2">
                <Button variant="primary" busy={test.isPending} disabled={busy} onClick={() => test.mutate()}>
                  Send test
                </Button>
                <Button busy={disable.isPending} disabled={busy} onClick={() => disable.mutate()}>
                  Disable
                </Button>
              </div>
            ) : null}
          </QueryState>
          {test.isSuccess ? <Alert tone="plain">Test notification sent.</Alert> : null}
          <ErrorText error={enable.error ?? disable.error ?? test.error} />
        </section>
      </div>
    </Panel>
  );
}

const THEME_OPTIONS = THEMES.map((t) => ({ value: t, label: t }));

function ThemeCard() {
  const { theme, setTheme } = useTheme();
  return (
    <Panel title="Theme">
      <div className="flex flex-wrap items-center gap-3">
        <Segmented label="Theme" options={THEME_OPTIONS} value={theme} onChange={setTheme} />
        <Muted>{theme === "system" ? "Follows the OS light/dark setting." : `Always ${theme}.`}</Muted>
      </div>
    </Panel>
  );
}

export function SettingsPage() {
  const status = useT3Status();
  return (
    <>
      <PageHeader title="Settings" meta="T3 connection, sync, defaults, notifications, appearance" />
      <div className="grid grid-cols-1 items-start gap-3.5 lg:grid-cols-2">
        <div className="lg:row-span-5">
          <QueryState isPending={status.isPending} error={status.error}>
            {status.data === undefined ? null : <StatusCard status={status.data} />}
          </QueryState>
        </div>
        <SyncCard />
        <DefaultModelCard />
        <PairCard />
        <NotificationsCard />
        <ThemeCard />
      </div>
    </>
  );
}
