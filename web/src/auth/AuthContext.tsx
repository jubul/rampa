// Sesión del usuario: GET /v1/me. Si responde 401 mandamos a /login.

import { createContext, useContext, useEffect, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Navigate, useLocation } from "react-router-dom";
import { useMe } from "../api/hooks";
import { isApiError } from "../api/client";
import type { Me, Role } from "../api/types";
import { FullPageSpinner } from "../components/ui/Spinner";
import { ErrorState } from "../components/ui/States";
import { currentLocale, normalizeLocale, setLocale } from "../i18n";

const AuthContext = createContext<Me | null>(null);

/**
 * La preferencia del usuario (GET /me locale) gana sobre el navegador. Solo
 * reaccionamos cuando cambia el locale que vino del servidor, así un cambio
 * local no se pisa mientras el PATCH está en vuelo.
 */
function useSyncLocale(serverLocale: string | null | undefined) {
  useEffect(() => {
    const pref = normalizeLocale(serverLocale);
    if (pref && pref !== currentLocale()) setLocale(pref);
  }, [serverLocale]);
}

export function RequireAuth({ children }: { children: ReactNode }) {
  const { t } = useTranslation();
  const me = useMe();
  const location = useLocation();
  useSyncLocale(me.data?.locale);

  if (me.isPending) return <FullPageSpinner label={t("auth.loadingSession")} />;
  if (me.isError) {
    if (isApiError(me.error) && me.error.isUnauthorized) {
      return <Navigate to="/login" replace state={{ from: location.pathname }} />;
    }
    return (
      <div className="flex h-full items-center justify-center p-8">
        <ErrorState title={t("auth.sessionError")} error={me.error} onRetry={() => void me.refetch()} />
      </div>
    );
  }
  return <AuthContext.Provider value={me.data}>{children}</AuthContext.Provider>;
}

export function useAuth(): Me {
  const ctx = useContext(AuthContext);
  if (!ctx) {
    throw new Error("useAuth must be used inside RequireAuth");
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
