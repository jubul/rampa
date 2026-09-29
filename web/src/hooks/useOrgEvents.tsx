// Un solo stream SSE por organización, compartido por todas las pantallas.
// Guarda los últimos eventos en memoria e invalida las queries afectadas
// para que scopes, deployments y clusters se refresquen al instante.

import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { openEvents } from "../api/client";
import { keys } from "../api/hooks";
import type { Event } from "../api/types";

const MAX_LIVE_EVENTS = 300;

// Eventos que pueden cambiar el estado de algún paso del onboarding.
const ONBOARDING_KINDS = new Set([
  "cluster.online",
  "cluster.created",
  "cluster.deleted",
  "application.imported",
  "application.deleted",
  "deployment.healthy",
  "cloud_account.verified",
]);

export type StreamState = "connecting" | "open" | "error";

interface LiveEventsContextValue {
  events: Event[];
  state: StreamState;
}

const LiveEventsContext = createContext<LiveEventsContextValue | null>(null);

function payloadString(payload: Record<string, unknown> | undefined, key: string): string | null {
  const v = payload?.[key];
  return typeof v === "string" ? v : null;
}

export function OrgEventsProvider({ org, children }: { org: string; children: ReactNode }) {
  const qc = useQueryClient();
  const [events, setEvents] = useState<Event[]>([]);
  const [state, setState] = useState<StreamState>("connecting");
  const seen = useRef<Set<string>>(new Set());

  const handleEvent = useCallback(
    (ev: Event) => {
      if (seen.current.has(ev.id)) return;
      seen.current.add(ev.id);
      if (seen.current.size > MAX_LIVE_EVENTS * 2) {
        // Evitamos que el set crezca sin límite en sesiones largas.
        seen.current = new Set(Array.from(seen.current).slice(-MAX_LIVE_EVENTS));
      }
      setEvents((prev) => [ev, ...prev].slice(0, MAX_LIVE_EVENTS));

      // Invalidación selectiva según el tipo de sujeto.
      const app = payloadString(ev.payload, "app") ?? payloadString(ev.payload, "application");
      const scope = payloadString(ev.payload, "scope");
      switch (ev.subject_type) {
        case "cluster":
          void qc.invalidateQueries({ queryKey: keys.clusters(org) });
          break;
        case "cloud_account":
          void qc.invalidateQueries({ queryKey: keys.cloudAccounts(org) });
          break;
        case "application":
          void qc.invalidateQueries({ queryKey: keys.apps(org) });
          break;
        case "scope":
        case "deployment":
          if (app) {
            void qc.invalidateQueries({ queryKey: keys.scopes(org, app) });
            if (scope) void qc.invalidateQueries({ queryKey: keys.deployments(org, app, scope) });
            else void qc.invalidateQueries({ queryKey: [...keys.app(org, app), "scopes"] });
          } else {
            // Sin app en el payload no sabemos qué scope tocar: refrescamos todo lo de apps.
            void qc.invalidateQueries({ queryKey: keys.apps(org) });
          }
          if (ev.subject_type === "deployment") {
            void qc.invalidateQueries({ queryKey: keys.deployment(org, ev.subject_id) });
          }
          break;
        case "org":
          void qc.invalidateQueries({ queryKey: keys.org(org) });
          void qc.invalidateQueries({ queryKey: keys.members(org) });
          break;
        default:
          break;
      }
      if (ONBOARDING_KINDS.has(ev.kind)) void qc.invalidateQueries({ queryKey: keys.onboarding(org) });
      // El listado histórico de eventos también se refresca, con un poco de calma.
      void qc.invalidateQueries({ queryKey: ["org", org, "events"] });
    },
    [org, qc],
  );

  useEffect(() => {
    setEvents([]);
    seen.current = new Set();
    setState("connecting");
    const close = openEvents(
      org,
      {},
      {
        onEvent: handleEvent,
        onOpen: () => setState("open"),
        onError: () => setState("error"),
      },
    );
    return close;
  }, [org, handleEvent]);

  const value = useMemo(() => ({ events, state }), [events, state]);
  return <LiveEventsContext.Provider value={value}>{children}</LiveEventsContext.Provider>;
}

export function useLiveEvents(): LiveEventsContextValue {
  const ctx = useContext(LiveEventsContext);
  if (!ctx) return { events: [], state: "connecting" };
  return ctx;
}

export { mergeEvents } from "../lib/events";
