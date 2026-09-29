// Cliente HTTP tipado contra docs/api.md. Todas las llamadas van con
// credentials: "include" para que viaje la cookie rampa_session. En dev el
// proxy de Vite hace que /v1 sea same-origin.

import type {
  AddMemberRequest,
  ApiErrorBody,
  Application,
  BindScopeRequest,
  CloudAccount,
  Cluster,
  ClusterWithToken,
  CreateCloudAccountRequest,
  CreateClusterRequest,
  CreateDeploymentRequest,
  CreateOrgRequest,
  CreateTokenRequest,
  Deployment,
  Event,
  EventsFilter,
  ImportAppRequest,
  ListResponse,
  Me,
  Member,
  Org,
  PatchCloudAccountRequest,
  PatchOrgRequest,
  Provider,
  Scope,
  SubjectType,
  Token,
  UpdateManifestRequest,
  VersionInfo,
} from "./types";
import { EVENT_KINDS } from "./types";

export const API_BASE = "/v1";

/** Error de la API con el cuerpo {"error": {code, message, details}}. */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly details: string[];

  constructor(status: number, code: string, message: string, details: string[] = []) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.details = details;
  }

  get isUnauthorized(): boolean {
    return this.status === 401 || this.code === "unauthorized";
  }

  get isForbidden(): boolean {
    return this.status === 403 || this.code === "forbidden";
  }

  get isNotFound(): boolean {
    return this.status === 404 || this.code === "not_found";
  }

  get isConflict(): boolean {
    return this.status === 409 || this.code === "conflict";
  }

  get isInvalid(): boolean {
    return this.status === 400 || this.status === 422 || this.code === "invalid";
  }
}

export function isApiError(e: unknown): e is ApiError {
  return e instanceof ApiError;
}

/** Mensaje legible para cualquier error, con fallback en español. */
export function errorMessage(e: unknown): string {
  if (isApiError(e)) return e.message;
  if (e instanceof Error) return e.message;
  return "Ocurrió un error inesperado";
}

function isErrorBody(x: unknown): x is ApiErrorBody {
  if (typeof x !== "object" || x === null) return false;
  const err = (x as { error?: unknown }).error;
  return typeof err === "object" && err !== null && typeof (err as { message?: unknown }).message === "string";
}

interface RequestOptions {
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  body?: unknown;
  query?: Record<string, string | number | undefined>;
  signal?: AbortSignal;
  timeoutMs?: number;
}

function buildQuery(query?: Record<string, string | number | undefined>): string {
  if (!query) return "";
  const params = new URLSearchParams();
  for (const [k, v] of Object.entries(query)) {
    if (v === undefined || v === "") continue;
    params.set(k, String(v));
  }
  const s = params.toString();
  return s ? `?${s}` : "";
}

const DEFAULT_TIMEOUT_MS = 30_000;

async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), opts.timeoutMs ?? DEFAULT_TIMEOUT_MS);
  if (opts.signal) {
    if (opts.signal.aborted) controller.abort();
    else opts.signal.addEventListener("abort", () => controller.abort(), { once: true });
  }

  const headers: Record<string, string> = { Accept: "application/json" };
  let body: string | undefined;
  if (opts.body !== undefined) {
    headers["Content-Type"] = "application/json";
    body = JSON.stringify(opts.body);
  }

  let res: Response;
  try {
    res = await fetch(`${API_BASE}${path}${buildQuery(opts.query)}`, {
      method: opts.method ?? "GET",
      headers,
      body,
      credentials: "include",
      signal: controller.signal,
    });
  } catch (e) {
    clearTimeout(timeout);
    if (controller.signal.aborted && !opts.signal?.aborted) {
      throw new ApiError(0, "timeout", "La API no respondió a tiempo");
    }
    throw new ApiError(0, "network", "No se pudo conectar con la API");
  }
  clearTimeout(timeout);

  const text = await res.text();
  let data: unknown = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = null;
    }
  }

  if (!res.ok) {
    if (isErrorBody(data)) {
      throw new ApiError(res.status, data.error.code, data.error.message, data.error.details ?? []);
    }
    throw new ApiError(res.status, codeForStatus(res.status), text || `Error HTTP ${res.status}`);
  }
  return data as T;
}

function codeForStatus(status: number): string {
  switch (status) {
    case 401:
      return "unauthorized";
    case 403:
      return "forbidden";
    case 404:
      return "not_found";
    case 409:
      return "conflict";
    case 400:
    case 422:
      return "invalid";
    default:
      return "internal";
  }
}

const enc = encodeURIComponent;

// Auth

export const auth = {
  me: (signal?: AbortSignal) => request<Me>("/me", { signal }),
  devLogin: (email: string) => request<Me>("/auth/dev-login", { method: "POST", body: { email } }),
  logout: () => request<void>("/auth/logout", { method: "POST" }),
  /** URL a la que hay que navegar (no fetch) para arrancar el login OIDC. */
  oidcStartUrl: `${API_BASE}/auth/oidc/start`,
};

// Organizaciones

