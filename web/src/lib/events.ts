// Lógica pura sobre eventos: filtros y merges. Sin React, para poder testearla.

import type { Application, Event } from "../api/types";

/**
 * Un evento pertenece a la app si su sujeto es la app, uno de sus deployments,
 * o si el payload trae el nombre o el id de la app. Los scopes no tienen id en
 * la API, así que sus eventos solo se pueden asociar por payload.
 */
export function eventBelongsToApp(e: Event, app: Pick<Application, "id" | "name">, deploymentIds: Set<string>): boolean {
  if (e.subject_type === "application" && e.subject_id === app.id) return true;
  if (e.subject_type === "deployment" && deploymentIds.has(e.subject_id)) return true;
  const p = e.payload ?? {};
  const name = p["app"] ?? p["application"] ?? p["app_name"];
  if (name === app.name) return true;
  const appId = p["application_id"] ?? p["app_id"];
  return appId === app.id;
}

/** Une eventos históricos y en vivo, sin duplicados, del más nuevo al más viejo. */
export function mergeEvents(live: Event[], history: Event[] | undefined, limit = 100): Event[] {
  const byId = new Map<string, Event>();
  for (const e of live) byId.set(e.id, e);
  for (const e of history ?? []) if (!byId.has(e.id)) byId.set(e.id, e);
  const all = Array.from(byId.values());
  all.sort((a, b) => (a.created_at < b.created_at ? 1 : a.created_at > b.created_at ? -1 : 0));
  return all.slice(0, limit);
}
