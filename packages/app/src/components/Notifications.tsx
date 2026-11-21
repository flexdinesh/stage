import { hashKey, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useId, useRef, useState } from "react";
import { Link, useNavigate } from "react-router";
import { useApi, usePlatform } from "../platform/context.tsx";
import {
  keys,
  latestNotificationsKey,
  useInvalidateWork,
  useNotificationHistory,
  useNotificationInbox,
  useNotifications,
  useHarnesses,
} from "../api/queries.ts";
import type { Notification, NotificationEvent, NotificationGroup, NotificationPage, WaitingSession } from "../api/types.ts";
import { dateTime, relativeTime } from "../lib/format.ts";
import { pushFooterNote } from "../lib/push.ts";
import { reconcileToasts } from "../lib/notification-toasts.ts";
import { IconBell, IconChevronDown, IconClose } from "./icons.tsx";
import { NotificationEventBadge, TaskStateBadge, notificationEventLabel, notificationEventTone } from "./StateBadge.tsx";
import { AppButton, Button, Chip, Drawer, ErrorText, Muted, QueryState, cx, errorText, toneClass } from "./ui.tsx";
import { useSessionDestination } from "../lib/hostState.ts";

const TOAST_MS = 8000;
const MAX_TOASTS = 3;

/** A needs-input notification whose task/thread is still waiting (what the app badge counts). */
const stillWaiting = (n: Notification) => n.event === "needs_input" && n.currentState === "needs_input";

/** A ready notification whose task still waits in backlog: it can be started from here. */
const startable = (n: Notification): n is Notification & { taskId: number } =>
  n.event === "ready" && n.currentState === "backlog" && n.taskId !== null;

/** Start for a ready notification (POST start); the error shows in the title and as a red line. */
function StartButton({ taskId, onStarted }: { taskId: number; onStarted?: () => void }) {
  const api = useApi();
  const invalidate = useInvalidateWork();
  const start = useMutation({
    mutationFn: () => api.startTask(taskId),
    onSuccess: () => onStarted?.(),
    onSettled: invalidate,
  });
  const error = errorText(start.error);
  return (
    <>
      {error === null ? null : (
        <span className="min-w-0 basis-full truncate text-right font-mono text-meta-sm text-bad" title={error}>
          {error}
        </span>
      )}
      <Button small variant="primary" busy={start.isPending} onClick={() => start.mutate()} title={`Start task #${taskId}`}>
        Start
      </Button>
    </>
  );
}

const notificationPath = (n: Notification) => n.taskId === null ? "/?inbox" : `/tasks/${n.taskId}`;

const projectOf = (n: Notification) => n.projectTitle ?? (n.taskId === null ? "external thread" : "");

// ---- Delivered notification → app ----

/** On every notification delivered to this device: refetch at once instead of waiting for the poll. */
export function usePushRefresh() {
  const { notifications } = usePlatform();
  const qc = useQueryClient();
  useEffect(
    () =>
      notifications?.onDelivered(() => {
        for (const queryKey of [keys.notifications, keys.badge, keys.tasks, keys.task, keys.thread, keys.trees, keys.tree]) {
          void qc.invalidateQueries({ queryKey });
        }
      }),
    [notifications, qc],
  );
}

/**
 * Cross-device clear: closes system notifications for groups viewed on any
 * device, on load, when the app becomes visible, and on every push.
 */
export function useSystemNotificationSync() {
  const { notifications } = usePlatform();
  useEffect(() => {
    if (notifications === undefined) return;
    const delivery = notifications;
    void delivery.syncShown();
    function onVisible() {
      if (document.visibilityState === "visible") void delivery.syncShown();
    }
    document.addEventListener("visibilitychange", onVisible);
    const unsubscribe = delivery.onDelivered(() => void delivery.syncShown());
    return () => {
      document.removeEventListener("visibilitychange", onVisible);
      unsubscribe();
    };
  }, [notifications]);
}

/** Marks notification groups read (viewed here); other devices close their system notifications on their next sync. */
export function useMarkNotificationGroupsRead() {
  const api = useApi();
  const qc = useQueryClient();
  return useCallback(
    (groups: string[]) => {
      api
        .markNotificationGroupsRead(groups)
        .then(() => qc.invalidateQueries({ queryKey: keys.notifications }))
        .catch(() => undefined);
    },
    [api, qc],
  );
}

