import type { TreeDetail } from "../../api/types.ts";
import { JournalFeed } from "../Journal.tsx";
import { Panel } from "../ui.tsx";

/** Durable tree events include departed members; task links always open their detail. */
export function TreeActivity({ tree, className }: { tree: TreeDetail; className?: string | undefined }) {
  return (
    <Panel title="Activity" flush className={className}>
      <JournalFeed owner={{ treeId: tree.id }} query={{ kind: "events", limit: 12 }} compact />
    </Panel>
  );
}
