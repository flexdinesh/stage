import type { ComponentType } from "react";
import { Navigate, createBrowserRouter, type RouteObject } from "react-router";
import { SETTINGS_PATH } from "./lib/settings.ts";
import { SkeletonRows } from "./components/ui.tsx";
import { Layout } from "./components/Layout.tsx";
import { AccessGate } from "./components/access/AccessGate.tsx";
import { RouteError } from "./components/ErrorBoundary.tsx";
import { DashboardPage } from "./pages/DashboardPage.tsx";
import { MorePage } from "./pages/MorePage.tsx";
import { NotFoundPage } from "./pages/NotFoundPage.tsx";
import { ProjectDetailPage } from "./pages/ProjectDetailPage.tsx";
import { ProjectsPage } from "./pages/ProjectsPage.tsx";
import { TaskDetailPage, TaskRoute } from "./pages/TaskDetailPage.tsx";
import { SessionRoute } from "./components/session/SessionView.tsx";
import { TasksPage } from "./pages/TasksPage.tsx";
import { TreesPage } from "./pages/TreesPage.tsx";

/**
 * A page loaded on first visit (its own chunk). Navigating there keeps the current page until it loads; opening it
 * directly shows skeleton rows inside the layout meanwhile.
 */
function lazyPage(load: () => Promise<ComponentType>) {
  return { lazy: async () => ({ Component: await load() }), hydrateFallbackElement: <SkeletonRows /> };
}

const workRoutes: RouteObject[] = [
  {
    path: "/",
    element: <Layout />,
    // A layout-level fallback for errors in the shell itself.
    errorElement: <RouteError />,
    children: [
      {
        // Page errors render inside the layout, so the sidebar and navigation survive.
        errorElement: <RouteError />,
        children: [
          { index: true, element: <DashboardPage /> },
          { path: "tasks", element: <TasksPage /> },
          { path: "trees", element: <TreesPage /> },
          { path: "trees/:id", ...lazyPage(async () => (await import("./pages/TreeDetailPage.tsx")).TreeDetailPage) },
          { path: "projects", element: <ProjectsPage /> },
          { path: "projects/:id", element: <ProjectDetailPage /> },
          {
            path: "tasks/:id",
            element: <TaskDetailPage />,
            children: [
              { index: true, element: <TaskRoute /> },
              { path: "session/:attempt?", element: <SessionRoute /> },
            ],
          },
          { path: "schedules", ...lazyPage(async () => (await import("./pages/SchedulesPage.tsx")).SchedulesPage) },
          { path: "schedules/:id", ...lazyPage(async () => (await import("./pages/ScheduleDetailPage.tsx")).ScheduleDetailPage) },
          { path: "connections/:connectionId/sessions/:sessionId", ...lazyPage(async () => (await import("./pages/SessionRefPage.tsx")).SessionRefPage) },
          { path: "sessions/:sessionId", ...lazyPage(async () => (await import("./pages/SessionRefPage.tsx")).SessionRefPage) },
          { path: "more", element: <MorePage /> },
          { path: "snippets", ...lazyPage(async () => (await import("./pages/SnippetsPage.tsx")).SnippetsPage) },
          { path: "settings", ...lazyPage(async () => (await import("./pages/SettingsPage.tsx")).SettingsGeneralPage) },
          { path: "settings/connections", ...lazyPage(async () => (await import("./pages/SettingsPage.tsx")).SettingsConnectionsPage) },
          { path: "settings/notifications", ...lazyPage(async () => (await import("./pages/SettingsPage.tsx")).SettingsNotificationsPage) },
          { path: "settings/account", ...lazyPage(async () => (await import("./pages/SettingsPage.tsx")).SettingsAccountPage) },
          { path: "admin", element: <Navigate to="/admin/people" replace /> },
          { path: "admin/people", ...lazyPage(async () => (await import("./pages/AdminPage.tsx")).AdminPeoplePage) },
          { path: "admin/invitations", ...lazyPage(async () => (await import("./pages/AdminPage.tsx")).AdminInvitationsPage) },
          // Machine pages became rows of Connections.
          { path: "settings/machines/:id", element: <Navigate to={SETTINGS_PATH.connections} replace /> },
          // Primitives gallery: unlinked.
          { path: "dev/ui", ...lazyPage(async () => (await import("./pages/DevUiPage.tsx")).DevUiPage) },
          { path: "*", element: <NotFoundPage /> },
        ],
      },
    ],
  },
];

export const router = createBrowserRouter([{ element: <AccessGate />, children: workRoutes }]);
