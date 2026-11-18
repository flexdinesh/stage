import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { useApi } from "../platform/context.tsx";
import { useInvalidateWork } from "../api/queries.ts";
import type { Task, LaunchLinkPreview } from "../api/types.ts";
import { Alert, Button, ErrorText, Field, Input } from "./ui.tsx";

export function LaunchRecovery({ task }: { task: Task }) {
  const api = useApi();
  const invalidate = useInvalidateWork();
  const [link, setLink] = useState("");
  const [editing, setEditing] = useState(false);
  const [preview, setPreview] = useState<{ link: string; value: LaunchLinkPreview } | null>(null);
  const inspect = useMutation({
    mutationFn: (submittedLink: string) => api.previewLaunchLink(task.id, submittedLink),
    onSuccess: (value, submittedLink) => setPreview({ link: submittedLink, value }),
  });
  const attach = useMutation({
    mutationFn: async () => {
      if (preview === null) throw new Error("Preview the session first.");
      return api.attachLaunchLink(task.id, preview.link, preview.value);
    },
    onSettled: invalidate,
  });
  if (task.state !== "launching") return null;
  return (
    <Alert tone="need">
      <div className="flex min-w-0 flex-col gap-3">
        <div>
          <p className="font-medium">Launch unconfirmed</p>
          <p className="text-small">Stage is checking the existing attempt. New work stays blocked until its outcome is known.</p>
        </div>
        {task.launch?.reason ? <p className="break-words text-small">{task.launch.reason}</p> : null}
        {editing ? (
          <>
            <Field label="Session link">
              {(id) => (
                <Input
                  id={id}
                  value={link}
                  disabled={inspect.isPending || attach.isPending}
                  placeholder="Paste the existing session link"
                  onChange={(e) => { setLink(e.target.value); setPreview(null); inspect.reset(); attach.reset(); }}
                />
              )}
            </Field>
            <div className="flex flex-wrap gap-2">
              <Button disabled={link.trim() === "" || attach.isPending} busy={inspect.isPending} onClick={() => inspect.mutate(link)}>
                Preview session
              </Button>
              <Button disabled={attach.isPending} onClick={() => { setEditing(false); setPreview(null); }}>Cancel</Button>
            </div>
            {preview === null || preview.link !== link ? null : (
              <div className="flex flex-col gap-2" role="status">
                <p className="break-words font-medium">{preview.value.observation.title}</p>
                <p className="break-words text-small">{preview.value.observation.state.replaceAll("_", " ")} · {preview.value.observation.branch || "Project workspace"}</p>
                <p className="text-small">Attach this session to this task. This sends no launch and does not complete the task.</p>
                <div><Button variant="primary" busy={attach.isPending} onClick={() => attach.mutate()}>Attach existing session</Button></div>
              </div>
            )}
            <ErrorText error={inspect.error ?? attach.error} />
          </>
        ) : <div><Button onClick={() => setEditing(true)}>Link existing session</Button></div>}
      </div>
    </Alert>
  );
}
