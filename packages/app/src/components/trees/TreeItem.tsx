import { useId, useState } from "react";
import { useNavigate } from "react-router";
import { useTree } from "../../api/queries.ts";
import type { TreeSummary } from "../../api/types.ts";
import { ErrorText, Row } from "../ui.tsx";
import { TreeRow } from "./TreeRow.tsx";
import { TreeView, TreeViewSkeleton } from "./TreeView.tsx";

/** A tree row (Trees view and Dashboard) whose task count expands its members in level order. */
export function TreeItem({ tree }: { tree: TreeSummary }) {
  const [open, setOpen] = useState(false);
  const membersId = useId();
  return (
    <Row bare>
      <TreeRow tree={tree} tasks={{ open, onToggle: () => setOpen((o) => !o), controls: membersId }} />
      <div id={membersId} hidden={!open}>
        {open ? <TreeMembers id={tree.id} /> : null}
      </div>
    </Row>
  );
}

function TreeMembers({ id }: { id: number }) {
  const tree = useTree(id);
  const navigate = useNavigate();
  return (
    <div className="border-t border-line bg-bg">
      {tree.isPending ? (
        <TreeViewSkeleton />
      ) : tree.error !== null || tree.data === undefined ? (
        <div className="p-3">
          <ErrorText error={tree.error} />
        </div>
      ) : (
        <TreeView tree={tree.data} selected={null} onSelect={(task) => void navigate(`/tasks/${task}`)} />
      )}
    </div>
  );
}
