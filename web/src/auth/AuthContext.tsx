// Sesión del usuario: GET /v1/me. Si responde 401 mandamos a /login.

import { createContext, useContext, type ReactNode } from "react";
import { Navigate, useLocation } from "react-router-dom";
import { useMe } from "../api/hooks";
import { isApiError } from "../api/client";
import type { Me, Role } from "../api/types";
import { FullPageSpinner } from "../components/ui/Spinner";
import { ErrorState } from "../components/ui/States";

const AuthContext = createContext<Me | null>(null);

export function RequireAuth({ children }: { children: ReactNode }) {
  const me = useMe();
  const location = useLocation();

  if (me.isPending) return <FullPageSpinner label="Cargando sesión" />;
  if (me.isError) {
    if (isApiError(me.error) && me.error.isUnauthorized) {
      return <Navigate to="/login" replace state={{ from: location.pathname }} />;
    }
    return (
      <div className="flex h-full items-center justify-center p-8">
        <ErrorState title="No se pudo cargar la sesión" error={me.error} onRetry={() => void me.refetch()} />
      </div>
    );
  }
  return <AuthContext.Provider value={me.data}>{children}</AuthContext.Provider>;
}

export function useAuth(): Me {
  const ctx = useContext(AuthContext);
  if (!ctx) {
    throw new Error("useAuth debe usarse dentro de RequireAuth");
  }
  return ctx;
}

const roleRank: Record<Role, number> = { owner: 4, admin: 3, developer: 2, viewer: 1 };

/** Rol del usuario en la organización actual y helpers de permiso. */
export function useOrgRole(orgSlug: string) {
  const me = useAuth();
  const membership = me.memberships.find((m) => m.org.slug === orgSlug);
  const role: Role | null = membership?.role ?? null;
  const atLeast = (min: Role) => role !== null && roleRank[role] >= roleRank[min];
  return {
    role,
    membership,
    isViewer: atLeast("viewer"),
    isDeveloper: atLeast("developer"),
    isAdmin: atLeast("admin"),
    isOwner: atLeast("owner"),
  };
}
