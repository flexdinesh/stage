import { useMutation } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router";
import { api } from "../api/client.ts";
import { useChains, useInvalidateWork } from "../api/queries.ts";
import { ChainList } from "../components/ChainList.tsx";
import { Button, Card, ErrorText, Field, Input, PageHeader, QueryState } from "../components/ui.tsx";

function CreateChain() {
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
    <Card title="New chain">
      <form onSubmit={submit} className="flex flex-col gap-3">
        <div className="flex flex-wrap items-end gap-3">
          <Field label="Title" className="min-w-60 flex-1">
            {(id) => <Input id={id} value={title} onChange={(e) => setTitle(e.target.value)} />}
          </Field>
          <Button type="submit" variant="primary" busy={create.isPending} disabled={title.trim() === ""}>
            Create
          </Button>
        </div>
        <p className="text-xs text-neutral-500">
          Chains run tasks in order. Each next step starts only after the previous thread is settled in T3.
        </p>
        <ErrorText error={create.error} />
      </form>
    </Card>
  );
}

export function ChainsPage() {
  const chains = useChains();
  return (
    <>
      <PageHeader title="Chains" />
      <div className="flex flex-col gap-5">
        <CreateChain />
        <QueryState isPending={chains.isPending} error={chains.error}>
          <ChainList chains={chains.data?.chains ?? []} emptyText="No chains yet." />
        </QueryState>
      </div>
    </>
  );
}
