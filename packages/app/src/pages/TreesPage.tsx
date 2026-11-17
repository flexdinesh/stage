import { useSearchParams } from "react-router";
import { useTrees } from "../api/queries.ts";
import type { TreeView } from "../api/types.ts";
import { TreeRow } from "../components/trees/TreeRow.tsx";
import { PageHeader, Panel, QueryState, Segmented } from "../components/ui.tsx";

const VIEWS: { value: TreeView; label: string }[] = [
  { value: "active", label: "Active" },
  { value: "done", label: "Done" },
  { value: "archived", label: "Archived" },
  { value: "all", label: "All" },
];

const TITLES: Record<TreeView, string> = {
  active: "Active trees",
  done: "Done trees",
  archived: "Archived trees",
  all: "All trees",
};

const EMPTY: Record<TreeView, string> = {
  active: "No active trees. A tree forms when a task needs another (agents build them with create_tree). Done and archived trees have their own views.",
  done: "No done trees. A tree is done once every task settles, or when you mark it done from its More menu.",
  archived: "No archived trees. Archive a tree from its More menu to keep it out of the way without deleting it.",
  all: "No trees yet. A tree forms when a task needs another; agents build them with create_tree.",
};

const parseView = (v: string | null): TreeView => VIEWS.find((o) => o.value === v)?.value ?? "active";

export function TreesPage() {
  const [params, setParams] = useSearchParams();
  const view = parseView(params.get("view"));
  const trees = useTrees({ status: view });
  const list = trees.data?.trees ?? [];
  const setView = (value: TreeView) => {
    const next = new URLSearchParams(params);
    if (value === "active") next.delete("view");
    else next.set("view", value);
    setParams(next, { replace: true });
  };
  return (
    <>
      <PageHeader title="Trees" meta="Tasks that wait on each other · recent activity first" />
      <div className="flex flex-col gap-3.5">
        <div>
          <Segmented label="Tree view" options={VIEWS} value={view} onChange={setView} />
        </div>
        <QueryState isPending={trees.isPending} error={trees.error}>
          <Panel title={TITLES[view]} meta={trees.isPlaceholderData ? "updating" : String(list.length)} flush>
            {list.length === 0 ? (
              <p className="px-3 py-2.5 text-small text-muted">{EMPTY[view]}</p>
            ) : (
              <ul>
                {list.map((t) => (
                  <li key={t.id} className="border-b border-line last:border-b-0">
                    <TreeRow tree={t} />
                  </li>
                ))}
              </ul>
            )}
          </Panel>
        </QueryState>
      </div>
    </>
  );
}
