// Lógica pura del wizard de aplicaciones: spec por defecto, validaciones,
// normalización antes de enviar y agrupación de errores por paso.

import i18n from "../i18n";
import type {
  ApplicationSpec,
  SpecParameter,
  SpecScope,
  SpecService,
} from "../api/types";

export const SCOPE_ORDER = ["dev", "staging", "prod", "pr"] as const;
export const PERMANENT_SCOPES = ["dev", "staging", "prod"] as const;

// Rangos de request a limit; se muestran con el separador "–" en ambos idiomas.
export const SIZES = [
  { value: "S", cpu: "100m - 500m", memory: "128Mi - 512Mi" },
  { value: "M", cpu: "250m - 1000m", memory: "256Mi - 1Gi" },
  { value: "L", cpu: "500m - 2000m", memory: "512Mi - 2Gi" },
  { value: "XL", cpu: "1000m - 4000m", memory: "1Gi - 4Gi" },
] as const;

/** Los textos de cada tipo salen del catálogo: appTypes.<value>.label y .help. */
export const APP_TYPES = [{ value: "api" }, { value: "worker" }, { value: "cron" }, { value: "static" }] as const;

export const RUNTIMES = ["go", "node", "python", "static"] as const;
export const STRATEGIES = ["rolling", "canary", "bluegreen"] as const;
export const SERVICE_TYPES = ["postgres", "redis", "sqs", "s3", "dynamodb"] as const;

/** Los textos de cada tier salen del catálogo: tiers.<value>.label y .help. */
export const TIERS = [{ value: 1 }, { value: 2 }, { value: 3 }] as const;

const NAME_MAX = 40;
const NAME_RE = /^[a-z]([a-z0-9-]*[a-z0-9])?$/;

/**
 * Nombre dns-safe: minúsculas, dígitos y guiones, empieza con letra y termina
 * con letra o dígito. Máximo 40 porque el namespace es <app>-<scope>. La API
 * tiene la última palabra; esto solo da feedback en vivo.
 */
export function validateAppName(name: string): string | null {
  const t = i18n.t.bind(i18n);
  if (name === "") return t("validation.nameRequired");
  if (name.length > NAME_MAX) return t("validation.maxChars", { max: NAME_MAX });
  if (!/^[a-z]/.test(name)) return t("validation.startLower");
  if (!/[a-z0-9]$/.test(name)) return t("validation.noTrailingDash");
  if (!NAME_RE.test(name)) return t("validation.lowerDigitsDash");
  return null;
}

const PARAM_NAME_RE = /^[A-Z][A-Z0-9_]*$/;

export function validateParamName(name: string): string | null {
  if (name === "") return i18n.t("validation.nameRequired");
  if (!PARAM_NAME_RE.test(name)) return i18n.t("validation.upperDigitsUnderscore");
  return null;
}

const DEFAULTS_BY_SCOPE: Record<string, Pick<SpecScope, "size" | "replicas" | "strategy" | "ttl">> = {
  dev: { size: "S", replicas: 1, strategy: "rolling", ttl: "" },
  staging: { size: "S", replicas: 2, strategy: "rolling", ttl: "" },
  prod: { size: "M", replicas: 3, strategy: "canary", ttl: "" },
  pr: { size: "S", replicas: 1, strategy: "rolling", ttl: "72h" },
};

export function defaultScope(name: string): SpecScope {
  const d = DEFAULTS_BY_SCOPE[name] ?? DEFAULTS_BY_SCOPE.dev!;
  return { name, ...d, domain: "", autoscaling: null };
}

/** Spec inicial del wizard: dev, prod y plantilla pr. */
export function defaultSpec(): ApplicationSpec {
  return {
    name: "",
    team: "",
    tier: 2,
    description: "",
    type: "api",
    runtime: "go",
    port: 8080,
    healthcheck: "/health",
    schedule: "",
    scopes: [defaultScope("dev"), defaultScope("prod"), defaultScope("pr")],
    parameters: [],
    services: [],
  };
}

export function defaultParameter(): SpecParameter {
  return { name: "", source: "value", value: "", scopes: [] };
}

export function defaultService(name = ""): SpecService {
  return { name, type: "postgres", size: "S", scopes: [] };
}

/** Agrega o quita un scope manteniendo el orden dev, staging, prod, pr. */
export function toggleScope(scopes: SpecScope[], name: string, included: boolean): SpecScope[] {
  const without = scopes.filter((s) => s.name !== name);
  if (!included) return without;
  const existing = scopes.find((s) => s.name === name) ?? defaultScope(name);
  const next = [...without, existing];
  next.sort((a, b) => order(a.name) - order(b.name));
  return next;
}

function order(name: string): number {
  const i = (SCOPE_ORDER as readonly string[]).indexOf(name);
  return i === -1 ? SCOPE_ORDER.length : i;
}

export function hasPort(type: string): boolean {
  return type === "api" || type === "static";
}

/**
 * Deja el spec listo para enviar: los campos que no aplican al tipo o al
 * origen se vacían, los strings se recortan y se descartan filas incompletas
 * de nombre vacío. No muta la entrada.
 */
