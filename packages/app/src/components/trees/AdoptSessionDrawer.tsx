import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { useNavigate } from "react-router";
import { useConnections, useInvalidateWork, useProjects } from "../../api/queries.ts";
import type { AdoptSessionRequest, Task, SessionPreview, TreeDetail } from "../../api/types.ts";
import { useApi } from "../../platform/context.tsx";
import { HARNESS_NAME } from "../../lib/harnesses.ts";

import { SnippetTextarea } from "../SnippetPicker.tsx";
import { Button, Drawer, ErrorText, Field, Input, Panel, Select } from "../ui.tsx";

/** Explicit lookup only. Preview never adopts a session. */
export function AdoptSessionDrawer({
  tree,
  onClose,
  onAdopted,
}: {
  tree?: TreeDetail;
  onClose: () => void;
  onAdopted?: (task: Task) => void;
}) {
  const api = useApi();
  const projects = useProjects();
  const connections = useConnections();
  const [chosen, setConnection] = useState("");
  const available = connections.data?.connections.filter((c) => c.active && c.configured) ?? [];
  const connection = chosen || (available.length === 1 ? String(available[0]?.id) : "");
  const [projectId, setProjectId] = useState("");
  const [projectTitle, setProjectTitle] = useState("");
  const invalidate = useInvalidateWork();
  const navigate = useNavigate();
  const [ref, setRef] = useState("");
  const [picked, setPicked] = useState<SessionPreview | null>(null);
  const [title, setTitle] = useState("");
  const [note, setNote] = useState("");
  const preview = useMutation({
    mutationFn: async () => {
      const resolved = await api.resolveRef(ref.trim(), Number(connection));
      if (resolved.session?.connectionId !== Number(connection))
        throw new Error("Paste a session link or ID from the selected harness.");
      return api.sessionContext(resolved.session);
    },
    onSuccess: (thread) => {
      setPicked(thread);
      setTitle(thread.title);
    },
  });
  const adopt = useMutation({
    mutationFn: (thread: SessionPreview) => {
      const body: AdoptSessionRequest = tree === undefined ? {} : { treeId: tree.id };
      if (thread.task === null) {
        if (thread.project === null) {
          if (projectId !== "") body.projectId = Number(projectId);
          else body.newProjectTitle = projectTitle.trim();
        }
        if (title.trim() !== "") body.title = title.trim();
        if (note.trim() !== "") body.initialNote = note;
      }
      return api.adoptSession(thread.session, body);
    },
    onSuccess: async (task) => {
      await invalidate();
      onClose();
      if (onAdopted !== undefined) onAdopted(task);
      else await navigate(`/tasks/${task.id}`);
    },
  });
  const eligible = picked !== null && !picked.unavailableReason;
  return (
    <Drawer
      title={tree === undefined ? "Track session" : "Add session"}
      onClose={onClose}
      footer={
        <Button
          variant="primary"
          disabled={
            !eligible ||
            (picked?.project === null && projectId === "" && projectTitle.trim() === "")
          }
          busy={adopt.isPending}
          onClick={() => {
            if (picked !== null) adopt.mutate(picked);
          }}
        >
          {tree !== undefined
            ? "Add to tree"
            : picked?.task != null
              ? "Open task"
              : "Track as task"}
        </Button>
      }
    >
      <div className="flex flex-col gap-3.5">
        <p className="text-small text-muted">
          Choose a harness and paste the session you want to track.
        </p>
        <form
          className="flex flex-col gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            preview.mutate();
          }}
        >
          <Field label="Harness">
            {(id) => (
              <Select
                id={id}
                value={connection}
                disabled={preview.isPending || adopt.isPending}
                onChange={(e) => {
                  setConnection(e.target.value);
                  setPicked(null);
                  preview.reset();
                  adopt.reset();
                }}
              >
                <option value="">Choose harness</option>
                {connections.data?.connections.filter((c) => c.active).map((c) => (
                  <option key={c.id} value={c.id}>
                    {HARNESS_NAME[c.harness]}
                  </option>
                ))}
              </Select>
            )}
          </Field>
          <Field label="Session link or ID">
            {(id) => (
              <Input
                id={id}
                data-autofocus
                value={ref}
                disabled={preview.isPending || adopt.isPending}
                onChange={(event) => {
                  setRef(event.target.value);
                  setPicked(null);
                  preview.reset();
                  adopt.reset();
                }}
              />
            )}
          </Field>
          <Button
            type="submit"
            disabled={!connection || ref.trim() === ""}
            busy={preview.isPending}
          >
            Preview session
          </Button>
        </form>
        <ErrorText error={preview.error ?? adopt.error} />
        {picked === null ? null : (
          <>
            <Panel title="Session">
              <div className="flex flex-col gap-2">
                <p className="text-small font-medium break-words">{picked.title}</p>
                <p className="font-mono text-meta break-words">
                  {picked.project?.title ?? picked.workspace}
                </p>
                <p className="font-mono text-meta break-all text-muted">
                  {picked.session.externalSessionId}
                </p>
                {!eligible ? (
                  <p className="text-small text-bad">{picked.unavailableReason}</p>
                ) : null}
                {picked.task === null ? null : (
                  <p className="text-small text-muted">
                    Already tracked as task #{picked.task.id}.
                    {tree === undefined ? "" : " Confirm to move that task into this tree."}
                  </p>
                )}
              </div>
            </Panel>
            {picked.project !== null || !eligible ? null : (
              <Panel title="Stage project">
                <div className="flex flex-col gap-3">
                  <p className="text-small text-muted">
                    This workspace is not bound. Choose a Stage project or create one while
                    adopting.
                  </p>
                  <Field label="Project">
                    {(id) => (
                      <Select
                        id={id}
                        value={projectId}
                        onChange={(event) => setProjectId(event.target.value)}
                      >
                        <option value="">Create new project</option>
                        {(projects.data?.projects ?? []).map((project) => (
                          <option key={project.id} value={project.id}>
                            {project.title}
                          </option>
                        ))}
                      </Select>
                    )}
                  </Field>
                  {projectId !== "" ? null : (
                    <Field label="New project title">
                      {(id) => (
                        <Input
                          id={id}
                          value={projectTitle}
                          onChange={(event) => setProjectTitle(event.target.value)}
                        />
                      )}
                    </Field>
                  )}
                  <p className="text-small text-muted">
                    Confirmation binds the session's workspace. Future launches use this binding.
                  </p>
                </div>
              </Panel>
            )}
            {picked.task !== null || !eligible ? null : (
              <>
                <Field label="Title">
                  {(id) => (
                    <Input
                      id={id}
                      value={title}
                      onChange={(event) => setTitle(event.target.value)}
                    />
                  )}
                </Field>
                <Field label="Initial note">
                  {(id) => (
                    <SnippetTextarea
                      id={id}
                      rows={3}
                      value={note}
                      onValueChange={setNote}
                      projectId={picked.project?.id ?? null}
                    />
                  )}
                </Field>
              </>
            )}
          </>
        )}
      </div>
    </Drawer>
  );
}

export function TrackSessionButton() {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button onClick={() => setOpen(true)}>Track session</Button>
      {open ? <AdoptSessionDrawer onClose={() => setOpen(false)} /> : null}
    </>
  );
}
