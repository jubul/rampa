// Mapeo de estados de la API a tonos de Badge y etiquetas (catálogo i18n, claves status.*).

import i18n from "../i18n";

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

export function toneForStatus(status: string | null | undefined): Tone {
  if (!status) return "gray";
  return toneByStatus[status] ?? "gray";
}

export function labelForStatus(status: string | null | undefined): string {
  if (!status) return "-";
  return i18n.t(`status.${status}`, { defaultValue: status });
}

/** Estados de deployment que todavía pueden cambiar. */
export function isDeploymentActive(status: string): boolean {
  return status === "awaiting_approval" || status === "queued" || status === "applying" || status === "progressing";
}

export function roleLabel(role: string): string {
  return i18n.t(`roles.${role}`, { defaultValue: role });
}

export function envLabel(env: string): string {
  return i18n.t(`env.${env}`, { defaultValue: env });
}

export function actorTypeLabel(type: string | undefined): string {
  if (!type) return "";
  return i18n.t(`actorTypes.${type}`, { defaultValue: type });
}
