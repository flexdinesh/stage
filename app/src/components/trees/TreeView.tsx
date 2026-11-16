import { useCallback, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { TreeDetail } from "../../api/types.ts";
import { connectorPaths, layoutLevels, related, type Rect } from "../../lib/trees.ts";
import { cx } from "../ui.tsx";
import { TreeNode } from "./TreeNode.tsx";

/** Lane for the across segment, px above a band's highest card (inside the band's 20px top padding). */
const LANE = 10;

type Measured = { width: number; height: number; rects: Map<number, Rect> };

const sameRects = (a: Measured, b: Measured) =>
  a.width === b.width &&
  a.height === b.height &&
  a.rects.size === b.rects.size &&
  [...a.rects].every(([id, r]) => {
    const o = b.rects.get(id);
    return o !== undefined && o.x === r.x && o.y === r.y && o.width === r.width && o.height === r.height;
  });

/**
 * Levels top-down as ruled bands (mono LEVEL n label, cards side by side, wrapping). On laptop, 1px orthogonal
 * connectors are drawn in an SVG behind the opaque cards from measured card boxes; hovering or focusing a card inks its
 * edges and dims every card that is not a direct need or dependent. Phone stacks cards and each names its needs.
 */
export function TreeView({
  tree,
  selected,
  onSelect,
}: {
  tree: TreeDetail;
  selected: number | null;
  onSelect: (id: number) => void;
}) {
  const levels = useMemo(() => layoutLevels(tree.tasks), [tree.tasks]);
  const byId = useMemo(() => new Map(tree.tasks.map((t) => [t.id, t])), [tree.tasks]);
  const [hover, setHover] = useState<number | null>(null);
  const [focus, setFocus] = useState<number | null>(null);
  const hot = hover ?? focus;
  const near = useMemo(() => (hot === null ? null : related(tree.tasks, hot)), [tree.tasks, hot]);

  const container = useRef<HTMLDivElement>(null);
  const nodes = useRef(new Map<number, HTMLLIElement>());
  const [measured, setMeasured] = useState<Measured>({ width: 0, height: 0, rects: new Map() });

  const measure = useCallback(() => {
    const el = container.current;
    if (el === null) return;
    const box = el.getBoundingClientRect();
    const rects = new Map<number, Rect>();
    for (const [id, node] of nodes.current) {
      const r = node.getBoundingClientRect();
      rects.set(id, { x: r.left - box.left, y: r.top - box.top, width: r.width, height: r.height });
    }
    const next = { width: box.width, height: box.height, rects };
    setMeasured((prev) => (sameRects(prev, next) ? prev : next));
  }, []);

  // Every commit re-measures (cards can move without resizing: a reorder, a new member); the observer catches resizes of
  // the view and of each card. setMeasured bails out when nothing moved.
  useLayoutEffect(() => measure());
  const observer = useRef<ResizeObserver | null>(null);
  useLayoutEffect(() => {
    const el = container.current;
    if (el === null) return;
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    for (const node of nodes.current.values()) ro.observe(node);
    observer.current = ro;
    return () => {
      ro.disconnect();
      observer.current = null;
    };
  }, [measure]);

  const paths = useMemo(() => connectorPaths(tree.tasks, measured.rects, LANE), [tree.tasks, measured.rects]);
  const touches = (p: { from: number; to: number }) => hot !== null && (p.from === hot || p.to === hot);
  // Inked edges paint last so they sit over the plain ones they cross.
  const ordered = paths.toSorted((a, b) => Number(touches(a)) - Number(touches(b)));

  const onHot = useCallback((kind: "hover" | "focus", id: number | null) => (kind === "hover" ? setHover(id) : setFocus(id)), []);
  // One stable ref callback for every card (React 19 cleanup), keyed by its data-node id.
  const nodeRef = useCallback((el: HTMLLIElement | null) => {
    if (el === null) return;
    const id = Number(el.dataset.node);
    nodes.current.set(id, el);
    observer.current?.observe(el);
    return () => {
      if (nodes.current.get(id) === el) nodes.current.delete(id);
      observer.current?.unobserve(el);
    };
  }, []);

  return (
    <div ref={container} className="relative">
      <svg
        aria-hidden
        width={measured.width}
        height={measured.height}
        className="pointer-events-none absolute inset-0 z-0 max-md:hidden"
        fill="none"
        strokeWidth={1}
        shapeRendering="crispEdges"
      >
        {ordered.map((p) => (
          <path
            key={`${p.from}-${p.to}`}
            d={p.d}
            className={cx(
              "transition-[stroke,opacity] duration-150 motion-reduce:transition-none",
              touches(p) ? "stroke-fg" : "stroke-line-strong",
              hot !== null && !touches(p) && "opacity-45",
            )}
          />
        ))}
      </svg>
      {levels.map((band, i) => (
        <section
          key={i}
          aria-label={`Level ${i + 1}`}
          className="grid gap-2.5 border-b border-line px-3 pt-4 pb-4 last:border-b-0 md:grid-cols-[72px_minmax(0,1fr)] md:gap-4 md:pt-5 md:pb-5"
        >
          <h3 className="label-caps md:pt-1">Level {i + 1}</h3>
          <ul className="flex flex-col gap-2 md:flex-row md:flex-wrap md:gap-x-6 md:gap-y-5">
            {band.map((id) => {
              const task = byId.get(id);
              if (task === undefined) return null;
              return (
                <TreeNode
                  key={id}
                  task={task}
                  byId={byId}
                  selected={selected === id}
                  dimmed={near !== null && id !== hot && !near.has(id)}
                  onSelect={onSelect}
                  onHot={onHot}
                  nodeRef={nodeRef}
                />
              );
            })}
          </ul>
        </section>
      ))}
    </div>
  );
}

/** Loading placeholder: three bands of pulsing cards. */
export function TreeViewSkeleton() {
  return (
    <div aria-busy="true" aria-label="Loading tree">
      {[2, 3, 1].map((n, i) => (
        <div key={i} className="grid gap-2.5 border-b border-line px-3 py-5 last:border-b-0 md:grid-cols-[72px_minmax(0,1fr)] md:gap-4">
          <div className="h-3 w-14 animate-pulse bg-panel" />
          <div className="flex flex-col gap-2 md:flex-row md:gap-6">
            {Array.from({ length: n }, (_, j) => (
              <div key={j} className="h-[116px] animate-pulse border border-line bg-panel md:w-[216px]" />
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}
