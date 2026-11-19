import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useHarnesses } from "../api/queries.ts";
import { HARNESS_NAME } from "../lib/harnesses.ts";
import { useApi } from "../platform/context.tsx";
import { Button, Drawer, ErrorText, Field, Input, Panel, Select } from "./ui.tsx";

export function SharedPaths() {
  const api = useApi();
  const qc = useQueryClient();
  const harnesses = useHarnesses();
  const paths = useQuery({ queryKey: ["shared-paths"], queryFn: api.sharedPaths });
  const [editing, setEditing] = useState(false);
  const [chosen, setConnection] = useState(0);
  // Mappings belong to active connections; default to the first one.
  const connection = harnesses.activeList.some((c) => c.id === chosen) ? chosen : (harnesses.activeList[0]?.id ?? 0);
  const [source, setSource] = useState("");
  const [local, setLocal] = useState("");
  const save = useMutation({
    mutationFn: ({ id, from, to }: { id: number; from: string; to: string }) =>
      api.setSharedPath(id, from, to),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["shared-paths"] });
      setEditing(false);
      setSource("");
      setLocal("");
    },
  });
  return (
    <Panel
      title="Shared directories"
      actions={
        <Button small onClick={() => setEditing(true)}>
          Add mapping
        </Button>
      }
    >
      <p className="text-small text-muted">
        Map a harness directory to the same files on the Stage server to match Git worktrees during
        binding.
      </p>
      {paths.data?.paths.map((p) => (
        <div
          key={p.connectionId + p.harnessRoot}
          className="mt-2 border-t border-line pt-2 text-small"
        >
          <span className="font-medium">{harnesses.name(p.connectionId)}</span>{" "}
          <code className="break-all">{p.harnessRoot}</code> to{" "}
          <code className="break-all">{p.stageRoot}</code>
          <Button
            small
            variant="ghost"
            busy={save.isPending}
            onClick={() => save.mutate({ id: p.connectionId, from: p.harnessRoot, to: "" })}
          >
            Remove mapping
          </Button>
        </div>
      ))}
      <ErrorText error={paths.error ?? save.error} />
      {editing ? (
        <Drawer
          title="Shared directory"
          onClose={() => setEditing(false)}
          footer={
            <Button
              variant="primary"
              busy={save.isPending}
              disabled={connection === 0 || !source.trim() || !local.trim()}
              onClick={() => save.mutate({ id: connection, from: source, to: local })}
            >
              Save mapping
            </Button>
          }
        >
          <div className="flex flex-col gap-3">
            <Field label="Harness">
              {(id) => (
                <Select
                  id={id}
                  value={connection}
                  onChange={(e) => setConnection(Number(e.target.value))}
                >
                  {harnesses.activeList.map((c) => (
                    <option key={c.id} value={c.id}>
                      {HARNESS_NAME[c.harness]}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
            <Field label="Directory on the harness">
              {(id) => <Input id={id} value={source} onChange={(e) => setSource(e.target.value)} />}
            </Field>
            <Field label="Same directory on the Stage server">
              {(id) => <Input id={id} value={local} onChange={(e) => setLocal(e.target.value)} />}
            </Field>
            <ErrorText error={save.error} />
          </div>
        </Drawer>
      ) : null}
    </Panel>
  );
}
