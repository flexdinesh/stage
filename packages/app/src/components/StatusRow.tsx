import { Link } from "react-router";
import { useConnectionStatuses, useHarnesses, useMachines } from "../api/queries.ts";
import type { OpenCodeStatus, T3Status } from "../api/types.ts";
import { relativeTime } from "../lib/format.ts";
import { machineSetupState } from "../lib/machines.ts";
import { t3Identity } from "../lib/onboarding.ts";
import {
  connectionStatusItem,
  harnessSummary,
  openCodeErrorText,
  openCodeSummary,
  statusRead,
  statusTargets,
  t3ErrorText,
  t3Summary,
  unreachableDetail,
  type HarnessSummary,
  type StatusRead,
} from "../lib/connections.ts";
import { serviceHostText, signInText } from "../lib/opencode.ts";
import { statusGroups, statusRow, type StatusItem, type StatusKind, type StatusRow as Row, type StatusTone } from "../lib/status.ts";
import { IconCheck, IconPlug, IconRefresh, IconSettings, IconTriangleAlert } from "./icons.tsx";
import { Button, InlineError, Marker, Meta, PanelHeader, PopoverPanel, Row as ListRow, RowList, cx, rowButtonClass, toneClass, usePopover, type IconComponent } from "./ui.tsx";

/** An item's detail lines and error, for the panel. */
type Detail = { lines: string[]; error: string | null };

/** Detail per item key, for the panel. */
type Details = Record<string, Detail>;

function t3Detail(read: StatusRead<T3Status>): Detail {
  const d = read.data;
  const expires = d?.tokenExpiresAt ?? null;
  return {
    lines: [
      [d === undefined ? null : t3Identity(d), d?.t3Url ?? null].filter((p) => p !== null && p !== "").join(" · "),
      [
        d?.lastFullSyncAt == null ? null : `synced ${relativeTime(d.lastFullSyncAt)}`,
        expires === null ? null : `token ${d?.state === "expired" ? "expired" : "expires"} ${relativeTime(expires)}`,
      ]
        .filter((p) => p !== null)
        .join(" · "),
    ].filter((l) => l !== ""),
    error: t3ErrorText(read),
  };
}

function openCodeDetail(read: StatusRead<OpenCodeStatus>): Detail {
  const d = read.data;
  return {
    lines: d === undefined ? [] : [[d.endpoint === "" ? null : serviceHostText(d), signInText(d)].filter((p) => p !== null).join(" · "), `checked ${relativeTime(d.checkedAt ?? null)}`],
    error: openCodeErrorText(read),
  };
}

/**
 * The status sentence, kind and tone, and each item's detail lines: every set-up connection on every machine, read on
 * its machine. A machine that cannot answer counts its connections as problems; with two or more machines the panel
 * has a section per machine.
 */
export function useStatus(): { row: Row; details: Details } {
  const harnesses = useHarnesses();
  const { machines, data } = useMachines();
  // Until connections and machines load, keep checking.
  const loaded = harnesses.loaded && data !== undefined;
  const targets = loaded ? statusTargets(machines, harnesses.connection) : [];
  const reads = useConnectionStatuses(targets.filter((t) => machineSetupState(t.machine) === "ready"));
  const live = new Map<number, { summary: HarnessSummary; detail: Detail }>();
  for (const { connection, query } of reads.t3) {
    const read = statusRead(query);
    live.set(connection.connectionId, { summary: t3Summary(read), detail: t3Detail(read) });
  }
  for (const { connection, query } of reads.openCode) {
    const read = statusRead(query);
    live.set(connection.connectionId, { summary: openCodeSummary(read), detail: openCodeDetail(read) });
  }
  for (const { connection, query } of reads.other) {
    const read = statusRead(query);
    const reason = read.data?.reason ?? read.data?.lastError ?? "";
    live.set(connection.connectionId, {
      summary: harnessSummary(read, connection.harness, harnesses.kindName(connection.harness)),
      detail: { lines: reason === "" ? [] : [reason], error: read.error },
    });
  }
  const grouped = machines.length > 1;
  const items: StatusItem[] = [];
  const details: Details = {};
  for (const target of targets) {
    const entry = live.get(target.connectionId);
    const item = connectionStatusItem(target, harnesses.kindName(target.harness), entry?.summary, grouped);
    items.push(item);
    details[item.key] = entry?.detail ?? { lines: unreachableDetail(target.machine), error: null };
  }
  return { row: statusRow(items, { checking: !loaded, empty: "Connect a harness" }), details };
}

