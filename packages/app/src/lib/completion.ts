import type { Acceptance, CompletionPreview, Task } from "../api/types.ts";

type Dependent = CompletionPreview["dependents"][number];

/** Dependents completing the task would start: unclaimed auto-mode ones, while the task's tree (if any) is open. */
export function autoStarts(preview: Pick<CompletionPreview, "dependents">, task: Pick<Task, "treeStatus">): Dependent[] {
  if (task.treeStatus !== null && task.treeStatus !== "open") return [];
  return preview.dependents.filter((d) => !d.claimed && d.startMode === "auto");
}

/** "Starts #12, #13" (ids deduplicated, first-seen order); "" when nothing starts. */
export function startsText(dependents: readonly Pick<Dependent, "taskId">[]): string {
  const ids = [...new Set(dependents.map((d) => d.taskId))];
  return ids.length === 0 ? "" : `Starts ${ids.map((id) => `#${id}`).join(", ")}`;
}

/** Accepted result line: who completed it and what stays fixed. */
export function provenanceText(provenance: Acceptance["provenance"]): string {
  switch (provenance) {
    case "historical_settlement":
      return "Migrated from historical T3 settlement. Evidence reflects the local cache at migration.";
    case "harness_settled":
      return "Completed automatically: settled in T3. This result stays unchanged when execution continues.";
    case "harness_archived":
      return "Completed automatically: archived in OpenCode. This result stays unchanged when execution continues.";
    case "user":
      return "Completed by you in Stage. This result stays unchanged when execution continues.";
  }
}

/** How an inferred completion happened, after "completed 3h ago" ("via T3 settle"); null for user or migrated ones. */
export function autoCompletedMeta(provenance: Acceptance["provenance"] | null): string | null {
  if (provenance === "harness_settled") return "via T3 settle";
  if (provenance === "harness_archived") return "via OpenCode archive";
  return null;
}
