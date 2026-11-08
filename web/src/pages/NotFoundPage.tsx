import { Link } from "react-router";
import { PageHeader } from "../components/ui.tsx";

export function NotFoundPage() {
  return (
    <>
      <PageHeader title="Not found" />
      <Link to="/" className="text-sm text-blue-600 hover:underline dark:text-blue-400">
        Back to dashboard
      </Link>
    </>
  );
}