export function normalizeSpec(spec: ApplicationSpec): ApplicationSpec {
  const scopeNames = new Set(spec.scopes.map((s) => s.name));
  const keepScopes = (names: string[]) => names.filter((n) => scopeNames.has(n));
  return {
    ...spec,
    name: spec.name.trim(),
    team: spec.team.trim(),
    description: spec.description.trim(),
    port: hasPort(spec.type) ? spec.port : 0,
    healthcheck: spec.type === "api" ? spec.healthcheck.trim() : "",
    schedule: spec.type === "cron" ? spec.schedule.trim() : "",
    scopes: spec.scopes.map((s) => {
      // compute solo viaja si no es el default (kubernetes).
      const { compute, ...rest } = s;
      return {
        ...rest,
        ...(compute === "serverless" ? { compute } : {}),
        domain: s.name === "prod" ? s.domain.trim() : "",
        ttl: s.name === "pr" ? s.ttl.trim() : "",
        autoscaling: s.autoscaling ? { ...s.autoscaling } : null,
      };
    }),
    parameters: spec.parameters
      .filter((p) => p.name.trim() !== "")
      .map((p): SpecParameter => {
        const base = { name: p.name.trim(), source: p.source, scopes: keepScopes(p.scopes) };
        if (p.source === "value") return { ...base, value: p.value ?? "" };
        if (p.source === "service") return { ...base, service: p.service ?? "", key: (p.key ?? "").trim() };
        return base;
      }),
    services: spec.services
      .filter((s) => s.name.trim() !== "")
      .map((s) => ({ ...s, name: s.name.trim(), scopes: keepScopes(s.scopes) })),
  };
}

export type WizardStep = "basics" | "scopes" | "parameters" | "services" | "review";

/** Las etiquetas salen del catálogo: wizard.steps.<key>. */
export const WIZARD_STEPS: { key: WizardStep }[] = [
  { key: "basics" },
  { key: "scopes" },
  { key: "parameters" },
  { key: "services" },
  { key: "review" },
];

/** Asocia un mensaje de validación de la API (por ejemplo "spec.scopes.prod.size ...") al paso que lo corrige. */
export function stepForDetail(detail: string): WizardStep {
  const d = detail.toLowerCase();
  if (/parameter/.test(d)) return "parameters";
  if (/services?\b|services?\[|services?\./.test(d) && !/fromservice/.test(d)) return "services";
  if (/scope/.test(d)) return "scopes";
  return "basics";
}

export function groupDetailsByStep(details: string[]): Record<WizardStep, string[]> {
  const out: Record<WizardStep, string[]> = { basics: [], scopes: [], parameters: [], services: [], review: [] };
  for (const d of details) out[stepForDetail(d)].push(d);
  return out;
}

/** Errores locales, instantáneos, que evitan pedir un preview inútil. */
export function localErrors(spec: ApplicationSpec): Record<WizardStep, string[]> {
  const out: Record<WizardStep, string[]> = { basics: [], scopes: [], parameters: [], services: [], review: [] };
  const nameErr = validateAppName(spec.name);
  if (nameErr) out.basics.push(i18n.t("validation.fieldName", { error: nameErr }));
  if (spec.team.trim() === "") out.basics.push(i18n.t("validation.teamRequired"));
  if (spec.type === "cron" && spec.schedule.trim() === "") out.basics.push(i18n.t("validation.scheduleRequired"));
  if (spec.scopes.length === 0) out.scopes.push(i18n.t("validation.pickScope"));
  for (const p of spec.parameters) {
    if (p.name.trim() === "") continue;
    const e = validateParamName(p.name.trim());
    if (e) out.parameters.push(`${p.name}: ${e}`);
    if (p.source === "service" && (p.service ?? "") === "") out.parameters.push(i18n.t("validation.pickService", { name: p.name }));
  }
  return out;
}

export function countErrors(e: Record<WizardStep, string[]>): number {
  return Object.values(e).reduce((n, l) => n + l.length, 0);
}

/** Completa lo que el servidor pueda omitir (arrays nulos, campos vacíos) para que el formulario no rompa. */
export function hydrateSpec(s: Partial<ApplicationSpec>): ApplicationSpec {
  const d = defaultSpec();
  return {
    name: s.name ?? "",
    team: s.team ?? "",
    tier: s.tier || d.tier,
    description: s.description ?? "",
    type: s.type || d.type,
    runtime: s.runtime || d.runtime,
    port: s.port ?? 0,
    healthcheck: s.healthcheck ?? "",
    schedule: s.schedule ?? "",
    scopes: (s.scopes ?? []).map((sc) => ({
      ...defaultScope(sc.name),
      ...sc,
      domain: sc.domain ?? "",
      ttl: sc.ttl ?? "",
      autoscaling: sc.autoscaling ?? null,
    })),
    parameters: (s.parameters ?? []).map((p) => ({ ...p, scopes: p.scopes ?? [] })),
    services: (s.services ?? []).map((v) => ({ ...v, scopes: v.scopes ?? [] })),
  };
}
