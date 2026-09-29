// Layout de una organización: barra lateral con navegación, selector de
// organización, usuario y logout. Abre el stream SSE de la org.

import { useEffect, useState } from "react";
import { NavLink, Navigate, Outlet, useNavigate, useParams } from "react-router-dom";
import { Boxes, Cloud, LayoutDashboard, LogOut, Menu, Package, Radio, Settings, X } from "lucide-react";
import { useAuth } from "../auth/AuthContext";
import { useLogout } from "../api/hooks";
import { OrgEventsProvider, useLiveEvents } from "../hooks/useOrgEvents";
import { Select } from "../components/ui/Select";
import { Button } from "../components/ui/Button";
import { roleLabels } from "../lib/status";
import { cn } from "../lib/cn";

const nav = [
  { to: "", label: "Resumen", icon: LayoutDashboard, end: true },
  { to: "apps", label: "Aplicaciones", icon: Package, end: false },
  { to: "clusters", label: "Clusters", icon: Boxes, end: false },
  { to: "cloud-accounts", label: "Cuentas de nube", icon: Cloud, end: false },
  { to: "settings", label: "Configuración", icon: Settings, end: false },
];

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
  const [open, setOpen] = useState(false);
  useEffect(() => setOpen(false), [org]);

  return (
    <div className="flex h-full">
      <aside
        className={cn(
          "fixed inset-y-0 left-0 z-30 flex w-64 flex-col border-r border-slate-200 bg-white transition-transform lg:static lg:translate-x-0",
          open ? "translate-x-0" : "-translate-x-full",
        )}
      >
        <Sidebar org={org} role={role} onNavigate={() => setOpen(false)} />
      </aside>
      {open && <button className="fixed inset-0 z-20 bg-slate-900/30 lg:hidden" aria-label="Cerrar menú" onClick={() => setOpen(false)} />}
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-12 items-center gap-3 border-b border-slate-200 bg-white px-4 lg:hidden">
          <Button variant="ghost" size="sm" onClick={() => setOpen((v) => !v)} aria-label="Menú" icon={open ? <X className="size-4" /> : <Menu className="size-4" />} />
          <Brand />
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

function Brand() {
  return (
    <div className="flex items-center gap-2">
      <span className="flex size-6 items-center justify-center rounded-md bg-slate-900 text-accent-500">
        <svg viewBox="0 0 32 32" className="size-4" aria-hidden>
          <path d="M7 23 L25 9 M7 23 H25" stroke="currentColor" strokeWidth="4" strokeLinecap="round" fill="none" />
        </svg>
      </span>
      <span className="text-sm font-semibold tracking-tight">Rampa</span>
    </div>
  );
}

function Sidebar({ org, role, onNavigate }: { org: string; role: string; onNavigate: () => void }) {
  const me = useAuth();
  const navigate = useNavigate();
  const logout = useLogout();
  const { state } = useLiveEvents();

  const orgOptions = me.memberships.map((m) => ({ value: m.org.slug, label: m.org.name }));
  orgOptions.push({ value: "__new__", label: "+ Nueva organización" });

  return (
    <>
      <div className="flex h-12 items-center border-b border-slate-200 px-4">
        <Brand />
      </div>
      <div className="border-b border-slate-200 px-3 py-3">
        <label className="mb-1 block text-[11px] font-medium uppercase tracking-wide text-slate-400">Organización</label>
        <Select
          value={org}
          options={orgOptions}
          aria-label="Organización"
          onChange={(e) => {
            const v = e.target.value;
            navigate(v === "__new__" ? "/orgs/new" : `/o/${v}`);
          }}
        />
        <p className="mt-1.5 text-xs text-slate-500">Tu rol: {roleLabels[role] ?? role}</p>
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
                isActive ? "bg-slate-100 text-slate-900" : "text-slate-600 hover:bg-slate-50 hover:text-slate-900",
              )
            }
          >
            <item.icon className="size-4" aria-hidden />
            {item.label}
          </NavLink>
        ))}
      </nav>
      <div className="border-t border-slate-200 px-3 py-3">
        <div className="mb-2 flex items-center gap-1.5 px-1 text-xs text-slate-500" title="Estado del stream de eventos">
          <Radio className={cn("size-3.5", state === "open" ? "text-green-600" : state === "error" ? "text-red-500" : "text-slate-400")} aria-hidden />
          {state === "open" ? "Eventos en vivo" : state === "error" ? "Reconectando eventos" : "Conectando eventos"}
        </div>
        <div className="flex items-center justify-between gap-2 px-1">
          <div className="min-w-0">
            <p className="truncate text-sm font-medium text-slate-800">{me.user.name || me.user.email}</p>
            <p className="truncate text-xs text-slate-500">{me.user.email}</p>
          </div>
          <Button
            variant="ghost"
            size="sm"
            aria-label="Cerrar sesión"
            title="Cerrar sesión"
            loading={logout.isPending}
            icon={<LogOut className="size-4" />}
            onClick={() => logout.mutate(undefined, { onSettled: () => navigate("/login", { replace: true }) })}
          />
        </div>
      </div>
    </>
  );
}
