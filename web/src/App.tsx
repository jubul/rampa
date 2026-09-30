import { useTranslation } from "react-i18next";
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
import { OnboardingPage } from "./pages/OnboardingPage";
import { EditAppPage, NewAppPage } from "./pages/AppWizardPage";
import { InfraPage } from "./pages/InfraPage";
import { NewClusterPage } from "./pages/NewClusterPage";
import { InfraRunPage } from "./pages/InfraRunPage";
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
      { path: "apps/new", element: <NewAppPage /> },
      { path: "apps/:app", element: <AppDetailPage /> },
      { path: "apps/:app/edit", element: <EditAppPage /> },
      { path: "empezar", element: <OnboardingPage /> },
      { path: "infra", element: <InfraPage /> },
      { path: "infra/new-cluster", element: <NewClusterPage /> },
      { path: "infra/runs/:id", element: <InfraRunPage /> },
      { path: "settings", element: <SettingsPage /> },
      { path: "*", element: <NotFoundPage /> },
    ],
  },
  { path: "*", element: <NotFoundPage /> },
]);

export function App() {
  // Los textos que salen de helpers (estados, validaciones, fechas) no se
  // suscriben al idioma: al cambiarlo remontamos el árbol para que todo se
  // vuelva a renderizar con el idioma nuevo.
  const { i18n } = useTranslation();
  return <RouterProvider key={i18n.resolvedLanguage ?? i18n.language} router={router} />;
}
