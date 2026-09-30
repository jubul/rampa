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
import i18n from "../i18n";

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

export function toneForRun(status: string): Tone {
  return runTone[status] ?? "gray";
}

export function labelForRun(status: string): string {
  return i18n.t(`runStatus.${status}`, { defaultValue: status });
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

// Formulario de cluster o máquina, armado desde los inputs del módulo

/** Inputs que el formulario ya pide con campos fijos (cuenta, nombre, entorno). */
const FIXED_INPUTS = new Set(["name", "env", "cloud_account_id"]);
/** Inputs que arma la API o son secretos: nunca se piden acá. */
const HIDDEN_INPUTS = /^(api_url|agent_chart_url|bootstrap_token|agent_token|cluster_token)$|token|secret|password/i;

export type InputKind = "select" | "text" | "number" | "bool" | "list";

export function inputKind(input: InfraModuleInput): InputKind {
  const type = (input.type ?? "").toLowerCase();
  if (input.options && input.options.length > 0) return "select";
  if (type === "bool" || type === "boolean") return "bool";
  if (type === "number" || type === "int" || type === "integer") return "number";
  if (type === "list" || type === "array" || Array.isArray(input.default)) return "list";
  if (type === "" && /(_arns|_cidrs|_ids|_keys|_list)$/.test(input.name)) return "list";
  return "text";
}

/** Inputs que se renderizan en el formulario (sin los fijos ni los secretos). */
export function visibleInputs(inputs: InfraModuleInput[] | undefined): InfraModuleInput[] {
  return (inputs ?? []).filter((i) => !FIXED_INPUTS.has(i.name) && !i.secret && !HIDDEN_INPUTS.test(i.name));
}

export function findInput(inputs: InfraModuleInput[] | undefined, name: string): InfraModuleInput | undefined {
  return inputs?.find((i) => i.name === name);
}

function defaultText(input: InfraModuleInput): string {
  const d = input.default;
  if (Array.isArray(d)) return d.map(String).join("\n");
  if (d === undefined || d === null) return "";
  return String(d);
}

/** Default de un input: su default, si no la primera opción (en selects), si no vacío. */
export function inputDefault(input: InfraModuleInput | undefined, fallback = ""): string {
  if (!input) return fallback;
  const d = defaultText(input);
  if (d !== "") return d;
  if (input.options && input.options.length > 0) return input.options[0] ?? fallback;
  if (inputKind(input) === "bool") return "false";
  return fallback;
}

/** Valores iniciales (todo como texto) a partir de los inputs del módulo. */
export function initialInputValues(inputs: InfraModuleInput[] | undefined): Record<string, string> {
  const out: Record<string, string> = {};
  for (const i of visibleInputs(inputs)) out[i.name] = inputDefault(i);
  return out;
}

/** Separa por líneas o comas, recorta y elimina duplicados y vacíos. */
export function parseList(text: string): string[] {
  const seen = new Set<string>();
  for (const part of text.split(/[\n,]+/)) {
    const v = part.trim();
    if (v) seen.add(v);
  }
  return Array.from(seen);
}

/** Valores del formulario a los tipos del input; los opcionales vacíos se omiten. */
export function inputsToPayload(inputs: InfraModuleInput[] | undefined, values: Record<string, string>): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const i of visibleInputs(inputs)) {
    const raw = values[i.name] ?? "";
    switch (inputKind(i)) {
      case "list": {
        const list = parseList(raw);
        if (list.length > 0) out[i.name] = list;
        break;
      }
      case "bool":
        out[i.name] = raw === "true";
        break;
      case "number":
        if (raw.trim() !== "" && !Number.isNaN(Number(raw))) out[i.name] = Number(raw);
        break;
      default:
        if (raw.trim() !== "") out[i.name] = raw.trim();
    }
  }
  return out;
}

export interface ClusterFormBase {
  cloud_account_id: string;
  name: string;
  env: ClusterEnv;
}

export type ClusterFormErrors = Record<string, string>;

const NAME_RE = /^[a-z][a-z0-9-]{0,38}[a-z0-9]$|^[a-z]$/;

