import { Link } from "react-router";
import type { TaskSessionSummary } from "../../api/types.ts";
import { taskSessionPath } from "../../lib/taskSession.ts";
import { IconArrowRight, IconChevronDown } from "../icons.tsx";
import { Button, Menu, Meta, cx, useMenu } from "../ui.tsx";

export function AttemptSwitcher({
  taskId, sessions, selected,
}: {
  taskId: number;
  sessions: readonly TaskSessionSummary[];
  selected: number | undefined;
}) {
  const menu = useMenu();
  if (sessions.length === 0) return null;
  const current = sessions.find((session) => session.current);
  return (
    <>
      <Button {...menu.triggerProps} aria-haspopup="menu" aria-label={selected === undefined ? "Session attempts" : `Session attempts, attempt ${selected}`}>
        {selected === undefined ? "Session attempts" : `Attempt ${selected}`} <IconChevronDown size={14} />
      </Button>
      <Menu menu={menu} label="Session attempts" align="right" className="max-h-[50dvh] w-56 overflow-y-auto">
        {(current === undefined ? [undefined, ...sessions] : sessions).map((session) => {
          const active = session?.attempt === selected;
          const to = taskSessionPath(taskId, session?.current === false ? session.attempt : undefined);
          return (
            <Link
              key={session?.attempt ?? "current"}
              role="menuitem"
              to={to}
              onClick={menu.hide}
              aria-current={active ? "page" : undefined}
              className={cx("flex min-h-11 items-center gap-2 px-3 text-ui outline-none hover:bg-panel focus-visible:bg-panel md:min-h-control", active && "bg-panel")}
            >
              <span className="flex-1">{session === undefined ? "Current session" : `Attempt ${session.attempt}`}</span>
              {session?.current === true ? <Meta>current</Meta> : null}
              <IconArrowRight size={12} />
            </Link>
          );
        })}
      </Menu>
    </>
  );
}
