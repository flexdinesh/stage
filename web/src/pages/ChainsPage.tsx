import { useMutation } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router";
import { api } from "../api/client.ts";
import { useChains, useInvalidateWork } from "../api/queries.ts";
import { ChainList } from "../components/ChainList.tsx";
import { IconPlus } from "../components/icons.tsx";
import { Button, Drawer, ErrorText, Field, Input, PageHeader, QueryState } from "../components/ui.tsx";

const FORM_ID = "new-chain-form";

function CreateChainDrawer({ onClose }: { onClose: () => void }) {
  const navigate = useNavigate();
  const invalidate = useInvalidateWork();
  const [title, setTitle] = useState("");
  const create = useMutation({
    mutationFn: () => api.createChain({ title: title.trim() }),
    onSuccess: async (chain) => {
      await invalidate();
      await navigate(`/chains/${chain.id}`);
    },
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (title.trim() !== "") create.mutate();
  };
  return (
    <Drawer
      title="New chain"
      onClose={onClose}
      footer={
        <>
          <Button type="submit" form={FORM_ID} variant="primary" busy={create.isPending} disabled={title.trim() === ""}>
            Create chain
          </Button>
          <Button onClick={onClose}>Cancel</Button>
        </>
      }
    >
      <form id={FORM_ID} onSubmit={submit} className="flex flex-col gap-3">
        <Field
          label="Title"
          hint="Chains run tasks in order. Each next step starts only after the previous thread is settled in T3."
        >
          {(id) => <Input id={id} data-autofocus value={title} onChange={(e) => setTitle(e.target.value)} />}
        </Field>
        <ErrorText error={create.error} />
      </form>
    </Drawer>
  );
}

export function ChainsPage() {
  const chains = useChains();
  const [creating, setCreating] = useState(false);
  const list = chains.data?.chains ?? [];
  const running = list.filter((c) => c.state === "running").length;
  return (
    <>
      <PageHeader
        title="Chains"
        meta={chains.data === undefined ? "loading" : `${list.length} chains · ${running} running`}
        actions={
          <Button variant="primary" onClick={() => setCreating(true)}>
            <IconPlus /> New chain
          </Button>
        }
      />
      <QueryState isPending={chains.isPending} error={chains.error}>
        <ChainList chains={list} emptyText="No chains yet. Create one, then add backlog tasks as steps." />
      </QueryState>
      {creating ? <CreateChainDrawer onClose={() => setCreating(false)} /> : null}
    </>
  );
}
