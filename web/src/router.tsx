import { createBrowserRouter } from "react-router";
import { Layout } from "./components/Layout.tsx";
import { ChainDetailPage } from "./pages/ChainDetailPage.tsx";
import { ChainsPage } from "./pages/ChainsPage.tsx";
import { DashboardPage } from "./pages/DashboardPage.tsx";
import { HistoryPage } from "./pages/HistoryPage.tsx";
import { NotFoundPage } from "./pages/NotFoundPage.tsx";
import { ProjectDetailPage } from "./pages/ProjectDetailPage.tsx";
import { ProjectsPage } from "./pages/ProjectsPage.tsx";
import { SettingsPage } from "./pages/SettingsPage.tsx";
import { TaskDetailPage } from "./pages/TaskDetailPage.tsx";
import { TemplatesPage } from "./pages/TemplatesPage.tsx";

export const router = createBrowserRouter([
  {
    path: "/",
    element: <Layout />,
    children: [
      { index: true, element: <DashboardPage /> },
      { path: "projects", element: <ProjectsPage /> },
      { path: "projects/:id", element: <ProjectDetailPage /> },
      { path: "tasks/:id", element: <TaskDetailPage /> },
      { path: "chains", element: <ChainsPage /> },
      { path: "chains/:id", element: <ChainDetailPage /> },
      { path: "history", element: <HistoryPage /> },
      { path: "templates", element: <TemplatesPage /> },
      { path: "settings", element: <SettingsPage /> },
      { path: "*", element: <NotFoundPage /> },
    ],
  },
]);
