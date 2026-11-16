import { useSearchParams } from "react-router";
import { useTrees } from "../api/queries.ts";
import { TreeRow } from "../components/trees/TreeRow.tsx";
import { PageHeader, Panel, QueryState, Segmented } from "../components/ui.tsx";

const VIEWS: { value: "active" | "all"; label: string }[] = [
  { value: "active", label: "Active" },
  { value: "all", label: "All" },
];

export function TreesPage() {
  const [params, setParams] = useSearchParams();
  const view = params.get("view") === "all" ? "all" : "active";
  const trees = useTrees(view === "all" ? { all: true } : {});
  const list = trees.data?.trees ?? [];
  const setView = (value: "active" | "all") => {
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
          <Panel
            title={view === "active" ? "Active trees" : "All trees"}
            meta={trees.isPlaceholderData ? "updating" : String(list.length)}
            flush
          >
            {list.length === 0 ? (
              <p className="px-3 py-2.5 text-small text-muted">
                {view === "active"
                  ? "No active trees. A tree forms when a task needs another (agents build them with create_tree). Switch to All for completed trees."
                  : "No trees yet. A tree forms when a task needs another; agents build them with create_tree."}
              </p>
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
