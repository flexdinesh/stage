import { NavLink, Outlet } from "react-router";

const NAV = [
  { to: "/", label: "Dashboard" },
  { to: "/projects", label: "Projects" },
  { to: "/chains", label: "Chains" },
  { to: "/history", label: "History" },
  { to: "/templates", label: "Templates" },
  { to: "/settings", label: "Settings" },
] as const;

export function Layout() {
  return (
    <div className="min-h-screen bg-neutral-50 text-neutral-900">
      <header className="border-b border-neutral-200 bg-white">
        <nav className="mx-auto flex max-w-6xl items-center gap-1 px-4 py-2">
          <span className="mr-4 font-semibold">t3-stage</span>
          {NAV.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.to === "/"}
              className={({ isActive }) =>
                `rounded px-3 py-1.5 text-sm ${isActive ? "bg-neutral-900 text-white" : "text-neutral-600 hover:bg-neutral-100"}`
              }
            >
              {item.label}
            </NavLink>
          ))}
        </nav>
      </header>
      <main className="mx-auto max-w-6xl px-4 py-6">
        <Outlet />
      </main>
    </div>
  );
}
