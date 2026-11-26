import { useMutation } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router";
import { useConnections, useHarnesses, useInvalidateWork, useMachines, useProjects } from "../../api/queries.ts";
import { connectionOptions } from "../../lib/machines.ts";
import type { AdoptSessionRequest, SessionPreview, SessionRef, Task, TreeDetail } from "../../api/types.ts";
import { useApi } from "../../platform/context.tsx";

import { ProjectCombobox } from "../ProjectSelect.tsx";
import { SnippetTextarea } from "../SnippetPicker.tsx";
import { Button, Drawer, ErrorText, Field, InlineError, Input, Meta, Muted, Panel, Combobox } from "../ui.tsx";
import { BlockedReason, LaunchBlockAlert, useOperationGate } from "../LaunchGate.tsx";

export function useTrackSessionBlock(): string | null {
  const connections = useConnections();
  const gate = useOperationGate();
  if (connections.data === undefined) return connections.error?.message ?? "Checking harness connections.";
  const blocks = connections.data.connections.filter((c) => c.active).map((c) => gate(c.id, "adopt"));
  return blocks.includes(null) ? null : blocks[0]?.reason ?? "Connect a harness in Settings to track sessions.";
}

/**
 * Explicit lookup only. Preview never adopts a session. `initial` (a waiting session from the inbox) fills the
 * harness and session and previews it at once; tracking still waits for the user's confirmation.
 */
