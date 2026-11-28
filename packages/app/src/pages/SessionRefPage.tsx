import { useMutation, useQuery } from "@tanstack/react-query";
import { Link, Navigate, useNavigate, useParams } from "react-router";
import { ApiError } from "../api/client.ts";
import { useInvalidateWork } from "../api/queries.ts";
import { IconBack } from "../components/icons.tsx";
import { Button, ErrorText, PageHeader, SkeletonRows, buttonClass } from "../components/ui.tsx";
import { useApi } from "../platform/context.tsx";
import { taskSessionPath } from "../lib/taskSession.ts";

/**
 * A session link (`/connections/<c>/sessions/<id>`, or historical `/sessions/<id>`): opens the task that owns the
 * session, else says the session is not tracked here; a connection's session can be adopted as a task.
 */
export function SessionRefPage() {
  const api = useApi();
  const navigate = useNavigate();
  const invalidate = useInvalidateWork();
  const { connectionId, sessionId = "" } = useParams();
  const connection = connectionId === undefined ? null : Number(connectionId);
  const valid = sessionId !== "" && (connection === null || (Number.isSafeInteger(connection) && connection > 0));
  const session = `/sessions/${encodeURIComponent(sessionId)}`;
  const path = connection === null ? session : `/connections/${connection}${session}`;
  const resolved = useQuery({
    // Same key as the session chips.
    queryKey: ["session-reference", path],
    queryFn: () => api.resolveRef(path, undefined, true),
    enabled: valid,
    retry: false,
  });
  const adopt = useMutation({
    mutationFn: (scope: number) => api.adoptSession({ connectionId: scope, externalSessionId: sessionId }),
    onSuccess: async (task) => {
      await invalidate();
      await navigate(taskSessionPath(task.id), { replace: true });
    },
  });
  if (valid && resolved.isPending) return <SkeletonRows />;
  const taskId = resolved.data?.taskId ?? null;
  if (taskId !== null) return <Navigate replace to={taskSessionPath(taskId, resolved.data?.attempt)} />;
  // A 400 is a link that names no session here (no connection, or not this connection's).
  const failed = resolved.error !== null && !(resolved.error instanceof ApiError && resolved.error.status === 400);
  return (
    <>
      <PageHeader title="Session" meta={<span className="break-all">{sessionId}</span>} />
      {failed ? (
        <ErrorText error={resolved.error} />
      ) : (
        <p className="mb-3 text-ui text-muted">
          {connection === null ? "This session is not tracked in Stage, or its link names no connection." : "This session is not tracked in Stage."}
        </p>
      )}
      <div className="flex flex-wrap gap-2">
        {connection === null || !valid || failed ? null : (
          <Button variant="primary" busy={adopt.isPending} onClick={() => adopt.mutate(connection)}>
            Adopt as task
          </Button>
        )}
        <Link to="/" className={buttonClass("secondary")}>
          <IconBack /> Back to dashboard
        </Link>
      </div>
      <ErrorText error={adopt.error} />
    </>
  );
}
