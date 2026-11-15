import { createBrowserRouter } from "react-router";
import { Layout } from "./components/Layout.tsx";
import { DashboardPage } from "./pages/DashboardPage.tsx";
import { SessionsPage } from "./pages/SessionsPage.tsx";
import { SessionDetailPage } from "./pages/SessionDetailPage.tsx";
import { HistoryPage } from "./pages/HistoryPage.tsx";
import { NotFoundPage } from "./pages/NotFoundPage.tsx";
import { ProjectDetailPage } from "./pages/ProjectDetailPage.tsx";
import { ProjectsPage } from "./pages/ProjectsPage.tsx";
import { SettingsPage } from "./pages/SettingsPage.tsx";
import { TaskDetailPage } from "./pages/TaskDetailPage.tsx";
import { SnippetsPage } from "./pages/SnippetsPage.tsx";
import { ScheduleDetailPage } from "./pages/ScheduleDetailPage.tsx";
import { SchedulesPage } from "./pages/SchedulesPage.tsx";

export const router = createBrowserRouter([
  {
    path: "/",
    element: <Layout />,
    children: [
      { index: true, element: <DashboardPage /> },
      { path: "projects", element: <ProjectsPage /> },
      { path: "projects/:id", element: <ProjectDetailPage /> },
      { path: "tasks/:id", element: <TaskDetailPage /> },
      { path: "sessions", element: <SessionsPage /> },
      { path: "sessions/:id", element: <SessionDetailPage /> },
      { path: "schedules", element: <SchedulesPage /> },
      { path: "schedules/:id", element: <ScheduleDetailPage /> },
      { path: "history", element: <HistoryPage /> },
      { path: "snippets", element: <SnippetsPage /> },
      { path: "settings", element: <SettingsPage /> },
      { path: "*", element: <NotFoundPage /> },
    ],
  },
]);