export function AdoptSessionDrawer({
  tree,
  initial,
  onClose,
  onAdopted,
}: {
  tree?: TreeDetail;
  initial?: SessionRef;
  onClose: () => void;
  onAdopted?: (task: Task) => void;
}) {
  const api = useApi();
  const projects = useProjects();
  const connections = useConnections();
  const harnessNames = useHarnesses();
  const gate = useOperationGate();
  const { machines } = useMachines();
  const [chosen, setConnection] = useState(initial === undefined ? "" : String(initial.connectionId));
  const available = connections.data?.connections.filter((c) => c.active) ?? [];
  const connection = chosen || (available.length === 1 ? String(available[0]?.id) : "");
  const block = connection === "" ? null : gate(Number(connection), "adopt");
  const sessionHint = harnessNames.descriptor(harnessNames.harness(Number(connection)) ?? "")?.sessionHint;
  const [projectId, setProjectId] = useState("");
  const [projectTitle, setProjectTitle] = useState("");
  const invalidate = useInvalidateWork();
  const navigate = useNavigate();
  const [ref, setRef] = useState(initial?.externalSessionId ?? "");
  const [picked, setPicked] = useState<SessionPreview | null>(null);
  const [title, setTitle] = useState("");
  const [note, setNote] = useState("");
  const preview = useMutation({
    mutationFn: async () => {
      if (block !== null) throw new Error(block.reason);
      if (connection === "" || ref.trim() === "") throw new Error("Choose a harness and enter a session link or ID.");
      const resolved = await api.resolveRef(ref.trim(), Number(connection));
      if (resolved.session?.connectionId !== Number(connection))
        throw new Error("Paste a session link or ID from the selected harness.");
      return api.sessionContext(resolved.session);
    },
    onSuccess: (session) => {
      setPicked(session);
      setTitle(session.title);
    },
  });
  const adopt = useMutation({
    mutationFn: (session: SessionPreview) => {
      const adoptionBlock = gate(session.session.connectionId, "adopt");
      if (adoptionBlock !== null) throw new Error(adoptionBlock.reason);
      if (session.unavailableReason) throw new Error(session.unavailableReason);
      const body: AdoptSessionRequest = tree === undefined ? {} : { treeId: tree.id };
      if (session.task === null) {
        if (session.project === null) {
          if (projectId !== "") body.projectId = Number(projectId);
          else body.newProjectTitle = projectTitle.trim();
        }
        if (title.trim() !== "") body.title = title.trim();
        if (note.trim() !== "") body.initialNote = note;
      }
      return api.adoptSession(session.session, body);
    },
    onSuccess: async (task) => {
      await invalidate();
      onClose();
      if (onAdopted !== undefined) onAdopted(task);
      else await navigate(`/tasks/${task.id}`);
    },
  });
  // Preview the handed-over session once, on open.
  const previewMutate = preview.mutate;
  const prefilled = useRef(initial !== undefined);
  const canPreview = block === null && connection !== "" && ref.trim() !== "";
  useEffect(() => {
    if (!prefilled.current || !canPreview) return;
    prefilled.current = false;
    previewMutate();
  }, [canPreview, previewMutate]);
  const eligible = picked !== null && !picked.unavailableReason;
  return (
    <Drawer
      title={tree === undefined ? "Track session" : "Add session"}
      onClose={onClose}
      footer={
        <Button
          variant="primary"
          disabled={
            block !== null || !eligible ||
            (picked?.project === null && projectId === "" && projectTitle.trim() === "")
          }
          busy={adopt.isPending}
          onClick={() => {
            if (picked !== null && block === null) adopt.mutate(picked);
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
        <Muted>Choose a harness and paste the session you want to track.</Muted>
        <form
          className="flex flex-col gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            if (canPreview && !preview.isPending && !adopt.isPending) preview.mutate();
          }}
        >
          <Field label="Harness">
            {(id) => (
              <Combobox
                id={id}
                value={connection}
                disabled={preview.isPending || adopt.isPending}
                onChange={(next) => {
                  prefilled.current = false;
                  setConnection(next);
                  setPicked(null);
                  preview.reset();
                  adopt.reset();
                }}
                listLabel="Harnesses"
                showTag
                options={[
                  { value: "", label: "Choose harness" },
                  ...connectionOptions(available, machines, harnessNames.kindName).map((option) => ({
                    ...option,
                    detail: gate(Number(option.value), "adopt")?.reason,
                  })),
                ]}
              />
            )}
          </Field>
          <Field label="Session link or ID" {...(sessionHint === undefined || sessionHint === "" ? {} : { hint: sessionHint })}>
            {(id) => (
              <Input
                id={id}
                data-autofocus
                value={ref}
                disabled={preview.isPending || adopt.isPending}
                onChange={(event) => {
                  prefilled.current = false;
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
            disabled={!canPreview || adopt.isPending}
            busy={preview.isPending}
          >
            Preview session
          </Button>
        </form>
        {block === null ? null : <LaunchBlockAlert block={block} tone="plain" />}
        <ErrorText error={preview.error ?? adopt.error} />
        {picked === null ? null : (
          <>
            <Panel title="Session">
              <div className="flex flex-col gap-2">
                <p className="text-small font-medium break-words">{picked.title}</p>
                <p className="font-mono text-meta break-words">
                  {picked.project?.title ?? picked.workspace}
                </p>
                <Meta className="break-all">{picked.session.externalSessionId}</Meta>
                {!eligible ? <InlineError>{picked.unavailableReason}</InlineError> : null}
                {picked.task === null ? null : (
                  <Muted>
                    Already tracked as task #{picked.task.id}.
                    {tree === undefined ? "" : " Confirm to move that task into this tree."}
                  </Muted>
                )}
              </div>
            </Panel>
            {picked.project !== null || !eligible ? null : (
              <Panel title="Stage project">
                <div className="flex flex-col gap-3">
                  <Muted>
                    This workspace is not bound. Choose a Stage project or create one while
                    adopting.
                  </Muted>
                  <Field label="Project">
                    {(id) => (
                      <ProjectCombobox
                        id={id}
                        projects={projects.data?.projects ?? []}
                        value={projectId}
                        onChange={setProjectId}
                        pinned="Create new project"
                        className="w-full"
                      />
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
                  <Muted>Confirmation binds the session's workspace. Future launches use this binding.</Muted>
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
  const reason = useTrackSessionBlock();
  return (
    <>
      <BlockedReason reason={reason ?? undefined} showOnPhone>
        <Button disabled={reason !== null} onClick={() => setOpen(true)}>Track session</Button>
      </BlockedReason>
      {open ? <AdoptSessionDrawer onClose={() => setOpen(false)} /> : null}
    </>
  );
}
