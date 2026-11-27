import { useCallback, useMemo, type ReactNode } from "react";
import { useProjectGroups } from "../api/queries.ts";
import type { Project } from "../api/types.ts";
import { matchProjects, type ProjectMatch } from "../lib/projects.ts";
import { GroupSummary } from "./GroupWorkspaceDisplay.tsx";
import { useWorkspaceConnections } from "./WorkspaceDisplay.tsx";
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
}) {
  const { data } = useProjectGroups();
  const groups = useMemo(() => new Map(data?.groups.map((group) => [group.id, group])), [data]);
  const options = useCallback((query: string) => matchProjects(projects, groups, query).map(toOption), [projects, groups]);
  const connections = useWorkspaceConnections();
  const pinnedOptions = useMemo(() => (pinned === undefined ? [] : [{ value: "", label: pinned }]), [pinned]);
  return (
    <Combobox
      id={id}
      value={value}
      onChange={onChange}
      options={options}
      renderDetail={(option) => {
        const group = groups.get(Number(option.value));
        return group === undefined ? null : <GroupSummary group={group} connections={connections} />;
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