export const orgs = {
  list: (signal?: AbortSignal) => request<ListResponse<Org>>("/orgs", { signal }),
  create: (body: CreateOrgRequest) => request<Org>("/orgs", { method: "POST", body }),
  get: (org: string, signal?: AbortSignal) => request<Org>(`/orgs/${enc(org)}`, { signal }),
  patch: (org: string, body: PatchOrgRequest) => request<Org>(`/orgs/${enc(org)}`, { method: "PATCH", body }),
};

export const members = {
  list: (org: string, signal?: AbortSignal) => request<ListResponse<Member>>(`/orgs/${enc(org)}/members`, { signal }),
  add: (org: string, body: AddMemberRequest) => request<Member>(`/orgs/${enc(org)}/members`, { method: "POST", body }),
  remove: (org: string, userId: string) =>
    request<void>(`/orgs/${enc(org)}/members/${enc(userId)}`, { method: "DELETE" }),
};

export const tokens = {
  list: (org: string, signal?: AbortSignal) => request<ListResponse<Token>>(`/orgs/${enc(org)}/tokens`, { signal }),
  create: (org: string, body: CreateTokenRequest) => request<Token>(`/orgs/${enc(org)}/tokens`, { method: "POST", body }),
  remove: (org: string, id: string) => request<void>(`/orgs/${enc(org)}/tokens/${enc(id)}`, { method: "DELETE" }),
};

// Providers y cuentas de nube

export const providers = {
  list: (org: string, signal?: AbortSignal) =>
    request<ListResponse<Provider>>(`/orgs/${enc(org)}/providers`, { signal }),
};

export const cloudAccounts = {
  list: (org: string, signal?: AbortSignal) =>
    request<ListResponse<CloudAccount>>(`/orgs/${enc(org)}/cloud-accounts`, { signal }),
  get: (org: string, id: string, signal?: AbortSignal) =>
    request<CloudAccount>(`/orgs/${enc(org)}/cloud-accounts/${enc(id)}`, { signal }),
  create: (org: string, body: CreateCloudAccountRequest) =>
    request<CloudAccount>(`/orgs/${enc(org)}/cloud-accounts`, { method: "POST", body }),
  patch: (org: string, id: string, body: PatchCloudAccountRequest) =>
    request<CloudAccount>(`/orgs/${enc(org)}/cloud-accounts/${enc(id)}`, { method: "PATCH", body }),
  verify: (org: string, id: string) =>
    request<CloudAccount>(`/orgs/${enc(org)}/cloud-accounts/${enc(id)}/verify`, {
      method: "POST",
      timeoutMs: 60_000,
    }),
  remove: (org: string, id: string) =>
    request<void>(`/orgs/${enc(org)}/cloud-accounts/${enc(id)}`, { method: "DELETE" }),
};

// Clusters

export const clusters = {
  list: (org: string, signal?: AbortSignal) => request<ListResponse<Cluster>>(`/orgs/${enc(org)}/clusters`, { signal }),
  get: (org: string, id: string, signal?: AbortSignal) =>
    request<Cluster>(`/orgs/${enc(org)}/clusters/${enc(id)}`, { signal }),
  create: (org: string, body: CreateClusterRequest) =>
    request<ClusterWithToken>(`/orgs/${enc(org)}/clusters`, { method: "POST", body }),
  rotateToken: (org: string, id: string) =>
    request<ClusterWithToken>(`/orgs/${enc(org)}/clusters/${enc(id)}/rotate-token`, { method: "POST" }),
  remove: (org: string, id: string) => request<void>(`/orgs/${enc(org)}/clusters/${enc(id)}`, { method: "DELETE" }),
};

// Applications

export const apps = {
  list: (org: string, signal?: AbortSignal) =>
    request<ListResponse<Application>>(`/orgs/${enc(org)}/apps`, { signal }),
  get: (org: string, app: string, signal?: AbortSignal) =>
    request<Application>(`/orgs/${enc(org)}/apps/${enc(app)}`, { signal }),
  import: (org: string, body: ImportAppRequest) => request<Application>(`/orgs/${enc(org)}/apps`, { method: "POST", body }),
  updateManifest: (org: string, app: string, body: UpdateManifestRequest) =>
    request<Application>(`/orgs/${enc(org)}/apps/${enc(app)}/manifest`, { method: "PUT", body }),
  remove: (org: string, app: string) => request<void>(`/orgs/${enc(org)}/apps/${enc(app)}`, { method: "DELETE" }),
};

// Scopes

export const scopes = {
  list: (org: string, app: string, signal?: AbortSignal) =>
    request<ListResponse<Scope>>(`/orgs/${enc(org)}/apps/${enc(app)}/scopes`, { signal }),
  bind: (org: string, app: string, scope: string, body: BindScopeRequest) =>
    request<Scope>(`/orgs/${enc(org)}/apps/${enc(app)}/scopes/${enc(scope)}`, { method: "PUT", body }),
  remove: (org: string, app: string, scope: string) =>
    request<void>(`/orgs/${enc(org)}/apps/${enc(app)}/scopes/${enc(scope)}`, { method: "DELETE" }),
};

// Deployments

