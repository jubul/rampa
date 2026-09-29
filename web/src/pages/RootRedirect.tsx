import { Navigate } from "react-router-dom";
import { useAuth } from "../auth/AuthContext";

/** "/" va a la primera organización del usuario, o al alta si no tiene ninguna. */
export function RootRedirect() {
  const me = useAuth();
  const first = me.memberships[0];
  return <Navigate to={first ? `/o/${first.org.slug}` : "/orgs/new"} replace />;
}