// ---- Bell ----

export function NotificationsBell({ onOpen }: { onOpen: () => void }) {
  const data = useNotifications().data;
  const unread = data?.unread ?? 0;
  const urgent = data?.notifications.some((n) => n.readAt === null && stillWaiting(n)) ?? false;
  const label = unread === 0 ? "Notifications" : `Notifications, ${unread} unread`;
  return (
    <Button
      variant="secondary"
      onClick={onOpen}
      title={label}
      aria-label={label}
      className={cx(unread === 0 ? "w-10 px-0 md:w-8" : "gap-1.5 px-2.5 md:px-2")}
    >
      <IconBell size={16} />
      {unread === 0 ? null : (
        <span className={cx("chip px-1.5 text-meta-sm leading-4 tabular", urgent ? "tone-need" : "tone-plain")}>
          {unread > 99 ? "99+" : unread}
        </span>
      )}
    </Button>
  );
}

// ---- Toasts ----

/**
 * Toasts notifications that arrive while the page is visible. The first load,
 * anything fetched while hidden, and the first fetch after returning only move
 * the baseline (the bell's unread count covers missed events). Unmounted while
 * the drawer is open (the list shows them there).
 */
export function NotificationToasts({ onOpenInbox }: { onOpenInbox: () => void }) {
  const qc = useQueryClient();
  useNotifications(); // keeps the polled query active
  const [toasts, setToasts] = useState<Notification[]>([]);

  useEffect(() => {
    const maxId = (items: Notification[]) => items.reduce((m, n) => Math.max(m, n.id), 0);
    const cached = qc.getQueryData<NotificationPage>(latestNotificationsKey);
    let baseline: number | null = cached === undefined ? null : maxId(cached.notifications);
    let skipNext = false;

    function onVisibility() {
      if (document.visibilityState === "hidden") skipNext = true;
    }
    document.addEventListener("visibilitychange", onVisibility);

    // Every successful fetch, even when the data is unchanged.
    const unsubscribe = qc.getQueryCache().subscribe((event) => {
      if (event.type !== "updated" || event.action.type !== "success") return;
      if (event.query.queryHash !== hashKey(latestNotificationsKey)) return;
      const items = qc.getQueryData<NotificationPage>(latestNotificationsKey)?.notifications;
      if (items === undefined) return;
      // Live state changes even when no new notification was sent.
      setToasts((t) => reconcileToasts(t, items));
      const prev = baseline;
      baseline = Math.max(prev ?? 0, maxId(items));
      const visible = document.visibilityState === "visible";
      if (prev === null || !visible || skipNext) {
        if (visible) skipNext = false;
        return;
      }
      const fresh = items.filter((n) => n.id > prev).toSorted((a, b) => a.id - b.id);
      if (fresh.length > 0) setToasts((t) => [...t, ...reconcileToasts(fresh, items)]);
    });
    return () => {
      unsubscribe();
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [qc]);

  const dismiss = (id: number) => setToasts((t) => t.filter((n) => n.id !== id));
  // Newest at the bottom, nearest the corner; older overflow collapses into "+n more".
  const shown = toasts.slice(-MAX_TOASTS);
  const hidden = toasts.length - shown.length;

  return (
    <div
      role="region"
      aria-label="New notifications"
      aria-live="polite"
      className="pointer-events-none fixed inset-x-3 bottom-[calc(56px+env(safe-area-inset-bottom)+8px)] z-[45] flex flex-col gap-2 md:inset-x-auto md:right-6 md:w-[360px] lg:bottom-6"
    >
      {hidden > 0 ? (
        <button
          type="button"
          onClick={() => {
            setToasts([]);
            onOpenInbox();
          }}
          className="toast-in pointer-events-auto border border-line-strong bg-surface px-3 py-2 text-left font-mono text-meta text-muted transition-colors duration-150 hover:bg-panel hover:text-fg"
        >
          +{hidden} more · open notifications
        </button>
      ) : null}
      {shown.map((n) => (
        <Toast key={n.id} n={n} onDismiss={() => dismiss(n.id)} />
      ))}
    </div>
  );
}

/** The harness destination of a notification or waiting session; none once its connection was replaced. */
function useNotificationDestination() {
  const destination = useSessionDestination();
  const harnesses = useHarnesses();
  return (n: Pick<Notification, "connectionId" | "links">) => (harnesses.retired(n.connectionId) ? null : destination(n));
}

function Toast({ n, onDismiss }: { n: Notification; onDismiss: () => void }) {
  const destination = useNotificationDestination();
  const { name } = useHarnesses();
  const sticky = n.event === "needs_input";
  const [paused, setPaused] = useState(false);
  const remaining = useRef(TOAST_MS);
  const onDismissRef = useRef(onDismiss);
  useEffect(() => {
    onDismissRef.current = onDismiss;
  });

  useEffect(() => {
    if (sticky || paused) return;
    const started = Date.now();
    const timer = window.setTimeout(() => onDismissRef.current(), remaining.current);
    return () => {
      window.clearTimeout(timer);
      remaining.current = Math.max(1000, remaining.current - (Date.now() - started));
    };
  }, [sticky, paused]);

  const project = projectOf(n);
  return (
    <div
      onMouseEnter={() => setPaused(true)}
      onMouseLeave={() => setPaused(false)}
      onFocus={() => setPaused(true)}
      onBlur={() => setPaused(false)}
      className={cx(
        "toast-in pointer-events-auto flex flex-col gap-1 border border-line-strong bg-surface py-2 pr-3",
        sticky ? "border-l-2 border-l-need pl-[11px]" : "pl-3",
      )}
    >
      <div className="flex items-center justify-between gap-2">
        <Link to={notificationPath(n)} onClick={onDismiss} title={n.title} className="min-w-0 truncate text-body font-medium hover:underline">
          {n.title}
        </Link>
        <Button variant="ghost" small onClick={onDismiss} aria-label="Dismiss notification" className="-my-1 -mr-1.5 w-6 shrink-0 px-0">
          <IconClose />
        </Button>
      </div>
      <div className="flex flex-wrap items-center justify-between gap-x-2.5 gap-y-1.5">
        <div className="flex min-w-0 items-center gap-2">
          <NotificationEventBadge event={n.event} />
          <span className="min-w-0 truncate font-mono text-meta text-muted">
            {project === "" ? "" : `${project} · `}
            <span className="tabular">{relativeTime(n.createdAt)}</span>
          </span>
        </div>
        <div className="flex flex-wrap items-center justify-end gap-1.5">
          {startable(n) ? <StartButton taskId={n.taskId} onStarted={onDismiss} /> : null}
          <ToastView n={n} onDismiss={onDismiss} />
          {destination(n) === null ? null : (
            <AppButton small variant={stillWaiting(n) ? "primary" : "secondary"} href={destination(n) ?? ""}>
              Open in {name(n.connectionId)}
            </AppButton>
          )}
        </div>
      </div>
    </div>
  );
}

function ToastView({ n, onDismiss }: { n: Notification; onDismiss: () => void }) {
  const navigate = useNavigate();
  return (
    <Button
      small
      onClick={() => {
        onDismiss();
        void navigate(notificationPath(n));
      }}
    >
      View
    </Button>
  );
}

// ---- Drawer ----

type Group = { label: string; items: NotificationGroup[] };

function groupByDay(items: NotificationGroup[], now: Date = new Date()): Group[] {
  const today = new Date(now);
  today.setHours(0, 0, 0, 0);
  const yesterday = new Date(today);
  yesterday.setDate(yesterday.getDate() - 1);
  const groups: Group[] = [
    { label: "Needs input", items: [] },
    { label: "Recent · Today", items: [] },
    { label: "Recent · Yesterday", items: [] },
    { label: "Earlier", items: [] },
  ];
  for (const n of items) {
    const t = new Date(n.createdAt).getTime();
    const index = n.currentState === "needs_input" ? 0 : t >= today.getTime() ? 1 : t >= yesterday.getTime() ? 2 : 3;
    const g = groups[index];
    g?.items.push(n);
  }
  return groups.filter((g) => g.items.length > 0);
}

const PUSH_KEY = ["pushState"];

/**
 * Opening reads the inbox: unread markers stay (from a snapshot) until
 * the drawer closes; anything that arrives while open is read on close.
 */
export function NotificationsDrawer({ onClose }: { onClose: () => void }) {
  const api = useApi();
  const { notifications } = usePlatform();
  const qc = useQueryClient();
  const inbox = useNotificationInbox();
  const silent = inbox.data?.pages[0]?.waitingSessions ?? [];
  const push = useQuery({
    queryKey: PUSH_KEY,
    queryFn: () => notifications?.state() ?? Promise.resolve(undefined),
    refetchInterval: false,
    enabled: notifications !== undefined,
  });
  // Snapshot at open: which rows were unread, and the newest id then.
  const [opened, setOpened] = useState<{ unread: ReadonlySet<string>; topId: number; markRead: boolean } | null>(null);
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set());
  const read = useMutation({
    mutationFn: api.markNotificationsRead,
    onSettled: () => qc.invalidateQueries({ queryKey: keys.notifications }),
  });
  const readMutate = read.mutate;

  // Opening the inbox clears all system notifications.
  useEffect(() => {
    void notifications?.closeShown();
  }, [notifications]);

  // A live state change can move a group across a page boundary. The newest
  // page wins; never render the same task twice after loading older pages.
  const byKey = new Map<string, NotificationGroup>();
  for (const page of inbox.data?.pages ?? []) {
    for (const group of page.groups) if (!byKey.has(group.key)) byKey.set(group.key, group);
  }
  const items = [...byKey.values()].toSorted((a, b) =>
    Number(b.currentState === "needs_input") - Number(a.currentState === "needs_input") || b.id - a.id,
  );
  const firstPage = inbox.data?.pages[0];
  if (firstPage !== undefined && opened === null && !inbox.isFetching && inbox.error === null) {
    const unread = new Set(items.filter((n) => n.unreadCount > 0).map((n) => n.key));
    setOpened({ unread, topId: firstPage.latestId, markRead: firstPage.unread > 0 });
  }
  useEffect(() => {
    if (opened?.markRead === true && opened.topId > 0) readMutate(opened.topId);
  }, [opened, readMutate]);

  // On close: read whatever arrived while open.
  const close = useRef({ newest: 0, topId: Number.MAX_SAFE_INTEGER });
  useEffect(() => {
    close.current = { newest: firstPage?.latestId ?? 0, topId: opened?.topId ?? Number.MAX_SAFE_INTEGER };
  });
  useEffect(
    () => () => {
      const { newest, topId } = close.current;
      if (newest <= topId) return;
      api
        .markNotificationsRead(newest)
        .then(() => qc.invalidateQueries({ queryKey: keys.notifications }))
        .catch(() => undefined);
    },
    [api, qc],
  );

  const isUnread = (n: NotificationGroup) =>
    opened !== null && (opened.unread.has(n.key) || (n.unreadCount > 0 && n.id > opened.topId));

  const toggleHistory = (key: string) =>
    setExpanded((previous) => {
      const next = new Set(previous);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });

  const note = pushFooterNote(push.data, notifications?.channel);
  const footer =
    note === null ? null : (
      <p className="font-mono text-meta text-muted">
        {note} ·{" "}
        <Link to="/settings" onClick={onClose} className="text-fg underline underline-offset-2">
          Settings
        </Link>
      </p>
    );

  return (
    <Drawer title="Notifications" onClose={onClose} footer={footer ?? undefined}>
      <QueryState isPending={inbox.isPending} error={items.length === 0 ? inbox.error : null}>
        {items.length === 0 && silent.length === 0 ? (
          <Muted>No notifications in the last 30 days. Needs input, failed, ready, done and settled events land here.</Muted>
        ) : (
          <div className="flex flex-col gap-2">
            {silent.length === 0 ? null : <section className="border border-line">
              <h3 className="label-caps flex min-h-7 items-center justify-between border-b border-line bg-panel px-3 py-1">Waiting in T3 <span className="tabular text-muted">{silent.length}</span></h3>
              <ul>{silent.map((session) => <li key={`${session.connectionId}:${session.externalSessionId}`} className="border-b border-line last:border-b-0"><SilentWaitingRow session={session} /></li>)}</ul>
            </section>}
            {groupByDay(items).map((g) => (
              <section key={g.label} className="border border-line">
                <h3 className="label-caps flex min-h-7 items-center justify-between gap-3 border-b border-line bg-panel px-3 py-1 text-fg">
                  {g.label}
                  <span className="tabular text-muted">{g.items.length}</span>
                </h3>
                <ul>
                  {g.items.map((n) => (
                    <li key={n.key} className="border-b border-line last:border-b-0">
                      <NotificationRow
                        n={n}
                        unread={isUnread(n)}
                        expanded={expanded.has(n.key)}
                        onToggle={() => toggleHistory(n.key)}
                        onNavigate={onClose}
                      />
                    </li>
                  ))}
                </ul>
              </section>
            ))}
            {inbox.hasNextPage ? (
              <Button className="self-start" busy={inbox.isFetchingNextPage} onClick={() => void inbox.fetchNextPage()}>
                {inbox.isFetchNextPageError ? "Try loading more again" : "Load more"}
              </Button>
            ) : null}
          </div>
        )}
      </QueryState>
      {inbox.error === null ? null : (
        <div className="mt-3 flex flex-col items-start gap-2">
          {items.length === 0 ? null : <ErrorText error={inbox.error} />}
          <Button small busy={inbox.isFetching} onClick={() => void inbox.refetch()}>
            Retry notifications
          </Button>
        </div>
      )}
      {read.error === null ? null : <ErrorText error={read.error} />}
    </Drawer>
  );
}

function NotificationRow({ n, unread, expanded, onToggle, onNavigate }: {
  n: NotificationGroup;
  unread: boolean;
  expanded: boolean;
  onToggle: () => void;
  onNavigate: () => void;
}) {
  const historyId = useId();
  const destination = useNotificationDestination();
  const { name } = useHarnesses();
  const waiting = n.currentState === "needs_input";
  const project = projectOf(n);
  // A ready task still in backlog reads "ready": that is its current state, not a change.
  const ready = startable(n);
  const changed = n.currentState !== null && n.currentState !== n.event && !ready;
  return (
    <div className={cx("border-l-2 px-3 py-2", waiting ? "border-l-need" : "border-l-transparent")}>
      <div className="flex items-center justify-between gap-2.5">
        <div className="flex min-w-0 items-center gap-2">
          {unread ? <span className="size-1.5 shrink-0 bg-fg" role="img" aria-label="Unread" /> : null}
          <Link
            to={notificationPath(n)}
            onClick={onNavigate}
            title={n.title}
            className={cx("min-w-0 truncate text-body hover:underline", unread ? "font-semibold" : "font-medium")}
          >
            {n.title}
          </Link>
        </div>
        <div className="shrink-0">
          {ready ? (
            <NotificationEventBadge event="ready" />
          ) : n.currentState === null ? (
            <Chip className="text-muted">unavailable</Chip>
          ) : (
            <TaskStateBadge state={n.currentState} />
          )}
        </div>
      </div>
      <div className="mt-1 flex items-center justify-between gap-2.5">
        <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 font-mono text-meta text-muted">
          <span className="flex min-w-0 items-center gap-2">
            {project === "" ? null : <><span className="min-w-0 truncate">{project}</span><MetaDot /></>}
            <span title={dateTime(n.createdAt)} className="shrink-0 tabular">
              {relativeTime(n.createdAt)}
            </span>
          </span>
          {changed || n.currentState === null ? (
            <>
              {/* Phone: the historical event starts its own line, so no separator dangles. */}
              <span aria-hidden="true" className="basis-full md:hidden" />
              <span className="flex items-center gap-2">
                <MetaDot className="max-md:hidden" />
                <span>
                  was <NotificationEventLabel event={n.event} />
                </span>
              </span>
            </>
          ) : null}
          {n.eventCount > 1 ? (
            <Button
              small
              variant="ghost"
              className="-ml-1 gap-0.5 px-1 md:px-1"
              onClick={onToggle}
              aria-expanded={expanded}
              aria-controls={historyId}
              aria-label={`${expanded ? "Hide" : "Show"} notification history for ${n.title}`}
            >
              <span className={cx("inline-flex", !expanded && "-rotate-90")}>
                <IconChevronDown size={14} />
              </span>
              <span className="font-mono tabular">{n.eventCount} updates</span>
            </Button>
          ) : null}
        </div>
        <div className="flex flex-wrap items-center justify-end gap-1.5">
          {startable(n) ? <StartButton taskId={n.taskId} /> : null}
          {destination(n) === null ? null : (
            <AppButton small variant={waiting ? "primary" : "secondary"} href={destination(n) ?? ""}>
              Open in {name(n.connectionId)}
            </AppButton>
          )}
        </div>
      </div>
      <div id={historyId} hidden={!expanded}>
        {expanded ? <NotificationHistory groupKey={n.key} /> : null}
      </div>
    </div>
  );
}

function MetaDot({ className }: { className?: string }) {
  return <span aria-hidden="true" className={className}>·</span>;
}

function NotificationEventLabel({ event }: { event: NotificationEvent }) {
  return (
    <span className={cx(toneClass(notificationEventTone(event)), "font-mono text-meta text-[var(--cf)]")}>
      {notificationEventLabel(event)}
    </span>
  );
}

function NotificationHistory({ groupKey }: { groupKey: string }) {
  const history = useNotificationHistory(groupKey);
  const events = history.data?.pages.flatMap((page) => page.notifications) ?? [];
  const unique = [...new Map(events.map((event) => [event.id, event])).values()];
  return (
    <div className="mt-2 flex flex-col gap-2">
      <QueryState isPending={history.isPending} error={unique.length === 0 ? history.error : null}>
        {unique.length === 0 ? <Muted>No notifications in the last 30 days.</Muted> : (
          <ol aria-label="Notification history, newest first" className="ml-[3px] w-[calc(100%-3px)] border-l border-line-strong pl-4">
            {unique.map((event) => (
              <li
                key={event.id}
                className={cx(toneClass(notificationEventTone(event.event)), "relative flex items-baseline justify-between gap-3 py-1")}
              >
                <span aria-hidden="true" className="absolute top-2.5 -left-[19.5px] size-1.5 bg-[var(--c)] outline-3 outline-surface" />
                <NotificationEventLabel event={event.event} />
                <time
                  dateTime={event.createdAt}
                  title={dateTime(event.createdAt)}
                  className="shrink-0 font-mono text-meta-sm tabular text-muted"
                >
                  {relativeTime(event.createdAt)}
                </time>
              </li>
            ))}
          </ol>
        )}
      </QueryState>
      {history.hasNextPage ? (
        <Button small className="self-start" busy={history.isFetchingNextPage} onClick={() => void history.fetchNextPage()}>
          Older updates
        </Button>
      ) : null}
      {history.error === null ? null : (
        <>
          {unique.length === 0 ? null : <ErrorText error={history.error} />}
          <Button small className="self-start" busy={history.isFetching} onClick={() => void history.refetch()}>
            Retry history
          </Button>
        </>
      )}
    </div>
  );
}

/** Current attention only. No event timestamp, history, unread marker or read mutation. */
function SilentWaitingRow({ session }: { session: WaitingSession }) {
  const destination = useNotificationDestination();
  const { name } = useHarnesses();
  const href = destination(session);
  const fallback = `Open in ${name(session.connectionId)}: ${session.externalSessionId}`;
  return <div className="border-l-2 border-l-need px-3 py-2">
    <div className="flex items-center justify-between gap-2.5">
      <p title={session.title} className="min-w-0 truncate text-body font-medium">{session.title}</p>
      <div className="shrink-0"><TaskStateBadge state="needs_input" /></div>
    </div>
    <div className="mt-1 flex items-center justify-between gap-2.5">
      <span className="min-w-0 truncate font-mono text-meta text-muted">{session.projectTitle ?? name(session.connectionId)}</span>
      {href === null ? <span title={fallback} className="min-w-0 truncate text-small text-muted">{fallback}</span> : <AppButton small href={href} variant="primary">Open in {name(session.connectionId)}</AppButton>}
    </div>
  </div>;
}
