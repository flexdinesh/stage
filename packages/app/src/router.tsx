import { createBrowserRouter } from "react-router";
import { Layout } from "./components/Layout.tsx";
import { DashboardPage } from "./pages/DashboardPage.tsx";
import { MachinePage } from "./pages/MachinePage.tsx";
import { MorePage } from "./pages/MorePage.tsx";
import { NotFoundPage } from "./pages/NotFoundPage.tsx";
import { ProjectDetailPage } from "./pages/ProjectDetailPage.tsx";
import { ProjectsPage } from "./pages/ProjectsPage.tsx";
import { SettingsPage } from "./pages/SettingsPage.tsx";
import { TaskDetailPage } from "./pages/TaskDetailPage.tsx";
import { SnippetsPage } from "./pages/SnippetsPage.tsx";
import { ScheduleDetailPage } from "./pages/ScheduleDetailPage.tsx";
import { SchedulesPage } from "./pages/SchedulesPage.tsx";
import { TasksPage } from "./pages/TasksPage.tsx";
import { TreeDetailPage } from "./pages/TreeDetailPage.tsx";
import { TreesPage } from "./pages/TreesPage.tsx";

export const router = createBrowserRouter([
  {
    path: "/",
    element: <Layout />,
    children: [
      { index: true, element: <DashboardPage /> },
      { path: "tasks", element: <TasksPage /> },
      { path: "trees", element: <TreesPage /> },
      { path: "trees/:id", element: <TreeDetailPage /> },
      { path: "projects", element: <ProjectsPage /> },
      { path: "projects/:id", element: <ProjectDetailPage /> },
      { path: "tasks/:id", element: <TaskDetailPage /> },
      { path: "schedules", element: <SchedulesPage /> },
      { path: "schedules/:id", element: <ScheduleDetailPage /> },
      { path: "more", element: <MorePage /> },
      { path: "snippets", element: <SnippetsPage /> },
      { path: "settings", element: <SettingsPage /> },
      { path: "settings/machines/:id", element: <MachinePage /> },
      // Primitives gallery: unlinked, loaded only when visited.
      { path: "dev/ui", lazy: async () => ({ Component: (await import("./pages/DevUiPage.tsx")).DevUiPage }) },
      { path: "*", element: <NotFoundPage /> },
    ],
  },
]);
