import { useIsMutating, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useApi, usePlatform } from "../platform/context.tsx";
import { invalidateWork, keys, useHarnesses, useMachines, useMe, usePreferences } from "../api/queries.ts";
import {
  type ModelDefaults,
  type NotificationEvent,
  type Preferences,
} from "../api/types.ts";
import { DesktopAppCard } from "../components/Host.tsx";
import { SharedPaths } from "../components/SharedPaths.tsx";
import { HarnessConnections } from "../components/HarnessConnections.tsx";
import { MachinesCard } from "../components/Machines.tsx";
import { ServersCard } from "../components/Servers.tsx";
import { ModelFields, type ModelConfig } from "../components/ModelFields.tsx";
import { notificationEventTone } from "../components/StateBadge.tsx";
import {
  Alert,
  Button,
  Checkbox,
  ChoiceRow,
  Chip,
  ErrorText,
  ExternalButton,
  Marker,
  MetaLine,
  Muted,
  PageHeader,
  Panel,
  QueryState,
  Row,
  RowList,
  SectionHeading,
  Segmented,
  SubHeading,
  type ChipTone,
} from "../components/ui.tsx";
import { HTTPS_DOCS_URL, type PushState } from "../lib/push.ts";
import type { NotificationDelivery } from "../platform/index.ts";
import { THEMES, useTheme } from "../lib/theme.ts";
import { dateTime, shortDate } from "../lib/format.ts";

/** The acting user (GET /api/me). No sign-in: everything is attributed to the default user. */
function UserCard() {
  const me = useMe();
  return (
    <Panel title="User" meta="no sign-in">
      <QueryState isPending={me.isPending} error={me.error}>
        {me.data === undefined ? null : (
          <div className="flex flex-col gap-3">
            <div className="min-w-0">
              <p className="truncate text-small font-medium text-fg">{me.data.displayName}</p>
              <MetaLine>
                <span>
                  user #<span className="tabular">{me.data.id}</span>
                </span>
                <span>since {shortDate(me.data.createdAt)}</span>
              </MetaLine>
            </div>
            <Muted>Stage has no sign-in. Everything created here is attributed to this user.</Muted>
          </div>
        )}
      </QueryState>
    </Panel>
  );
}


function SyncCard() {
  const api = useApi();
  const harnesses = useHarnesses();
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
        <Muted>Refresh workspace inventory and tracked sessions. Stage checks them every five minutes.</Muted>
        <div>
          <Button variant="primary" busy={sync.isPending} onClick={() => sync.mutate()}>
            Sync now
          </Button>
        </div>
        {sync.data === undefined ? null : (
          <Alert tone="plain">
            Synced <span className="tabular font-mono">{sync.data.projects}</span> projects,{" "}
            <span className="tabular font-mono">{sync.data.threads}</span> sessions at{" "}
            <span className="font-mono">{dateTime(sync.data.finishedAt)}</span>.
            {sync.data.unavailableConnections?.map(id => <p key={id}>{harnesses.name(id)} could not finish syncing. Check its connection above.</p>)}
          </Alert>
        )}
        <ErrorText error={sync.error} />
      </div>
    </Panel>
  );
}

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
    <Panel title="T3 default model" {...(prefs.data === undefined ? {} : { meta: pinned ? "pinned" : "last used" })}>
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
  const save = useMutation({
    mutationFn: (d: ModelDefaults | null) => api.setPinned(d),
    onSuccess: (p) => qc.setQueryData(keys.preferences, p),
  });
  const pinned = prefs.pinned !== null;
  const note = pinned
    ? "New T3 tasks use this pinned selection."
    : prefs.lastUsed === null
      ? "Not pinned. New tasks start from the T3 default until you create one; after that, from your last task."
      : "New T3 tasks reuse the last T3 selection, including harness defaults. Pin the values shown below to use them explicitly.";
  return (
    <div className="flex flex-col gap-3">
      <Muted>{note} Interaction mode and permission start at harness default.</Muted>
      <ModelFields value={config} onChange={setConfig} />
      <div className="flex flex-wrap gap-2">
        <Button
          variant="primary"
          busy={save.isPending && save.variables !== null}
          disabled={save.isPending}
          onClick={() => save.mutate({ ...config, interactionMode: start.interactionMode })}
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
  { event: "ready", label: "Ready", hint: "needs are completed, start it" },
  { event: "done", label: "Done", hint: "run finished" },
  { event: "settled", label: "Completed", hint: "accepted in Stage" },
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
        <RowList className="border-y border-line">
          {EVENTS.map(({ event, label, hint }) => (
            <Row key={event} bare>
              <ChoiceRow
                control={
                  <>
                    <Checkbox checked={enabled.includes(event)} onChange={(e) => toggle(event, e.target.checked)} />
                    <Marker tone={notificationEventTone(event)} />
                  </>
                }
                detail={hint}
              >
                {label}
              </ChoiceRow>
            </Row>
          ))}
        </RowList>
      </QueryState>
      <ErrorText error={save.error} />
    </fieldset>
  );
}

/** The event filter always; this device's delivery only when the host has system notifications. */
function NotificationsCard() {
  const { notifications } = usePlatform();
  if (notifications === undefined) {
    return (
      <Panel title="Notifications">
        <EventFilter />
      </Panel>
    );
  }
  return <DeviceNotificationsCard notifications={notifications} />;
}

