import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";
import { useNavigate } from "react-router";
import { usePreferences } from "../api/queries.ts";
import { TaskFormModal } from "./TaskForm.tsx";

type Open = (defaultProjectId?: number) => void;

const NewTaskContext = createContext<Open>(() => {});

/** Global "New task" drawer: top bar button, command palette, `N` shortcut. */
export function NewTaskProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<{ projectId: number | undefined } | null>(null);
  const navigate = useNavigate();
  // Warm the model-defaults cache so the drawer opens prefilled without a loading step.
  usePreferences();
  const open = useCallback<Open>((projectId) => setState({ projectId }), []);
  const value = useMemo(() => open, [open]);
  return (
    <NewTaskContext.Provider value={value}>
      {children}
      {state === null ? null : (
        <TaskFormModal
          {...(state.projectId === undefined ? {} : { defaultProjectId: state.projectId })}
          onClose={() => setState(null)}
          onSaved={(task) => {
            setState(null);
            void navigate(`/tasks/${task.id}`);
          }}
        />
      )}
    </NewTaskContext.Provider>
  );
}

export const useNewTask = (): Open => useContext(NewTaskContext);
