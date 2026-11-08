import { useProjects } from "../api/queries.ts";
import { Select } from "./ui.tsx";

/** Project filter select; value "" = all projects. */
export function ProjectSelect({
  id,
  value,
  onChange,
  allLabel = "All projects",
}: {
  id?: string;
  value: string;
  onChange: (value: string) => void;
  allLabel?: string;
}) {
  const projects = useProjects();
  return (
    <Select id={id} value={value} onChange={(e) => onChange(e.target.value)} className="min-w-40">
      <option value="">{allLabel}</option>
      {(projects.data?.projects ?? []).map((p) => (
        <option key={p.id} value={String(p.id)}>
          {p.title}
        </option>
      ))}
    </Select>
  );
}
