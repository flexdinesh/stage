import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { api } from "../api/client.ts";
import { invalidateWork, keys, useT3Status } from "../api/queries.ts";
import type { T3Status } from "../api/types.ts";
import { Badge, Button, Card, Detail, Details, ErrorText, Field, Input, PageHeader, QueryState } from "../components/ui.tsx";
import { dateTime, relativeTime } from "../lib/format.ts";

const when = (iso: string | null) => (iso === null ? "-" : `${dateTime(iso)} (${relativeTime(iso)})`);

function StatusCard({ status }: { status: T3Status }) {
  return (
    <Card title="T3 connection">
      <Details>
        <Detail label="Status">
          {status.connected ? <Badge tone="green">connected</Badge> : <Badge tone="red">not connected</Badge>}
        </Detail>
        <Detail label="T3 URL">
          <span className="font-mono text-xs">{status.t3Url}</span>
        </Detail>
        <Detail label="Public URL">
          <span className="font-mono text-xs">{status.publicUrl}</span>
        </Detail>
        <Detail label="Environment">{status.environmentId ?? "-"}</Detail>
        <Detail label="Client id">
          <span className="font-mono text-xs">{status.clientId ?? "-"}</span>
        </Detail>
        <Detail label="Token expires">{when(status.tokenExpiresAt)}</Detail>
        <Detail label="Admin token">
          {status.adminTokenConfigured ? "configured (auto-renew)" : "not configured (pair manually)"}
        </Detail>
        <Detail label="Last full sync">{when(status.lastFullSyncAt)}</Detail>
        <Detail label="Last error">
          {status.lastError === null ? "-" : <span className="text-red-600 dark:text-red-400">{status.lastError}</span>}
        </Detail>
      </Details>
    </Card>
  );
}

function PairCard() {
  const qc = useQueryClient();
  const [code, setCode] = useState("");
  const pair = useMutation({
    mutationFn: () => api.pair({ code: code.trim() }),
    onSuccess: (status) => {
      setCode("");
      qc.setQueryData(keys.t3Status, status);
    },
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (code.trim() !== "") pair.mutate();
  };
  return (
    <Card title="Pair manually">
      <form onSubmit={submit} className="flex flex-col gap-3">
        <p className="text-sm text-neutral-500">
          Fallback when auto-renew is unavailable: run <code className="font-mono">t3 pair</code> on the T3 host and paste the code.
        </p>
        <div className="flex flex-wrap items-end gap-2">
          <Field label="Pairing code" className="min-w-52 flex-1">
            {(id) => (
              <Input id={id} value={code} onChange={(e) => setCode(e.target.value)} placeholder="ABCD-1234" className="font-mono" />
            )}
          </Field>
          <Button type="submit" variant="primary" busy={pair.isPending} disabled={code.trim() === ""}>
            Pair
          </Button>
        </div>
        {pair.isSuccess ? <p className="text-sm text-green-700 dark:text-green-400">Paired.</p> : null}
        <ErrorText error={pair.error} />
      </form>
    </Card>
  );
}

function SyncCard() {
  const qc = useQueryClient();
  const sync = useMutation({
    mutationFn: api.sync,
    onSuccess: async () => {
      await Promise.all([
        invalidateWork(qc),
        qc.invalidateQueries({ queryKey: keys.projects }),
        qc.invalidateQueries({ queryKey: keys.t3Status }),
      ]);
    },
  });
  return (
    <Card title="Sync">
      <div className="flex flex-col gap-3">
        <p className="text-sm text-neutral-500">
          Full sync of all T3 projects and threads runs every 5 minutes. Run it now to refresh history immediately.
        </p>
        <div>
          <Button variant="primary" busy={sync.isPending} onClick={() => sync.mutate()}>
            Sync now
          </Button>
        </div>
        {sync.data === undefined ? null : (
          <p className="text-sm text-green-700 dark:text-green-400">
            Synced {sync.data.projects} projects, {sync.data.threads} threads at {dateTime(sync.data.finishedAt)}.
          </p>
        )}
        <ErrorText error={sync.error} />
      </div>
    </Card>
  );
}

export function SettingsPage() {
  const status = useT3Status();
  return (
    <>
      <PageHeader title="Settings" />
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <div className="lg:row-span-2">
          <QueryState isPending={status.isPending} error={status.error}>
            {status.data === undefined ? null : <StatusCard status={status.data} />}
          </QueryState>
        </div>
        <PairCard />
        <SyncCard />
      </div>
    </>
  );
}
