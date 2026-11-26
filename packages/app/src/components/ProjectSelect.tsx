import { useCallback, useMemo, type ReactNode } from "react";
import type { Project } from "../api/types.ts";
import { matchProjects, type ProjectMatch } from "../lib/projects.ts";
import { WorkspaceDisplay, useWorkspaceConnections } from "./WorkspaceDisplay.tsx";
import { Combobox, type ComboboxOption } from "./ui.tsx";

function toOption(m: ProjectMatch): ComboboxOption {
  return {
    value: String(m.project.id),
    label: m.project.title,
    labelMatch: m.title,
    ...(m.project.archivedAt === null ? {} : { chip: "archived" }),
  };
}

/** Type-to-filter project picker; value is the project id as a string, "" for the pinned option (or none). */
export function ProjectCombobox({
  projects,
  id,
  value,
  onChange,
  pinned,
  placeholder,
  disabled,
  className,
  size,
  autoFocus,
  emptyText = "No matching projects",
  connectionId,
}: {
  projects: readonly Project[];
  id?: string | undefined;
  value: string;
  onChange: (value: string) => void;
  /** Label of a first option with value "" (e.g. "All projects", "Global"). */
  pinned?: string | undefined;
  placeholder?: string | undefined;
  disabled?: boolean | undefined;
  className?: string | undefined;
  size?: number | undefined;
  autoFocus?: boolean | undefined;
  /** Shown when no project matches. */
  emptyText?: ReactNode;
  connectionId?: number | undefined;
}) {
  const options = useCallback((query: string) => matchProjects(projects, query).map(toOption), [projects]);
  const connections = useWorkspaceConnections();
  const pinnedOptions = useMemo(() => (pinned === undefined ? [] : [{ value: "", label: pinned }]), [pinned]);
  return (
    <Combobox
      id={id}
      value={value}
      onChange={onChange}
      options={options}
      renderDetail={(option) => {
        const project = projects.find((p) => String(p.id) === option.value);
        return project === undefined ? null : <WorkspaceDisplay bindings={project.bindings} connectionId={connectionId} connections={connections} />;
      }}
      pinned={pinnedOptions}
      // Projects match on paths and repos too, so filtering helps even in a short list.
      searchable
      placeholder={placeholder}
      emptyText={emptyText}
      listLabel="Projects"
      disabled={disabled}
      className={className}
      size={size}
      autoFocus={autoFocus}
    />
  );
}