const TEXT: Record<StatusTone, string> = { ok: "text-muted", idle: "text-muted", need: "text-need-fg", bad: "text-bad" };

/** One section per group; each line links to Settings. `touch` sizes rows for the phone. */
export function StatusSections({ row, details, touch = false, onPick }: { row: Row; details: Details; touch?: boolean; onPick?: () => void }) {
  const groups = statusGroups(row.items);
  if (groups.length === 0) {
    return (
      <p className={cx("flex items-center gap-2 px-3 text-ui text-muted", touch ? "min-h-11" : "min-h-9")}>
        <Marker tone={row.tone} />
        {row.kind === "checking" ? "Checking connections" : "No harness connected yet"}
      </p>
    );
  }
  return groups.map(({ group, items }) => (
    <section key={group} aria-label={group}>
      <PanelHeader title={group} meta={items.length} />
      <RowList>
        {items.map((i) => {
          const d = details[i.key];
          return (
            <ListRow key={i.key} bare>
              <Link
                to="/settings/connections"
                onClick={onPick}
                className="grid grid-cols-[6px_minmax(0,1fr)_auto] items-center gap-x-2 gap-y-0.5 px-3 py-row hover:bg-panel"
              >
                <Marker tone={i.tone} />
                <span className={cx("truncate text-ui", i.tone === "need" || i.tone === "bad" ? TEXT[i.tone] : "text-fg")}>{i.name}</span>
                <span className={cx("font-mono text-meta", TEXT[i.tone])}>{i.state}</span>
                {d?.lines.map((l) => (
                  <Meta key={l} small className="col-start-2 col-end-4 truncate">
                    {l}
                  </Meta>
                ))}
                {d?.error == null ? null : <InlineError className="col-start-2 col-end-4 line-clamp-2">{d.error}</InlineError>}
              </Link>
            </ListRow>
          );
        })}
      </RowList>
    </section>
  ));
}

const ICON: Record<StatusKind, { icon: IconComponent; className: string }> = {
  ok: { icon: IconCheck, className: "text-ok" },
  need: { icon: IconTriangleAlert, className: "text-need-fg" },
  bad: { icon: IconTriangleAlert, className: "text-bad" },
  checking: { icon: IconRefresh, className: "text-muted" },
  empty: { icon: IconPlug, className: "text-muted" },
};

/**
 * Sidebar bottom-row status button: one icon for everything Stage depends on (a check when all is well, an alert in
 * the worst tone, refresh while checking, a plug with nothing set up), and a toned count from two problems up. Its
 * name and title carry the sentence ("T3 pairing expired"). Click toggles the details panel above the row; the
 * wrapper stays static so the panel anchors to the row (the nearest positioned ancestor).
 */
export function StatusButton() {
  const { row, details } = useStatus();
  const popover = usePopover();
  const { icon: Icon, className } = ICON[row.kind];
  const count = row.problems > 1 ? row.problems : null;
  const label = `Status: ${row.text}`;
  return (
    <div {...popover.wrapProps}>
      <Button
        variant="secondary"
        {...popover.triggerProps}
        title={row.text}
        aria-label={label}
        className={cx(count === null ? "w-control px-0" : "gap-1.5 px-2", popover.open && "bg-panel")}
      >
        <span className={cx("flex", className)}>
          <Icon size={16} />
        </span>
        {count === null ? null : <span className={cx("chip px-1.5 text-meta-sm leading-4 tabular", toneClass(row.tone))}>{count}</span>}
      </Button>
      <PopoverPanel popover={popover} label="Status" className="absolute bottom-full left-3 mb-1 w-[300px]">
        <StatusSections row={row} details={details} onPick={popover.close} />
        <Link to="/settings/connections" onClick={popover.close} className={cx(rowButtonClass(), "border-t border-line text-small text-fg")}>
          <IconSettings size={14} />
          Open settings
        </Link>
      </PopoverPanel>
    </div>
  );
}

/** The More tab's marker tone: only when something needs attention. */
export function useStatusAlert(): StatusTone | null {
  const { row } = useStatus();
  return row.tone === "need" || row.tone === "bad" ? row.tone : null;
}

