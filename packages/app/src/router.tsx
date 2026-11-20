import { Navigate, createBrowserRouter } from "react-router";
import { Layout } from "./components/Layout.tsx";
import { DashboardPage } from "./pages/DashboardPage.tsx";
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

export const router = createBrowserRouter([
  {
    path: "/",
    element: <Layout />,
    children: [
      { index: true, element: <DashboardPage /> },
      { path: "tasks", element: <TasksPage /> },
      // The Trees list became the Tasks view; old links land there.
      { path: "trees", element: <Navigate to="/tasks" replace /> },
      { path: "trees/:id", element: <TreeDetailPage /> },
      { path: "projects", element: <ProjectsPage /> },
      { path: "projects/:id", element: <ProjectDetailPage /> },
      { path: "tasks/:id", element: <TaskDetailPage /> },
      { path: "schedules", element: <SchedulesPage /> },
      { path: "schedules/:id", element: <ScheduleDetailPage /> },
      { path: "more", element: <MorePage /> },
      { path: "snippets", element: <SnippetsPage /> },
      { path: "settings", element: <SettingsPage /> },
      { path: "*", element: <NotFoundPage /> },
    ],
  },
]);
