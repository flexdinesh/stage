import { hashKey, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { Link, useNavigate } from "react-router";
import { api } from "../api/client.ts";
import { keys, latestNotificationsKey, useNotificationInbox, useNotifications } from "../api/queries.ts";
import type { Notification, NotificationPage } from "../api/types.ts";
import { dateTime, relativeTime } from "../lib/format.ts";
import { closeSystemNotifications, getPushState } from "../lib/push.ts";
import { IconBell, IconClose } from "./icons.tsx";
import { TaskStateBadge, taskStateLabel } from "./StateBadge.tsx";
import { AppButton, Button, Drawer, ErrorText, Muted, QueryState, cx } from "./ui.tsx";

const TOAST_MS = 8000;
const MAX_TOASTS = 3;

/** A needs-input notification whose task/thread is still waiting (what the app badge counts). */
const stillWaiting = (n: Notification) => n.event === "needs_input" && n.currentState === "needs_input";

const projectOf = (n: Notification) => n.projectTitle ?? (n.taskId === null ? "external thread" : "");

// ---- Service worker → app ----

/** The service worker posts `{type: "push"}` on every push: refetch at once instead of waiting for the poll. */
export function usePushRefresh() {
  const qc = useQueryClient();
  useEffect(() => {
    if (!("serviceWorker" in navigator)) return;
    function onMessage(e: MessageEvent<unknown>) {
      const data = e.data;
      if (typeof data !== "object" || data === null || !("type" in data) || data.type !== "push") return;
      for (const queryKey of [keys.notifications, keys.badge, keys.tasks, keys.task, keys.threads, keys.stats]) {
        void qc.invalidateQueries({ queryKey });
      }
    }
    navigator.serviceWorker.addEventListener("message", onMessage);
    return () => navigator.serviceWorker.removeEventListener("message", onMessage);
  }, [qc]);
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
      const prev = baseline;
      baseline = Math.max(prev ?? 0, maxId(items));
      const visible = document.visibilityState === "visible";
      if (prev === null || !visible || skipNext) {
        if (visible) skipNext = false;
        return;
      }
      const fresh = items.filter((n) => n.id > prev).toSorted((a, b) => a.id - b.id);
      if (fresh.length > 0) setToasts((t) => [...t, ...fresh]);
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
      className="pointer-events-none fixed inset-x-3 bottom-[calc(56px+env(safe-area-inset-bottom)+8px)] z-[45] flex flex-col gap-2 md:inset-x-auto md:right-6 md:bottom-6 md:w-[360px]"
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

function Toast({ n, onDismiss }: { n: Notification; onDismiss: () => void }) {
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
        "toast-in pointer-events-auto flex flex-col gap-1.5 border border-line-strong bg-surface py-2.5 pr-3",
        sticky ? "border-l-2 border-l-need pl-[11px]" : "pl-3",
      )}
    >
      <div className="flex items-center justify-between gap-2">
        <TaskStateBadge state={n.event} />
        <Button variant="ghost" small onClick={onDismiss} aria-label="Dismiss notification" className="-mr-1.5 w-6 px-0">
          <IconClose />
        </Button>
      </div>
      <Link to={n.url} onClick={onDismiss} className="line-clamp-2 text-body font-medium break-words hover:underline">
        {n.title}
      </Link>
      <div className="flex flex-wrap items-center justify-between gap-x-2.5 gap-y-1.5">
        <span className="min-w-0 truncate font-mono text-meta text-muted">
          {project === "" ? "" : `${project} · `}
          <span className="tabular">{relativeTime(n.createdAt)}</span>
        </span>
        <div className="flex items-center gap-1.5">
          <ToastView n={n} onDismiss={onDismiss} />
          {n.t3DeepLink === null ? null : (
            <AppButton small variant={stillWaiting(n) ? "primary" : "secondary"} href={n.t3DeepLink}>
              Open in app
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
        void navigate(n.url);
      }}
    >
      View
    </Button>
  );
}

// ---- Drawer ----

type Group = { label: string; items: Notification[] };

function groupByDay(items: Notification[], now: Date = new Date()): Group[] {
  const today = new Date(now);
  today.setHours(0, 0, 0, 0);
  const yesterday = new Date(today);
  yesterday.setDate(yesterday.getDate() - 1);
  const groups: Group[] = [
    { label: "Today", items: [] },
    { label: "Yesterday", items: [] },
    { label: "Earlier", items: [] },
  ];
  for (const n of items) {
    const t = new Date(n.createdAt).getTime();
    const g = t >= today.getTime() ? groups[0] : t >= yesterday.getTime() ? groups[1] : groups[2];
    g?.items.push(n);
  }
  return groups.filter((g) => g.items.length > 0);
}

const PUSH_KEY = ["pushState"];

/**
 * Opening reads everything shown: unread markers stay (from a snapshot) until
 * the drawer closes; anything that arrives while open is read on close.
 */
