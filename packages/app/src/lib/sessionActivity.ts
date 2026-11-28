import type { Activity, ActivityItem } from "../api/types.ts";

const itemTitle: Record<ActivityItem["kind"], string> = {
  message_user: "User message",
  message_agent: "Assistant message",
  reasoning: "Reasoning",
  tool: "Tool",
  command: "Command",
  file_change: "File change",
  plan: "Plan",
  event: "Event",
};

/** Keep the adapter's order and prefer its purpose-built, bounded preview. */
export function activityView(activity: Activity) {
  return {
    empty: activity.items.length === 0 && activity.plan.length === 0 && activity.files.length === 0,
    items: activity.items.map((item) => ({
      ...item,
      title: item.title.trim() || itemTitle[item.kind] || "Event",
      preview: item.preview.trim() || item.text.trim(),
    })),
    files: activity.files.map((file) => ({
      ...file,
      added: `+${file.additions}`,
      removed: `-${file.deletions}`,
    })),
  };
}

export const liveActivity = (current: boolean, state: string): boolean =>
  current && (state === "running" || state === "needs_input");
