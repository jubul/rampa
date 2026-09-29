// Mapeo de estados de la API a tonos de Badge y etiquetas en español.

export type Tone = "green" | "red" | "amber" | "violet" | "gray" | "blue";

const toneByStatus: Record<string, Tone> = {
  // deployments
  awaiting_approval: "violet",
  queued: "amber",
  applying: "amber",
  progressing: "amber",
  healthy: "green",
  failed: "red",
  cancelled: "gray",
  // scopes
  unbound: "gray",
  idle: "gray",
  deploying: "amber",
  degraded: "amber",
  deleting: "gray",
  // clusters
  online: "green",
  offline: "gray",
  // cuentas de nube
  pending: "amber",
  connected: "green",
};

const labelByStatus: Record<string, string> = {
  awaiting_approval: "Espera aprobación",
  queued: "En cola",
  applying: "Aplicando",
  progressing: "Progresando",
  healthy: "Sano",
  failed: "Falló",
  cancelled: "Cancelado",
  unbound: "Sin cluster",
  idle: "Sin desplegar",
  deploying: "Desplegando",
  degraded: "Degradado",
  deleting: "Eliminando",
  online: "En línea",
  offline: "Desconectado",
  pending: "Pendiente",
  connected: "Conectada",
};

export function toneForStatus(status: string | null | undefined): Tone {
  if (!status) return "gray";
  return toneByStatus[status] ?? "gray";
}

export function labelForStatus(status: string | null | undefined): string {
  if (!status) return "-";
  return labelByStatus[status] ?? status;
}

/** Estados de deployment que todavía pueden cambiar. */
export function isDeploymentActive(status: string): boolean {
  return status === "awaiting_approval" || status === "queued" || status === "applying" || status === "progressing";
}

export const roleLabels: Record<string, string> = {
  owner: "Owner",
  admin: "Admin",
  developer: "Developer",
  viewer: "Viewer",
};

export const envLabels: Record<string, string> = {
  nonprod: "No prod",
  prod: "Prod",
};

export const actorTypeLabels: Record<string, string> = {
  user: "usuario",
  token: "token",
  agent: "agente",
  system: "sistema",
};