export function NotificationsDrawer({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const inbox = useNotificationInbox();
  const push = useQuery({ queryKey: PUSH_KEY, queryFn: getPushState, refetchInterval: false });
  // Snapshot at open: which rows were unread, and the newest id then.
  const [opened, setOpened] = useState<{ unread: ReadonlySet<number>; topId: number; markRead: boolean } | null>(null);
  const read = useMutation({
    mutationFn: api.markNotificationsRead,
    onSettled: () => qc.invalidateQueries({ queryKey: keys.notifications }),
  });
  const readMutate = read.mutate;

  // Opening the inbox clears all system notifications.
  useEffect(() => {
    void closeSystemNotifications();
  }, []);

  const items =inbox.data?.pages.flatMap((p) => p.notifications) ?? [];
  const firstPage = inbox.data?.pages[0];
  if (firstPage !== undefined && opened === null) {
    const unread = new Set(firstPage.notifications.filter((n) => n.readAt === null).map((n) => n.id));
    setOpened({ unread, topId: firstPage.notifications[0]?.id ?? 0, markRead: firstPage.unread > 0 });
  }
  useEffect(() => {
    if (opened?.markRead === true && opened.topId > 0) readMutate(opened.topId);
  }, [opened, readMutate]);

  // On close: read whatever arrived while open.
  const close = useRef({ newest: 0, topId: Number.MAX_SAFE_INTEGER });
  useEffect(() => {
    close.current = { newest: items[0]?.id ?? 0, topId: opened?.topId ?? Number.MAX_SAFE_INTEGER };
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
    [qc],
  );

  const isUnread = (n: Notification) =>
    opened !== null && (opened.unread.has(n.id) || (n.readAt === null && n.id > opened.topId));

  const footer =
    push.data === undefined || push.data === "on" ? null : (
      <p className="font-mono text-meta text-muted">
        Push is off on this device ·{" "}
        <Link to="/settings" onClick={onClose} className="text-fg underline underline-offset-2">
          Settings
        </Link>
      </p>
    );

  return (
    <Drawer title="Notifications" onClose={onClose} footer={footer ?? undefined}>
      <QueryState isPending={inbox.isPending} error={inbox.error}>
        {items.length === 0 ? (
          <Muted>No notifications in the last 30 days. Needs input, done and failed events land here.</Muted>
        ) : (
          <div className="flex flex-col gap-3.5">
            {groupByDay(items).map((g) => (
              <section key={g.label} className="border border-line">
                <h3 className="label-caps flex h-8 items-center border-b border-line bg-panel px-3 text-fg">{g.label}</h3>
                <ul>
                  {g.items.map((n) => (
                    <li key={n.id} className="border-b border-line last:border-b-0">
                      <NotificationRow n={n} unread={isUnread(n)} onNavigate={onClose} />
                    </li>
                  ))}
                </ul>
              </section>
            ))}
            {inbox.hasNextPage ? (
              <Button className="self-start" busy={inbox.isFetchingNextPage} onClick={() => void inbox.fetchNextPage()}>
                Load more
              </Button>
            ) : null}
          </div>
        )}
      </QueryState>
      {read.error === null ? null : <ErrorText error={read.error} />}
    </Drawer>
  );
}

function NotificationRow({ n, unread, onNavigate }: { n: Notification; unread: boolean; onNavigate: () => void }) {
  const waiting = stillWaiting(n);
  const project = projectOf(n);
  const changed = n.currentState !== null && n.currentState !== n.event;
  return (
    <div
      className={cx(
        "grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-2.5 gap-y-1.5 border-l-2 px-3 py-2.5 md:grid-cols-[118px_minmax(0,1fr)_auto] md:gap-x-3.5 md:py-2",
        waiting ? "border-l-need" : "border-l-transparent",
      )}
    >
      <div className="order-2 justify-self-end md:order-1 md:justify-self-start">
        <TaskStateBadge state={n.event} />
      </div>

      <div className="order-1 min-w-0 md:order-2">
        <div className="flex items-baseline gap-2">
          {unread ? <span className="size-1.5 shrink-0 translate-y-[-1px] bg-fg" aria-label="Unread" /> : null}
          <Link
            to={n.url}
            onClick={onNavigate}
            className={cx("text-body break-words hover:underline", unread ? "font-semibold" : "font-medium")}
          >
            {n.title}
          </Link>
        </div>
        <div className="mt-0.5 flex flex-wrap gap-x-2.5 gap-y-0.5 font-mono text-meta text-muted">
          {project === "" ? null : <span>{project}</span>}
          <span title={dateTime(n.createdAt)} className="tabular">
            {relativeTime(n.createdAt)}
          </span>
          {waiting ? <span className="text-need-fg">still waiting</span> : null}
          {changed && n.currentState !== null ? <span>now {taskStateLabel(n.currentState)}</span> : null}
        </div>
      </div>

      {n.t3DeepLink === null ? null : (
        <div className="order-3 col-span-2 flex items-center gap-1.5 md:col-span-1 md:justify-end">
          <AppButton small variant={waiting ? "primary" : "secondary"} href={n.t3DeepLink}>
            Open in app
          </AppButton>
        </div>
      )}
    </div>
  );
}
