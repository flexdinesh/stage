import { createBrowserRouter } from "react-router";
import { Layout } from "./components/Layout.tsx";
import { ChainsPage } from "./pages/ChainsPage.tsx";
import { DashboardPage } from "./pages/DashboardPage.tsx";
import { HistoryPage } from "./pages/HistoryPage.tsx";
import { NotFoundPage } from "./pages/NotFoundPage.tsx";
import { ProjectsPage } from "./pages/ProjectsPage.tsx";
import { SettingsPage } from "./pages/SettingsPage.tsx";
import { TemplatesPage } from "./pages/TemplatesPage.tsx";

export const router = createBrowserRouter([
  {
    path: "/",
    element: <Layout />,
    children: [
      { index: true, element: <DashboardPage /> },
      { path: "projects", element: <ProjectsPage /> },
      { path: "chains", element: <ChainsPage /> },
      { path: "history", element: <HistoryPage /> },
      { path: "templates", element: <TemplatesPage /> },
      { path: "settings", element: <SettingsPage /> },
      { path: "*", element: <NotFoundPage /> },
    ],
  },
]);
