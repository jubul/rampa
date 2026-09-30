// Layout de una organización: barra lateral con navegación, selector de
// organización, usuario y logout, y un header con idioma y tema. Abre el
// stream SSE de la org.

import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { NavLink, Navigate, Outlet, useNavigate, useParams } from "react-router-dom";
import { Boxes, Cloud, LayoutDashboard, Layers, Rocket, LogOut, Menu, Package, Radio, Settings, X } from "lucide-react";
import { useAuth } from "../auth/AuthContext";
import { useLogout } from "../api/hooks";
import { OrgEventsProvider, useLiveEvents } from "../hooks/useOrgEvents";
import { Select } from "../components/ui/Select";
import { Button } from "../components/ui/Button";
import { Logo } from "../components/Logo";
import { PreferencesControls } from "../components/PreferencesControls";
import { roleLabel } from "../lib/status";
import { cn } from "../lib/cn";

const nav = [
  { to: "", key: "summary", icon: LayoutDashboard, end: true },
  { to: "empezar", key: "getStarted", icon: Rocket, end: false },
  { to: "apps", key: "apps", icon: Package, end: false },
  { to: "clusters", key: "clusters", icon: Boxes, end: false },
  { to: "infra", key: "infra", icon: Layers, end: false },
  { to: "cloud-accounts", key: "cloudAccounts", icon: Cloud, end: false },
  { to: "settings", key: "settings", icon: Settings, end: false },
] as const;

export function AppLayout() {
  const { org = "" } = useParams();
  const me = useAuth();
  const membership = me.memberships.find((m) => m.org.slug === org);

  if (!membership) {
    // La org no existe o el usuario no es miembro: volvemos a la raíz, que decide.
    return <Navigate to="/" replace />;
  }

  return (
    <OrgEventsProvider org={org}>
      <Shell org={org} role={membership.role} />
    </OrgEventsProvider>
  );
}

function Shell({ org, role }: { org: string; role: string }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  useEffect(() => setOpen(false), [org]);

  return (
    <div className="flex h-full">
      <aside
        className={cn(
          "fixed inset-y-0 left-0 z-30 flex w-64 flex-col border-r border-slate-200 bg-white transition-transform dark:border-slate-800 dark:bg-slate-900 lg:static lg:translate-x-0",
          open ? "translate-x-0" : "-translate-x-full",
        )}
      >
        <Sidebar org={org} role={role} onNavigate={() => setOpen(false)} />
      </aside>
      {open && <button className="fixed inset-0 z-20 bg-slate-900/30 lg:hidden" aria-label={t("layout.closeMenu")} onClick={() => setOpen(false)} />}
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-12 shrink-0 items-center gap-3 border-b border-slate-200 bg-white px-4 dark:border-slate-800 dark:bg-slate-900">
          <div className="flex items-center gap-3 lg:hidden">
            <Button variant="ghost" size="sm" onClick={() => setOpen((v) => !v)} aria-label={t("layout.menu")} icon={open ? <X className="size-4" /> : <Menu className="size-4" />} />
            <Logo />
          </div>
          <div className="ml-auto">
            <PreferencesControls />
          </div>
        </header>
        <main className="flex-1 overflow-y-auto">
          <div className="mx-auto max-w-6xl px-4 py-6 sm:px-6 lg:px-8">
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  );
}

function Sidebar({ org, role, onNavigate }: { org: string; role: string; onNavigate: () => void }) {
  const { t } = useTranslation();
  const me = useAuth();
  const navigate = useNavigate();
  const logout = useLogout();
  const { state } = useLiveEvents();

  const orgOptions = me.memberships.map((m) => ({ value: m.org.slug, label: m.org.name }));
  orgOptions.push({ value: "__new__", label: t("layout.newOrg") });

  return (
    <>
      <div className="flex h-12 items-center border-b border-slate-200 px-4 dark:border-slate-800">
        <Logo />
      </div>
      <div className="border-b border-slate-200 px-3 py-3 dark:border-slate-800">
        <label className="mb-1 block text-[11px] font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400">{t("layout.organization")}</label>
        <Select
          value={org}
          options={orgOptions}
          aria-label={t("layout.organization")}
          onChange={(e) => {
            const v = e.target.value;
            navigate(v === "__new__" ? "/orgs/new" : `/o/${v}`);
          }}
        />
        <p className="mt-1.5 text-xs text-slate-500 dark:text-slate-400">{t("layout.yourRole", { role: roleLabel(role) })}</p>
      </div>
      <nav className="flex-1 space-y-0.5 px-3 py-3">
        {nav.map((item) => (
          <NavLink
            key={item.to}
            to={item.to === "" ? `/o/${org}` : `/o/${org}/${item.to}`}
            end={item.end}
            onClick={onNavigate}
            className={({ isActive }) =>
              cn(
                "flex items-center gap-2.5 rounded-md px-2.5 py-2 text-sm font-medium",
                isActive
                  ? "bg-slate-100 text-slate-900 dark:bg-slate-800 dark:text-slate-100"
                  : "text-slate-600 hover:bg-slate-50 hover:text-slate-900 dark:text-slate-300 dark:hover:bg-slate-800/60 dark:hover:text-slate-100",
              )
            }
          >
            <item.icon className="size-4" aria-hidden />
            {t(`nav.${item.key}`)}
          </NavLink>
        ))}
      </nav>
      <div className="border-t border-slate-200 px-3 py-3 dark:border-slate-800">
        <div className="mb-2 flex items-center gap-1.5 px-1 text-xs text-slate-500 dark:text-slate-400" title={t("layout.streamTitle")}>
          <Radio className={cn("size-3.5", state === "open" ? "text-green-600 dark:text-green-400" : state === "error" ? "text-red-500 dark:text-red-400" : "text-slate-500 dark:text-slate-400")} aria-hidden />
          {state === "open" ? t("layout.streamOpen") : state === "error" ? t("layout.streamError") : t("layout.streamConnecting")}
        </div>
        <div className="flex items-center justify-between gap-2 px-1">
          <div className="min-w-0">
            <p className="truncate text-sm font-medium text-slate-800 dark:text-slate-100">{me.user.name || me.user.email}</p>
            <p className="truncate text-xs text-slate-500 dark:text-slate-400">{me.user.email}</p>
          </div>
          <Button
            variant="ghost"
            size="sm"
            aria-label={t("layout.logout")}
            title={t("layout.logout")}
            loading={logout.isPending}
            icon={<LogOut className="size-4" />}
            onClick={() => logout.mutate(undefined, { onSettled: () => navigate("/login", { replace: true }) })}
          />
        </div>
      </div>
    </>
  );
}
