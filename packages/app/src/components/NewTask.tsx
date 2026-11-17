import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";
import { useNavigate } from "react-router";
import { usePreferences } from "../api/queries.ts";
import { TaskFormModal, type StartMode, type TreePrefill } from "./TaskForm.tsx";

/** Opens the New task drawer; `mode: "repeat"` opens it as a new schedule; `tree` joins a tree (tree view Add task). */
type Open = (defaultProjectId?: number, mode?: StartMode, tree?: TreePrefill) => void;

const NewTaskContext = createContext<Open>(() => {});

/** Global "New task" drawer: top bar button, command palette, `N` shortcut, New schedule actions. */
export function NewTaskProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<{
    projectId: number | undefined;
    mode: StartMode | undefined;
    tree: TreePrefill | undefined;
  } | null>(null);
  const navigate = useNavigate();
  // Warm the model-defaults cache so the drawer opens prefilled without a loading step.
  usePreferences();
  const open = useCallback<Open>((projectId, mode, tree) => setState({ projectId, mode, tree }), []);
  const value = useMemo(() => open, [open]);
  return (
    <NewTaskContext.Provider value={value}>
      {children}
      {state === null ? null : (
        <TaskFormModal
          {...(state.projectId === undefined ? {} : { defaultProjectId: state.projectId })}
          {...(state.mode === undefined ? {} : { defaultMode: state.mode })}
          {...(state.tree === undefined ? {} : { defaultTree: state.tree })}
          onClose={() => setState(null)}
          onSaved={(saved) => {
            setState(null);
            void navigate(saved.kind === "task" ? `/tasks/${saved.task.id}` : `/schedules/${saved.schedule.id}`);
          }}
        />
      )}
    </NewTaskContext.Provider>
  );
}

export const useNewTask = (): Open => useContext(NewTaskContext);