export const deployments = {
  list: (org: string, app: string, scope: string, limit = 20, signal?: AbortSignal) =>
    request<ListResponse<Deployment>>(`/orgs/${enc(org)}/apps/${enc(app)}/scopes/${enc(scope)}/deployments`, {
      query: { limit },
      signal,
    }),
  create: (org: string, app: string, scope: string, body: CreateDeploymentRequest) =>
    request<Deployment>(`/orgs/${enc(org)}/apps/${enc(app)}/scopes/${enc(scope)}/deployments`, {
      method: "POST",
      body,
    }),
  get: (org: string, id: string, signal?: AbortSignal) =>
    request<Deployment>(`/orgs/${enc(org)}/deployments/${enc(id)}`, { signal }),
  approve: (org: string, id: string) =>
    request<Deployment>(`/orgs/${enc(org)}/deployments/${enc(id)}/approve`, { method: "POST" }),
  cancel: (org: string, id: string) =>
    request<Deployment>(`/orgs/${enc(org)}/deployments/${enc(id)}/cancel`, { method: "POST" }),
};

// Eventos

export const events = {
  list: (org: string, filter: EventsFilter = {}, signal?: AbortSignal) =>
    request<ListResponse<Event>>(`/orgs/${enc(org)}/events`, {
      query: {
        subject_type: filter.subject_type,
        subject_id: filter.subject_id,
        limit: filter.limit,
        before: filter.before,
      },
      signal,
    }),
};

export const meta = {
  version: (signal?: AbortSignal) =>
    // /version está fuera de /v1.
    fetch("/version", { credentials: "include", signal }).then(async (r) => {
      if (!r.ok) throw new ApiError(r.status, codeForStatus(r.status), `Error HTTP ${r.status}`);
      return (await r.json()) as VersionInfo;
    }),
};

export interface StreamFilter {
  subject_type?: SubjectType;
  subject_id?: string;
}

export interface StreamHandlers {
  onEvent: (event: Event) => void;
  onOpen?: () => void;
  onError?: (e: globalThis.Event) => void;
}

/** Backoff de reapertura del stream cuando el EventSource queda CLOSED. */
export const EVENTS_RETRY_MIN_MS = 1000;
export const EVENTS_RETRY_MAX_MS = 30000;

/**
 * Abre el stream SSE de eventos de la organización y devuelve una función
 * de limpieza. El servidor manda `event: <kind>` y `data: <Event JSON>`, así
 * que registramos un listener por cada kind conocido (EventSource no entrega
 * eventos con nombre al handler genérico) más `message` por si viene sin nombre.
 * EventSource reconecta solo si la conexión se cae, pero ante una respuesta
 * HTTP distinta de 200 (502, 401) queda CLOSED para siempre: en ese caso
 * reabrimos con backoff exponencial (1s a 30s) hasta que se llame a la limpieza.
 */
export function openEvents(orgSlug: string, filter: StreamFilter, handlers: StreamHandlers): () => void {
  const url = `${API_BASE}/orgs/${enc(orgSlug)}/events/stream${buildQuery({
    subject_type: filter.subject_type,
    subject_id: filter.subject_id,
  })}`;

  const handle = (raw: MessageEvent) => {
    if (typeof raw.data !== "string" || raw.data === "") return;
    let parsed: unknown;
    try {
      parsed = JSON.parse(raw.data);
    } catch {
      return;
    }
    if (typeof parsed !== "object" || parsed === null) return;
    const ev = parsed as Event;
    if (typeof ev.id !== "string" || typeof ev.kind !== "string") return;
    handlers.onEvent(ev);
  };

  const listener = handle as EventListener;
  const kinds = new Set<string>(EVENT_KINDS);
  const CLOSED = 2; // EventSource.CLOSED
  let source: EventSource | null = null;
  let stopped = false;
  let delay = EVENTS_RETRY_MIN_MS;
  let timer: ReturnType<typeof setTimeout> | undefined;

  const onOpen = (e: globalThis.Event) => {
    delay = EVENTS_RETRY_MIN_MS;
    handlers.onOpen?.();
    void e;
  };

  const onError = (e: globalThis.Event) => {
    handlers.onError?.(e);
    const current = source;
    if (stopped || !current || current.readyState !== CLOSED) return;
    detach(current);
    source = null;
    timer = setTimeout(connect, delay);
    delay = Math.min(delay * 2, EVENTS_RETRY_MAX_MS);
  };

  function detach(es: EventSource) {
    for (const kind of kinds) es.removeEventListener(kind, listener);
    es.removeEventListener("message", listener);
    es.removeEventListener("open", onOpen);
    es.removeEventListener("error", onError);
    es.close();
  }

  function connect() {
    if (stopped) return;
    const es = new EventSource(url, { withCredentials: true });
    source = es;
    for (const kind of kinds) es.addEventListener(kind, listener);
    es.addEventListener("message", listener);
    es.addEventListener("open", onOpen);
    es.addEventListener("error", onError);
  }

  connect();

  return () => {
    stopped = true;
    if (timer !== undefined) clearTimeout(timer);
    if (source) detach(source);
    source = null;
  };
}
