// Settings: three tabs, each its own route (lib/settings.ts). General: user, theme, Info.
// Connections: machines and their harness connections (components/Machines.tsx), each with its new-task defaults.
// Notifications: events and this device's delivery.
import { useIsMutating, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import { useApi, usePlatform } from "../platform/context.tsx";
import { invalidateWork, keys, useHarnesses, useMachines, useMe, usePreferences } from "../api/queries.ts";
import { type NotificationEvent, type Preferences } from "../api/types.ts";
import { InfoPanel } from "../components/InfoPanel.tsx";
import { AddMachineDrawer, MachinesPanel } from "../components/Machines.tsx";
import { notificationEventTone } from "../components/StateBadge.tsx";
import { useStatusAlert } from "../components/StatusRow.tsx";
import {
  Alert,
  Button,
  Checkbox,
  ChoiceRow,
  FieldGroup,
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
  Segmented,
  SubHeading,
  TabNav,
  type ChipTone,
} from "../components/ui.tsx";
import { HTTPS_DOCS_URL, type PushState } from "../lib/push.ts";
import type { NotificationDelivery } from "../platform/index.ts";
import { connectedRows, connectionRows } from "../lib/connectionRows.ts";
import { SETTINGS_TABS, settingsTabLabel, type SettingsTab } from "../lib/settings.ts";
import { THEMES, useTheme } from "../lib/theme.ts";
import { dateTime, shortDate } from "../lib/format.ts";

/** The acting user (GET /api/me). No sign-in: everything is attributed to the default user. */
function UserCard() {
  const me = useMe();
  return (
    <Panel title="User" meta="no sign-in">
      <QueryState isPending={me.isPending} error={me.error} hasData={me.data !== undefined}>
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
    <FieldGroup label="Events" meta="all devices" aria-busy={pending > 0}>
      <QueryState isPending={prefs.isPending} error={prefs.error} hasData={prefs.data !== undefined}>
        <Muted>
          {enabled.length === 0
            ? "All events off. Nothing is recorded or pushed."
            : "Checked events push to every device and appear in the inbox. Unchecked events are dropped."}
        </Muted>
        <RowList className="border-y border-line">
          {EVENTS.map(({ event, label, hint }) => (
            <Row key={event} bare>
              <ChoiceRow
                inset
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
    </FieldGroup>
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
  const state = useQuery({ queryKey: keys.device.push, queryFn: () => notifications.state(), refetchInterval: false });
  const refresh = () => qc.invalidateQueries({ queryKey: keys.device.push });
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
          <QueryState isPending={state.isPending} error={state.error} hasData={state.data !== undefined}>
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

/** Settings' title, meta and tabs; each tab is its own route. */
function SettingsHeader({ tab, meta, actions }: { tab: SettingsTab; meta: string; actions?: ReactNode }) {
  const alert = useStatusAlert();
  const items = SETTINGS_TABS.map((t) => ({
    to: t.to,
    label: t.label,
    end: t.tab === "general",
    ...(t.tab === "connections" && alert !== null ? { marker: <Marker tone={alert} /> } : {}),
  }));
  return (
    <>
      <PageHeader title="Settings" name={`${settingsTabLabel(tab)} · Settings`} meta={meta} {...(actions === undefined ? {} : { actions })} />
      <TabNav label="Settings" items={items} className="mb-4" />
    </>
  );
}

/** /settings: the user, appearance and Info. New-task defaults live with each connection (Connections). */
export function SettingsGeneralPage() {
  return (
    <>
      <SettingsHeader tab="general" meta={settingsTabLabel("general")} />
      <div className="flex max-w-[720px] flex-col gap-3.5">
        <UserCard />
        <ThemeCard />
        <InfoPanel />
      </div>
    </>
  );
}

/** /settings/connections: every machine with its harness connections; Sync now and Add machine. */
export function SettingsConnectionsPage() {
  const api = useApi();
  const qc = useQueryClient();
  const harnesses = useHarnesses();
  const { machines, data } = useMachines();
  const [adding, setAdding] = useState(false);
  const sync = useMutation({
    mutationFn: api.sync,
    onSuccess: async () => {
      await Promise.all([invalidateWork(qc), qc.invalidateQueries({ queryKey: keys.projects }), qc.invalidateQueries({ queryKey: keys.t3Status })]);
    },
  });
  const connections = machines.reduce((n, m) => n + connectedRows(connectionRows(m, harnesses.connection)).length, 0);
  const counts = data === undefined ? "" : ` · ${count(machines.length, "machine")} · ${count(connections, "connection")}`;
  return (
    <>
      <SettingsHeader
        tab="connections"
        meta={`${settingsTabLabel("connections")}${counts}`}
        actions={
          <>
            <Button busy={sync.isPending} onClick={() => sync.mutate()} title="Refresh workspace inventory and tracked sessions (also every 5 minutes)">
              Sync now
            </Button>
            <Button variant="primary" onClick={() => setAdding(true)}>
              Add machine
            </Button>
          </>
        }
      />
      <div className="flex flex-col gap-3.5">
        {sync.data === undefined ? null : (
          <Alert tone="plain">
            Synced <span className="tabular font-mono">{sync.data.projects}</span> projects,{" "}
            <span className="tabular font-mono">{sync.data.threads}</span> sessions at <span className="font-mono">{dateTime(sync.data.finishedAt)}</span>.
            {sync.data.unavailableConnections?.map((id) => <p key={id}>{harnesses.name(id)} could not finish syncing. Check its connection below.</p>)}
          </Alert>
        )}
        <ErrorText error={sync.error} />
        <MachinesPanel meta="synced every 5 min" />
      </div>
      {adding ? <AddMachineDrawer onClose={() => setAdding(false)} /> : null}
    </>
  );
}

/** /settings/notifications: the global event filter and this device's delivery. */
export function SettingsNotificationsPage() {
  return (
    <>
      <SettingsHeader tab="notifications" meta={settingsTabLabel("notifications")} />
      <div className="flex max-w-[720px] flex-col gap-3.5">
        <NotificationsCard />
      </div>
    </>
  );
}

const count = (n: number, noun: string) => `${n} ${noun}${n === 1 ? "" : "s"}`;
