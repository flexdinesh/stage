import type { TreeDetail } from "../../api/types.ts";
import { JournalFeed } from "../Journal.tsx";
import { Panel } from "../ui.tsx";

/** Durable tree events include departed members; task links always open their detail. */
export function TreeActivity({ tree }: { tree: TreeDetail }) {
  return (
    <Panel title="Activity" flush>
      <JournalFeed owner={{ treeId: tree.id }} query={{ kind: "events" }} />
    </Panel>
  );
}