export function validateClusterForm(
  base: ClusterFormBase,
  inputs: InfraModuleInput[] | undefined,
  values: Record<string, string>,
): ClusterFormErrors {
  const errors: ClusterFormErrors = {};
  if (!base.cloud_account_id) errors.cloud_account_id = i18n.t("validation.pickAccount");
  if (base.name.trim() === "") errors.name = i18n.t("validation.nameRequired");
  else if (!NAME_RE.test(base.name.trim())) errors.name = i18n.t("validation.nameFormat");
  for (const i of visibleInputs(inputs)) {
    const raw = values[i.name] ?? "";
    const kind = inputKind(i);
    if (i.required && (kind === "list" ? parseList(raw).length === 0 : raw.trim() === "")) {
      errors[i.name] = i18n.t("validation.required");
      continue;
    }
    if (kind === "number" && raw.trim() !== "" && Number.isNaN(Number(raw))) errors[i.name] = i18n.t("validation.number");
    if (kind === "list" && /arn/i.test(i.name)) {
      const bad = parseList(raw).find((v) => !v.startsWith("arn:"));
      if (bad) errors[i.name] = i18n.t("validation.notArn", { value: bad });
    }
  }
  return errors;
}

/**
 * Arma el body de POST /infra/clusters: cuenta, nombre, entorno, módulo e
 * inputs. Los campos históricos del contrato (kubernetes_version, domain,
 * ingress_class, admin_principal_arns) se repiten a nivel raíz si el módulo
 * los declara, así funciona con un servidor que todavía los lea de ahí.
 */
export function buildClusterRequest(
  base: ClusterFormBase,
  module: Pick<InfraModule, "module" | "inputs">,
  values: Record<string, string>,
): CreateInfraClusterRequest {
  const inputs = inputsToPayload(module.inputs, values);
  const req: CreateInfraClusterRequest = {
    cloud_account_id: base.cloud_account_id,
    name: base.name.trim(),
    env: base.env,
    module: module.module ?? "",
    inputs,
  };
  if (typeof inputs.kubernetes_version === "string") req.kubernetes_version = inputs.kubernetes_version;
  if (typeof inputs.domain === "string") req.domain = inputs.domain;
  if (typeof inputs.ingress_class === "string") req.ingress_class = inputs.ingress_class;
  if (Array.isArray(inputs.admin_principal_arns)) req.admin_principal_arns = inputs.admin_principal_arns as string[];
  return req;
}

/** Módulos de un kind disponibles y los que todavía no, opcionalmente de un solo provider. */
export function splitModules(
  items: InfraModule[],
  kind: string,
  provider?: string,
): { available: InfraModule[]; upcoming: InfraModule[] } {
  const list = items.filter((m) => m.kind === kind && (provider === undefined || m.provider === provider));
  return { available: list.filter((m) => m.available), upcoming: list.filter((m) => !m.available) };
}

const COST_RE = /(US?\$\s?\d|\$\s?\d|\d[\d.,]*\s?(USD|EUR|€)|€\s?\d)/i;

/**
 * Costo aproximado del tamaño elegido, si el módulo lo trae en la descripción
 * (del input o del módulo). Busca una línea que mencione el valor elegido y un
 * importe; si no hay por valor, usa la primera línea con importe de la
 * descripción del input. Devuelve null si no hay nada.
 */
export function costHint(module: Pick<InfraModule, "description" | "inputs">, chosen: { input?: string; value: string }): string | null {
  const input = chosen.input ? findInput(module.inputs, chosen.input) : undefined;
  const sources = [input?.description, input?.help, module.description].filter((x): x is string => !!x);
  const value = chosen.value.trim();
  for (const src of sources) {
    const lines = src.split(/\n|;/).map((l) => l.trim()).filter(Boolean);
    if (value) {
      const hit = lines.find((l) => l.includes(value) && COST_RE.test(l));
      if (hit) return hit;
    }
  }
  for (const src of [input?.description, input?.help].filter((x): x is string => !!x)) {
    const hit = src.split(/\n|;/).map((l) => l.trim()).find((l) => COST_RE.test(l));
    if (hit) return hit;
  }
  return null;
}

/** Nombre del input que representa el tamaño de la máquina, si existe. */
export function sizeInputName(inputs: InfraModuleInput[] | undefined): string | undefined {
  return visibleInputs(inputs).find((i) => /^(size|droplet_size|instance_type|shape|plan|machine_type)$/.test(i.name))?.name;
}

export function outputEntries(run: Pick<InfraRun, "outputs">): [string, string][] {
  return Object.entries(run.outputs ?? {}).map(([k, v]) => [k, typeof v === "string" ? v : JSON.stringify(v)]);
}
