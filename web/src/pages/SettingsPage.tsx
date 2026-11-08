import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { api } from "../api/client.ts";
import { invalidateWork, keys, useT3Status } from "../api/queries.ts";
import type { T3Status } from "../api/types.ts";
import { Alert, Button, Chip, Detail, Details, ErrorText, Field, Input, Muted, PageHeader, Panel, QueryState, Segmented } from "../components/ui.tsx";
import { THEMES, useTheme } from "../lib/theme.ts";
import { dateTime, relativeTime } from "../lib/format.ts";

const when = (iso: string | null) => (iso === null ? "-" : `${dateTime(iso)} (${relativeTime(iso)})`);

function StatusCard({ status }: { status: T3Status }) {
  return (
    <Panel
      title="T3 connection"
      actions={
        status.connected ? (
          <Chip tone="ok" dot>
            connected
          </Chip>
        ) : (
          <Chip tone="bad" dot>
            not connected
          </Chip>
        )
      }
    >
      <Details>
        <Detail label="T3 URL">{status.t3Url}</Detail>
        <Detail label="Public URL">{status.publicUrl}</Detail>
        <Detail label="Environment">{status.environmentId ?? "-"}</Detail>
        <Detail label="Client id">{status.clientId ?? "-"}</Detail>
        <Detail label="Token expires">{when(status.tokenExpiresAt)}</Detail>
        <Detail label="Admin token">
          {status.adminTokenConfigured ? "configured (auto-renew)" : "not configured (pair manually)"}
        </Detail>
        <Detail label="Last full sync">{when(status.lastFullSyncAt)}</Detail>
        <Detail label="Last error">
          {status.lastError === null ? "-" : <span className="text-bad">{status.lastError}</span>}
        </Detail>
      </Details>
    </Panel>
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
    <Panel title="Pair manually" meta="fallback">
      <form onSubmit={submit} className="flex flex-col gap-3">
        <Muted>
          Use when auto-renew is unavailable: run <code className="font-mono text-fg">t3 pair</code> on the T3 host and paste the code.
        </Muted>
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
        {pair.isSuccess ? <Alert tone="plain">Paired.</Alert> : null}
        <ErrorText error={pair.error} />
      </form>
    </Panel>
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
    <Panel title="Sync" meta="every 5 min">
      <div className="flex flex-col gap-3">
        <Muted>Full sync of all T3 projects and threads runs every 5 minutes. Run it now to refresh history immediately.</Muted>
        <div>
          <Button variant="primary" busy={sync.isPending} onClick={() => sync.mutate()}>
            Sync now
          </Button>
        </div>
        {sync.data === undefined ? null : (
          <Alert tone="plain">
            Synced <span className="tabular font-mono">{sync.data.projects}</span> projects,{" "}
            <span className="tabular font-mono">{sync.data.threads}</span> threads at{" "}
            <span className="font-mono">{dateTime(sync.data.finishedAt)}</span>.
          </Alert>
        )}
        <ErrorText error={sync.error} />
      </div>
    </Panel>
  );
}

const THEME_OPTIONS = THEMES.map((t) => ({ value: t, label: t }));

function ThemeCard() {
  const { theme, setTheme } = useTheme();
  return (
    <Panel title="Theme">
      <div className="flex flex-wrap items-center gap-3">
        <Segmented label="Theme" options={THEME_OPTIONS} value={theme} onChange={setTheme} />
        <Muted>{theme === "system" ? "Follows the OS light/dark setting." : `Always ${theme}.`}</Muted>
      </div>
    </Panel>
  );
}

export function SettingsPage() {
  const status = useT3Status();
  return (
    <>
      <PageHeader title="Settings" meta="T3 connection, sync, appearance" />
      <div className="grid grid-cols-1 items-start gap-3.5 lg:grid-cols-2">
        <div className="lg:row-span-3">
          <QueryState isPending={status.isPending} error={status.error}>
            {status.data === undefined ? null : <StatusCard status={status.data} />}
          </QueryState>
        </div>
        <SyncCard />
        <PairCard />
        <ThemeCard />
      </div>
    </>
  );
}
