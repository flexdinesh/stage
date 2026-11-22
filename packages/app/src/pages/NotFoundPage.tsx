import { Link, useLocation } from "react-router";
import { IconBack } from "../components/icons.tsx";
import { PageHeader, buttonClass } from "../components/ui.tsx";

export function NotFoundPage() {
  const { pathname } = useLocation();
  return (
    <>
      <PageHeader title="Not found" meta={<span className="break-all">{pathname}</span>} />
      <p className="mb-3 text-ui text-muted">Nothing lives at this address. It may have been deleted or the link is wrong.</p>
      <Link to="/" className={buttonClass("secondary")}>
        <IconBack /> Back to dashboard
      </Link>
    </>
  );
}

/** A record page whose record the server reports gone (404): what happened and the way back to its list. */
export function RecordNotFound({ title, meta, text, back }: { title: string; meta: string; text: string; back: { to: string; label: string } }) {
  return (
    <>
      <PageHeader title={title} meta={meta} />
      <p className="mb-3 text-ui text-muted">{text}</p>
      <Link to={back.to} className={buttonClass("secondary")}>
        <IconBack /> {back.label}
      </Link>
    </>
  );
}
