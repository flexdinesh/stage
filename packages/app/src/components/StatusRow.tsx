import { Link } from "react-router";
import { useHarnesses, useMachines } from "../api/queries.ts";
import { useConnectionStatuses } from "../harnesses/index.ts";
import { machineSetupState } from "../lib/machines.ts";
import { connectionStatusItem, statusTargets, unreachableDetail, type StatusDetail, type StatusView } from "../lib/connections.ts";
import { statusGroups, statusRow, type StatusItem, type StatusKind, type StatusRow as Row, type StatusTone } from "../lib/status.ts";
import { IconCheck, IconPlug, IconRefresh, IconSettings, IconTriangleAlert } from "./icons.tsx";
import { Button, InlineError, Marker, Meta, PanelHeader, PopoverPanel, Row as ListRow, RowList, cx, rowButtonClass, toneClass, usePopover, type IconComponent } from "./ui.tsx";

/** Detail per item key, for the panel. */
type Details = Record<string, StatusDetail>;

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
  const reads = useConnectionStatuses(
    targets.filter((t) => machineSetupState(t.machine) === "ready"),
    harnesses.kindName,
  );
  const live = new Map<number, StatusView>(reads.map(({ connection, view }) => [connection.connectionId, view]));
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

