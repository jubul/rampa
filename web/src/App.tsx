import { createBrowserRouter, RouterProvider } from "react-router-dom";
import { RequireAuth } from "./auth/AuthContext";
import { AppLayout } from "./layout/AppLayout";
import { LoginPage } from "./pages/LoginPage";
import { RootRedirect } from "./pages/RootRedirect";
import { NewOrgPage } from "./pages/NewOrgPage";
import { DashboardPage } from "./pages/DashboardPage";
import { CloudAccountsPage } from "./pages/CloudAccountsPage";
import { ClustersPage } from "./pages/ClustersPage";
import { AppsPage } from "./pages/AppsPage";
import { AppDetailPage } from "./pages/AppDetailPage";
import { SettingsPage } from "./pages/SettingsPage";
import { NotFoundPage } from "./pages/NotFoundPage";

const router = createBrowserRouter([
  { path: "/login", element: <LoginPage /> },
  {
    path: "/",
    element: (
      <RequireAuth>
        <RootRedirect />
      </RequireAuth>
    ),
  },
  {
    path: "/orgs/new",
    element: (
      <RequireAuth>
        <NewOrgPage />
      </RequireAuth>
    ),
  },
  {
    path: "/o/:org",
    element: (
      <RequireAuth>
        <AppLayout />
      </RequireAuth>
    ),
    children: [
      { index: true, element: <DashboardPage /> },
      { path: "cloud-accounts", element: <CloudAccountsPage /> },
      { path: "clusters", element: <ClustersPage /> },
      { path: "apps", element: <AppsPage /> },
      { path: "apps/:app", element: <AppDetailPage /> },
      { path: "settings", element: <SettingsPage /> },
      { path: "*", element: <NotFoundPage /> },
    ],
  },
  { path: "*", element: <NotFoundPage /> },
]);

export function App() {
  return <RouterProvider router={router} />;
}
