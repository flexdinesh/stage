import { useMemo } from "react";
import type { Task, TreeDetail } from "../../api/types.ts";
import { dateTime } from "../../lib/format.ts";
import { bandState, edgeState, graphLayout, inState, type BandState, type EdgeState } from "../../lib/trees.ts";
import { IdTag, Meta, TextButton, TextLink, cx, needEdgeClass } from "../ui.tsx";
import { NodeChip } from "./TreeNode.tsx";

const SIZE = { nodeW: 224, nodeH: 80, gapX: 16, gapY: 32 };
/** Step label column on the left of every band. */
const GUTTER = 88;
/** Space around the graph inside the bands (half a level gap, so the first and last bands frame their row evenly). */
const PAD = SIZE.gapY / 2;

const BAND_TEXT: Record<BandState, string> = { now: "now", done: "done", waits: "waits" };

const EDGE_CLASS: Record<EdgeState, string> = {
  met: "stroke-fg",
  waiting: "stroke-muted opacity-60",
  failed: "stroke-bad",
};

/**
 * The tree as a top-down layered graph: one band per level (STEP n, read top to bottom as the execution order; a band
 * with live or ready work reads "now" and is tinted), boxes per member, arrows from each need to its dependents. A met
 * need's arrow is solid Ink, a waiting one dashed, a failed one dashed red. A box selects the task on lg+ (side panel)
 * and opens it below lg, like the list's title.
 */
export function TreeGraph({
  tree,
  selected,
  onSelect,
}: {
  tree: TreeDetail;
  selected: number | null;
  onSelect: (id: number) => void;
}) {
  const layout = useMemo(() => graphLayout(tree.tasks, SIZE), [tree.tasks]);
  const byId = useMemo(() => new Map(tree.tasks.map((t) => [t.id, t])), [tree.tasks]);
  const bands = useMemo(
    () => layout.levels.map((ids) => ids.flatMap((id) => byId.get(id) ?? [])),
    [layout.levels, byId],
  );
  const height = layout.height + PAD * 2;
  const band = SIZE.nodeH + SIZE.gapY;

  return (
    <div className="overflow-x-auto">
      <div className="relative" style={{ height, minWidth: GUTTER + layout.width + PAD * 2 }}>
        {bands.map((members, l) => {
          const state = bandState(members);
          return (
            <div
              key={l}
              aria-hidden
              className={cx("absolute inset-x-0 flex flex-col justify-center gap-0.5 pl-3", l > 0 && "border-t border-line", state === "now" && "bg-run/5")}
              style={{ top: l * band, height: band }}
            >
              <span className="label-caps text-fg">Step {l + 1}</span>
              <span className={cx("font-mono text-meta-sm", state === "now" ? "text-run" : "text-muted")}>{BAND_TEXT[state]}</span>
              {members.length > 1 ? <Meta small className="tabular">{members.length} parallel</Meta> : null}
            </div>
          );
        })}
        <div className="absolute inset-y-0 right-0" style={{ left: GUTTER }}>
          <div className="relative mx-auto h-full" style={{ width: layout.width + PAD * 2 }}>
            <svg
              aria-hidden
              className="absolute overflow-visible"
              style={{ left: PAD, top: PAD }}
              width={layout.width}
              height={layout.height}
              fill="none"
            >
              {layout.edges.map((e) => {
                const need = byId.get(e.from);
                const state = need === undefined ? "waiting" : edgeState(need);
                const dash = state === "met" ? undefined : "4 3";
                return (
                  <g key={`${e.from}-${e.to}`} className={EDGE_CLASS[state]} strokeWidth={state === "met" ? 1.5 : 1.25}>
                    <path d={e.path} strokeDasharray={dash} />
                    <path d={e.arrow} />
                  </g>
                );
              })}
            </svg>
            <ol aria-label="Tree tasks as a graph, by step" className="absolute" style={{ left: PAD, top: PAD }}>
              {layout.nodes.map((n) => {
                const task = byId.get(n.id);
                if (task === undefined) return null;
                return (
                  <GraphNode key={n.id} task={task} step={n.level + 1} x={n.x} y={n.y} selected={selected === n.id} onSelect={onSelect} />
                );
              })}
            </ol>
          </div>
        </div>
      </div>
    </div>
  );
}

/**
 * One member box: state chip and time in state, then `#id` and title, then project. Running takes the Sky border, unstarted backlog
 * a dashed one, needs input the One Yellow Edge, the selected box the inset Ink outline.
 */
function GraphNode({
  task,
  step,
  x,
  y,
  selected,
  onSelect,
}: {
  task: Task;
  step: number;
  x: number;
  y: number;
  selected: boolean;
  onSelect: (id: number) => void;
}) {
  const when = inState(task);
  const titleCls = "truncate text-ui font-medium after:absolute after:inset-0";
  const label = `Step ${step}: #${task.id} ${task.title} · Project: ${task.projectTitle}`;
  return (
    <li
      data-node={task.id}
      className={cx(
        "absolute flex flex-col justify-center gap-1 border bg-surface px-2",
        task.state === "running" || task.state === "launching" ? "border-run" : "border-line-strong",
        task.state === "backlog" && "border-dashed",
        task.state === "needs_input" && needEdgeClass(true),
        selected && "outline outline-1 -outline-offset-2 outline-fg",
      )}
      style={{ left: x, top: y, width: SIZE.nodeW, height: SIZE.nodeH }}
    >
      <span className="flex min-w-0 items-center gap-2">
        <NodeChip task={task} className={task.state === "backlog" ? "border-dashed" : undefined} />
        <span
          className={cx("ml-auto truncate font-mono text-meta-sm tabular", task.state === "needs_input" ? "text-need-fg" : "text-muted")}
          title={dateTime(when.at)}
        >
          {when.text}
        </span>
      </span>
      <span className="flex min-w-0 items-baseline gap-1.5">
        <IdTag id={task.id} />
        <TextButton aria-pressed={selected} aria-label={label} title={task.title} onClick={() => onSelect(task.id)} className={cx(titleCls, "max-lg:hidden")}>
          {task.title}
        </TextButton>
        <TextLink to={`/tasks/${task.id}`} aria-label={label} title={task.title} className={cx(titleCls, "min-w-0 text-left lg:hidden")}>
          {task.title}
        </TextLink>
      </span>
      <span className="truncate font-mono text-meta text-fg" title={task.projectTitle}>{task.projectTitle}</span>
    </li>
  );
}
