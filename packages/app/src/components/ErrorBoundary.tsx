// Render-error recovery. A page error keeps the shell (sidebar, navigation) and offers a retry; an error above the
// router (the host gate, the connection screens) falls back to a reload.
import { Component, type ErrorInfo, type ReactNode } from "react";
import { Link, useLocation, useNavigate, useRouteError } from "react-router";
import { IconBack } from "./icons.tsx";
import { Alert, Button, PageHeader, buttonClass, errorText } from "./ui.tsx";

/** A route's `errorElement`: the page failed to render; the layout around it stays. */
export function RouteError() {
  const error = useRouteError();
  const location = useLocation();
  const navigate = useNavigate();
  return (
    <>
      <PageHeader title="Something went wrong" />
      <Alert tone="bad">{errorText(error) ?? "This page failed to render."}</Alert>
      <div className="mt-3 flex flex-wrap gap-2">
        {/* Navigating to the same address clears the route error and renders the page again. */}
        <Button variant="primary" onClick={() => void navigate(location, { replace: true })}>
          Try again
        </Button>
        <Link to="/" className={buttonClass("secondary")}>
          <IconBack /> Back to dashboard
        </Link>
      </div>
    </>
  );
}

/** The last resort around the whole app: an error outside any route. */
export class AppErrorBoundary extends Component<{ children: ReactNode }, { error: unknown }> {
  override state: { error: unknown } = { error: null };

  static getDerivedStateFromError(error: unknown): { error: unknown } {
    return { error };
  }

  override componentDidCatch(error: unknown, info: ErrorInfo): void {
    console.error("Stage failed to render", error, info.componentStack);
  }

  override render(): ReactNode {
    if (this.state.error === null) return this.props.children;
    return (
      <main className="mx-auto flex max-w-xl flex-col gap-3 p-6">
        <PageHeader title="Something went wrong" />
        <Alert tone="bad">{errorText(this.state.error) ?? "Stage failed to render."}</Alert>
        <div>
          <Button variant="primary" onClick={() => window.location.reload()}>
            Reload
          </Button>
        </div>
      </main>
    );
  }
}
