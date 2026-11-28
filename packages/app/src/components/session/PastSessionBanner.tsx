import { taskSessionPath } from "../../lib/taskSession.ts";
import { IconArrowRight } from "../icons.tsx";
import { Alert, TextLink } from "../ui.tsx";

export function PastSessionBanner({ taskId, attempt }: { taskId: number; attempt: number }) {
  return (
    <Alert tone="plain" actions={
      <TextLink to={taskSessionPath(taskId)} className="inline-flex items-center gap-1">
        Current session <IconArrowRight size={12} />
      </TextLink>
    }>
      Attempt {attempt} · past session
    </Alert>
  );
}
