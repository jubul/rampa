// Lógica pura de infraestructura: estados, parser del stream de log y
// conversión de los inputs de un módulo a valores del formulario.

import type {
  ClusterEnv,
  CreateInfraClusterRequest,
  InfraModule,
  InfraModuleInput,
  InfraRun,
} from "../api/types";
import type { Tone } from "./status";

const runTone: Record<string, Tone> = {
  queued: "amber",
  initializing: "amber",
  planning: "amber",
  applying: "amber",
  succeeded: "green",
  failed: "red",
  destroying: "gray",
  destroyed: "gray",
};

const runLabel: Record<string, string> = {
  queued: "En cola",
  initializing: "Inicializando",
  planning: "Planificando",
  applying: "Aplicando",
  succeeded: "Listo",
  failed: "Falló",
  destroying: "Destruyendo",
  destroyed: "Destruido",
};

export function toneForRun(status: string): Tone {
  return runTone[status] ?? "gray";
}

export function labelForRun(status: string): string {
  return runLabel[status] ?? status;
}

/** Estados en los que el run todavía avanza y conviene refrescar. */
export function isRunActive(status: string): boolean {
  return ["queued", "initializing", "planning", "applying", "destroying"].includes(status);
}

export function isRunFinal(status: string): boolean {
  return status === "succeeded" || status === "failed" || status === "destroyed";
}

// Stream de log

export type LogStreamEvent =
  | { type: "line"; text: string }
  | { type: "status"; status: string }
  | { type: "done" };

/** Interpreta un evento SSE crudo (nombre y data). Devuelve null si no es del contrato. */
export function parseLogEvent(name: string, data: unknown): LogStreamEvent | null {
  const text = typeof data === "string" ? data : "";
  switch (name) {
    case "line":
      return { type: "line", text };
    case "status": {
      const status = text.trim();
      return status === "" ? null : { type: "status", status };
    }
    case "done":
      return { type: "done" };
    default:
      return null;
  }
}

export interface LogState {
  lines: string[];
  status: string;
  done: boolean;
}

export const MAX_LOG_LINES = 5000;

export function initialLogState(seed: string[] = [], status = ""): LogState {
  return { lines: seed.slice(-MAX_LOG_LINES), status, done: false };
}

export type LogAction = LogStreamEvent | { type: "reset"; seed?: string[] };

/**
 * Reductor del log. Al abrir (o reabrir) el stream el servidor reenvía las
 * líneas guardadas, así que "reset" descarta el buffer para no duplicar.
 */
export function logReducer(state: LogState, action: LogAction): LogState {
  switch (action.type) {
    case "reset":
      return { lines: (action.seed ?? []).slice(-MAX_LOG_LINES), status: state.status, done: false };
    case "line": {
      const lines = state.lines.length >= MAX_LOG_LINES ? state.lines.slice(-(MAX_LOG_LINES - 1)) : state.lines.slice();
      lines.push(action.text);
      return { ...state, lines };
    }
    case "status":
      return { ...state, status: action.status };
    case "done":
      return { ...state, done: true };
  }
}

// Formulario de cluster

export interface ClusterFormValues {
  cloud_account_id: string;
  name: string;
  env: ClusterEnv;
  kubernetes_version: string;
  domain: string;
  ingress_class: string;
  admin_arns: string;
}

export const FALLBACK_K8S_VERSION = "1.33";

export function findInput(inputs: InfraModuleInput[] | undefined, name: string): InfraModuleInput | undefined {
  return inputs?.find((i) => i.name === name);
}

/** Default de un input: su default, si no la primera opción, si no el fallback. */
export function inputDefault(input: InfraModuleInput | undefined, fallback = ""): string {
  if (!input) return fallback;
  if (input.default !== undefined && input.default !== "") return input.default;
  return input.options?.[0] ?? fallback;
}

/** Valores iniciales del formulario a partir de los inputs del módulo. */
export function inputsToFormValues(inputs: InfraModuleInput[] | undefined): ClusterFormValues {
  return {
    cloud_account_id: "",
    name: "",
    env: "nonprod",
    kubernetes_version: inputDefault(findInput(inputs, "kubernetes_version"), FALLBACK_K8S_VERSION),
    domain: inputDefault(findInput(inputs, "domain")),
    ingress_class: inputDefault(findInput(inputs, "ingress_class"), "nginx"),
    admin_arns: "",
  };
}

/** Separa por líneas o comas, recorta y elimina duplicados y vacíos. */
export function parseArns(text: string): string[] {
  const seen = new Set<string>();
  for (const part of text.split(/[\n,]+/)) {
    const v = part.trim();
    if (v) seen.add(v);
  }
  return Array.from(seen);
}

export function formValuesToRequest(v: ClusterFormValues): CreateInfraClusterRequest {
  return {
    cloud_account_id: v.cloud_account_id,
    name: v.name.trim(),
    env: v.env,
    kubernetes_version: v.kubernetes_version.trim(),
    domain: v.domain.trim(),
    ingress_class: v.ingress_class.trim() || "nginx",
    admin_principal_arns: parseArns(v.admin_arns),
  };
}

export function validateClusterForm(v: ClusterFormValues): Partial<Record<keyof ClusterFormValues, string>> {
  const errors: Partial<Record<keyof ClusterFormValues, string>> = {};
  if (!v.cloud_account_id) errors.cloud_account_id = "Elegí una cuenta conectada";
  if (v.name.trim() === "") errors.name = "El nombre es obligatorio";
  else if (!/^[a-z][a-z0-9-]{0,38}[a-z0-9]$|^[a-z]$/.test(v.name.trim())) errors.name = "Minúsculas, dígitos y guiones; empieza con letra";
  if (v.domain.trim() === "") errors.domain = "El dominio es obligatorio";
  if (v.kubernetes_version.trim() === "") errors.kubernetes_version = "Elegí una versión";
  for (const arn of parseArns(v.admin_arns)) {
    if (!arn.startsWith("arn:")) {
      errors.admin_arns = `"${arn}" no parece un ARN (empieza con arn:)`;
      break;
    }
  }
  return errors;
}

/** Módulos de cluster disponibles y los que todavía no. */
export function splitClusterModules(items: InfraModule[]): { available: InfraModule[]; upcoming: InfraModule[] } {
  const clusters = items.filter((m) => m.kind === "cluster");
  return { available: clusters.filter((m) => m.available), upcoming: clusters.filter((m) => !m.available) };
}

export const providerNames: Record<string, string> = {
  aws: "AWS",
  gcp: "Google Cloud",
  azure: "Azure",
  digitalocean: "DigitalOcean",
  oci: "Oracle Cloud",
};

export function outputEntries(run: Pick<InfraRun, "outputs">): [string, string][] {
  return Object.entries(run.outputs ?? {}).map(([k, v]) => [k, typeof v === "string" ? v : JSON.stringify(v)]);
}
