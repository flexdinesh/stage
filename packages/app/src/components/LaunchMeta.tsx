// Task detail, in the "{harness} session" panel: the launch options the task chose, in its harness's labels
// (lib/capabilities.ts launchParts), then where that harness says the session works (task detail `reported`).
// Two mono meta lines; nothing when there is neither.
import type { TaskDetail } from "../api/types.ts";
import type { LaunchPart } from "../lib/capabilities.ts";
import { MetaLine } from "./ui.tsx";

// Each item wraps as a unit: it moves to the next line before it breaks, and breaks inside only when wider than the line.
const ITEM = "inline-block max-w-full break-words";

export function LaunchMeta({ parts, reported, harness }: { parts: LaunchPart[]; reported: TaskDetail["reported"]; harness: string }) {
  const branch = reported?.branch ?? "";
  const directory = reported?.directory ?? "";
  if (parts.length === 0 && branch === "" && directory === "") return null;
  return (
    <div className="flex flex-col gap-1">
      {parts.length === 0 ? null : (
        <MetaLine>
          {parts.map((p) => (
            <span key={p.label} className={ITEM}>
              {p.label} <span className="text-fg">{p.value}</span>
            </span>
          ))}
        </MetaLine>
      )}
      {branch === "" && directory === "" ? null : (
        <MetaLine>
          {`reported by ${harness}`}
          {branch === "" ? "" : (
            <span key="branch" className={ITEM}>
              branch <span className="break-all text-fg">{branch}</span>
            </span>
          )}
          {directory === "" ? "" : (
            <span key="directory" className={`${ITEM} break-all text-fg`}>
              {directory}
            </span>
          )}
        </MetaLine>
      )}
    </div>
  );
}