/** "This device" copy per delivery channel (absent channel = web-push). */
const DEVICE_COPY: Record<NonNullable<NotificationDelivery["channel"]>, { intro: string; unsupported: string; tested: string }> = {
  "web-push": {
    intro: "Web Push to this browser for the checked events.",
    unsupported: "This browser does not support Web Push notifications.",
    tested: "Test notification sent.",
  },
  native: {
    intro: "System notifications from the Stage app for the checked events, when Stage is not in front.",
    unsupported: "This system has no notification service Stage can use.",
    tested: "Test notification shown.",
  },
};

function DeviceNotificationsCard({ notifications }: { notifications: NotificationDelivery }) {
  const qc = useQueryClient();
  const channel = notifications.channel ?? "web-push";
  const copy = DEVICE_COPY[channel];
  const canOpenSettings = notifications.openSystemSettings !== undefined;
  // Device state, not server state: no polling; rechecked on focus (permission may change in system settings).
  const state = useQuery({ queryKey: PUSH_KEY, queryFn: () => notifications.state(), refetchInterval: false });
  const refresh = () => qc.invalidateQueries({ queryKey: PUSH_KEY });
  const enable = useMutation({ mutationFn: () => notifications.enable(), onSettled: refresh });
  const disable = useMutation({ mutationFn: () => notifications.disable(), onSettled: refresh });
  const test = useMutation({ mutationFn: () => notifications.test(), onSettled: refresh });
  // Resolves false when the host cannot open them; the hint then says where to go. Returning re-checks state on focus.
  const settings = useMutation({ mutationFn: async () => (await notifications.openSystemSettings?.()) ?? false });
  const busy = enable.isPending || disable.isPending || test.isPending || settings.isPending;
  const chip = state.data === undefined ? null : PUSH_CHIP[state.data];
  return (
    <Panel
      title="Notifications"
      {...(chip === null ? {} : { actions: <Chip tone={chip.tone} dot>{chip.label}</Chip> })}
    >
      <div className="flex flex-col gap-4">
        <EventFilter />
        <section className="flex flex-col gap-3 border-t border-line pt-4" aria-label="This device">
          <SubHeading title="This device" />
          <Muted>{copy.intro}</Muted>
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
            {state.data === "unsupported" ? <Muted>{copy.unsupported}</Muted> : null}
            {state.data === "ios-install" ? (
              <Alert tone="plain">
                Add to Home Screen first: Share → Add to Home Screen, then open stage from the Home Screen.
              </Alert>
            ) : null}
            {state.data === "blocked" && channel === "web-push" ? (
              <Alert tone="plain">Notifications are blocked for this site. Allow them in the browser or system settings, then reload.</Alert>
            ) : null}
            {state.data === "blocked" && channel === "native" ? (
              <Alert
                tone="plain"
                actions={
                  <>
                    {canOpenSettings ? (
                      <Button small busy={settings.isPending} disabled={busy} onClick={() => settings.mutate()}>
                        Open System Settings
                      </Button>
                    ) : null}
                    <Button small busy={enable.isPending} disabled={busy} onClick={() => enable.mutate()}>
                      Try again
                    </Button>
                  </>
                }
              >
                Notifications from Stage are turned off in system settings. Allow them there, then try again.
              </Alert>
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
                {channel === "native" && canOpenSettings ? (
                  <Button variant="ghost" busy={settings.isPending} disabled={busy} onClick={() => settings.mutate()}>
                    System settings
                  </Button>
                ) : null}
              </div>
            ) : null}
          </QueryState>
          {settings.data === false ? <Muted>Allow notifications for Stage in your desktop&apos;s settings.</Muted> : null}
          {test.isSuccess ? <Alert tone="plain">{copy.tested}</Alert> : null}
          <ErrorText error={enable.error ?? disable.error ?? test.error ?? settings.error} />
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

/**
 * Harness setup (T3, OpenCode, shared paths) serves this server's machine. With more than one machine it sits under
 * that machine's name; remote machines list their connections under Machines.
 */
function LocalHarnessSetup() {
  const { machines } = useMachines();
  const local = machines.find((m) => m.kind === "local");
  const cards = (
    <>
      <HarnessConnections />
      <SharedPaths />
    </>
  );
  if (machines.length < 2 || local === undefined) return cards;
  return (
    <section aria-label={`${local.label} harness setup`} className="flex flex-col gap-3.5">
      <SectionHeading title={local.label} meta="harness setup · this server" className="mt-2" />
      {cards}
    </section>
  );
}

export function SettingsPage() {
  return (
    <>
      <PageHeader title="Settings" meta="User, machines, harness connections, defaults, notifications, appearance" />
      <div className="grid grid-cols-1 items-start gap-3.5 lg:grid-cols-2">
        <div className="flex flex-col gap-3.5 lg:row-span-6">
          <MachinesCard />
          <LocalHarnessSetup />
          <ServersCard />
        </div>
        <UserCard />
        <SyncCard />
        <DefaultModelCard />
        <NotificationsCard />
        <ThemeCard />
        <DesktopAppCard />
      </div>
    </>
  );
}
