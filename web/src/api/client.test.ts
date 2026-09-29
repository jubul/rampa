import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError, apps, auth, cloudAccounts, deployments, errorMessage, events, isApiError, openEvents } from "./client";
import { EVENT_KINDS } from "./types";

type FetchMock = ReturnType<typeof vi.fn<typeof fetch>>;

function jsonResponse(status: number, body: unknown): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("request", () => {
  let fetchMock: FetchMock;

  beforeEach(() => {
    fetchMock = vi.fn<typeof fetch>();
    vi.stubGlobal("fetch", fetchMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("manda credentials include y Accept json", async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { user: { id: "u1", email: "a@b.c", name: "A" }, memberships: [] }));
    const me = await auth.me();
    expect(me.user.id).toBe("u1");
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe("/v1/me");
    expect(init?.credentials).toBe("include");
    expect((init?.headers as Record<string, string>).Accept).toBe("application/json");
    expect(init?.body).toBeUndefined();
  });

  it("serializa el body como JSON con Content-Type", async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { id: "d1" }));
    await deployments.create("acme", "checkout-api", "dev", { image: "sha-abc" });
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe("/v1/orgs/acme/apps/checkout-api/scopes/dev/deployments");
    expect(init?.method).toBe("POST");
    expect((init?.headers as Record<string, string>)["Content-Type"]).toBe("application/json");
    expect(JSON.parse(String(init?.body))).toEqual({ image: "sha-abc" });
  });

  it("arma el query string omitiendo vacíos", async () => {
    fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(200, { items: [] })));
    await events.list("acme", { subject_type: "deployment", subject_id: "d1", limit: 50 });
    expect(fetchMock.mock.calls[0]![0]).toBe("/v1/orgs/acme/events?subject_type=deployment&subject_id=d1&limit=50");

    await deployments.list("acme", "app", "prod");
    expect(fetchMock.mock.calls[1]![0]).toBe("/v1/orgs/acme/apps/app/scopes/prod/deployments?limit=20");
  });

  it("escapa segmentos de ruta", async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { id: "x" }));
    await apps.get("acme", "raro/nombre");
    expect(fetchMock.mock.calls[0]![0]).toBe("/v1/orgs/acme/apps/raro%2Fnombre");
  });

  it("convierte {error:{code,message,details}} en ApiError", async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(400, {
        error: { code: "invalid", message: "rampa.yaml inválido", details: ["spec.type \"x\" inválido", "metadata.tier debe ser 1, 2 o 3"] },
      }),
    );
    const err = await apps.import("acme", { manifest: "kind: X", repo_url: "" }).catch((e: unknown) => e);
    expect(isApiError(err)).toBe(true);
    const api = err as ApiError;
    expect(api.status).toBe(400);
    expect(api.code).toBe("invalid");
    expect(api.isInvalid).toBe(true);
    expect(api.message).toBe("rampa.yaml inválido");
    expect(api.details).toHaveLength(2);
    expect(errorMessage(api)).toBe("rampa.yaml inválido");
  });

  it("mapea 401, 403, 404 y 409 aunque el cuerpo no sea JSON", async () => {
    for (const [status, code, flag] of [
      [401, "unauthorized", "isUnauthorized"],
      [403, "forbidden", "isForbidden"],
      [404, "not_found", "isNotFound"],
      [409, "conflict", "isConflict"],
    ] as const) {
      fetchMock.mockResolvedValueOnce(new Response("nope", { status }));
      const err = (await auth.me().catch((e: unknown) => e)) as ApiError;
      expect(err.status).toBe(status);
      expect(err.code).toBe(code);
      expect(err[flag]).toBe(true);
      expect(err.details).toEqual([]);
    }
  });

  it("acepta 204 sin cuerpo", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await expect(cloudAccounts.remove("acme", "c1")).resolves.toBeNull();
    expect(fetchMock.mock.calls[0]![1]?.method).toBe("DELETE");
  });

  it("PATCH de cuenta manda external_ref y settings", async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { id: "c1" }));
    await cloudAccounts.patch("acme", "c1", { external_ref: "arn:aws:iam::1:role/R", settings: { external_id: "x" } });
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe("/v1/orgs/acme/cloud-accounts/c1");
    expect(init?.method).toBe("PATCH");
    expect(JSON.parse(String(init?.body))).toEqual({ external_ref: "arn:aws:iam::1:role/R", settings: { external_id: "x" } });
  });

  it("errores de red se convierten en ApiError con code network", async () => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));
    const err = (await auth.me().catch((e: unknown) => e)) as ApiError;
    expect(isApiError(err)).toBe(true);
    expect(err.code).toBe("network");
    expect(err.status).toBe(0);
  });

  it("errorMessage tiene fallback para cosas que no son Error", () => {
    expect(errorMessage("x")).toBe("Ocurrió un error inesperado");
    expect(errorMessage(new Error("boom"))).toBe("boom");
  });
});

// EventSource falso: guarda los listeners y permite emitir.
class FakeEventSource {
  static instances: FakeEventSource[] = [];
  readonly url: string;
  readonly withCredentials: boolean;
  closed = false;
  readyState = 1;
  private listeners = new Map<string, Set<EventListener>>();

  constructor(url: string, init?: EventSourceInit) {
    this.url = url;
    this.withCredentials = Boolean(init?.withCredentials);
    FakeEventSource.instances.push(this);
  }

  addEventListener(type: string, cb: EventListener) {
    if (!this.listeners.has(type)) this.listeners.set(type, new Set());
    this.listeners.get(type)!.add(cb);
  }

  removeEventListener(type: string, cb: EventListener) {
    this.listeners.get(type)?.delete(cb);
  }

  close() {
    this.closed = true;
    this.readyState = 2;
  }

  /** Simula un error; con closed=true el navegador dejó el stream CLOSED (respuesta no-200). */
  fail(closed: boolean) {
    this.readyState = closed ? 2 : 0;
    const ev = { type: "error" } as unknown as globalThis.Event;
    for (const cb of this.listeners.get("error") ?? []) cb(ev);
  }

  emit(type: string, data: string) {
    const ev = { data, type } as unknown as globalThis.Event;
    for (const cb of this.listeners.get(type) ?? []) cb(ev);
  }

  listenerCount(): number {
    let n = 0;
    for (const s of this.listeners.values()) n += s.size;
    return n;
  }
}

describe("openEvents", () => {
  beforeEach(() => {
    FakeEventSource.instances = [];
    vi.stubGlobal("EventSource", FakeEventSource);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("abre el stream con filtros y credenciales y escucha todos los kinds", () => {
    const onEvent = vi.fn();
    const close = openEvents("acme", { subject_type: "deployment", subject_id: "d1" }, { onEvent });
    const es = FakeEventSource.instances[0]!;
    expect(es.url).toBe("/v1/orgs/acme/events/stream?subject_type=deployment&subject_id=d1");
    expect(es.withCredentials).toBe(true);
    // Un listener por kind más "message", "open" y "error" (necesarios para reabrir).
    expect(es.listenerCount()).toBe(EVENT_KINDS.length + 3);
    close();
    expect(es.closed).toBe(true);
    expect(es.listenerCount()).toBe(0);
  });

  it("parsea el data de eventos con nombre y descarta basura", () => {
    const onEvent = vi.fn();
    openEvents("acme", {}, { onEvent });
    const es = FakeEventSource.instances[0]!;
    expect(es.url).toBe("/v1/orgs/acme/events/stream");

    const payload = {
      id: "e1",
      kind: "deployment.healthy",
      actor: { type: "agent", id: "c1", name: "eks" },
      subject_type: "deployment",
      subject_id: "d1",
      message: "ok",
      payload: {},
      created_at: "2026-09-29T12:00:00Z",
    };
    es.emit("deployment.healthy", JSON.stringify(payload));
    es.emit("message", JSON.stringify({ ...payload, id: "e2" }));
    es.emit("deployment.failed", "esto no es json");
    es.emit("cluster.online", JSON.stringify({ sin: "id" }));
    es.emit("cluster.online", "");

    expect(onEvent).toHaveBeenCalledTimes(2);
    expect(onEvent.mock.calls[0]![0]).toMatchObject({ id: "e1", kind: "deployment.healthy" });
    expect(onEvent.mock.calls[1]![0]).toMatchObject({ id: "e2" });
  });
});

describe("openEvents reconexión", () => {
  beforeEach(() => {
    FakeEventSource.instances = [];
    vi.useFakeTimers();
    vi.stubGlobal("EventSource", FakeEventSource);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("reabre con backoff exponencial tope 30s cuando queda CLOSED", () => {
    const onError = vi.fn();
    openEvents("acme", {}, { onEvent: vi.fn(), onError });
    const expected = [1000, 2000, 4000, 8000, 16000, 30000, 30000];
    expected.forEach((ms, i) => {
      const n = FakeEventSource.instances.length;
      FakeEventSource.instances[n - 1]!.fail(true);
      vi.advanceTimersByTime(ms - 1);
      expect(FakeEventSource.instances.length).toBe(n);
      vi.advanceTimersByTime(1);
      expect(FakeEventSource.instances.length).toBe(n + 1);
      expect(FakeEventSource.instances[i]!.listenerCount()).toBe(0);
    });
    expect(onError).toHaveBeenCalledTimes(expected.length);
  });

  it("no reabre si EventSource sigue reconectando solo", () => {
    openEvents("acme", {}, { onEvent: vi.fn() });
    FakeEventSource.instances[0]!.fail(false);
    vi.advanceTimersByTime(60000);
    expect(FakeEventSource.instances.length).toBe(1);
  });

  it("open reinicia el backoff y el cleanup cancela reaperturas pendientes", () => {
    const onOpen = vi.fn();
    const close = openEvents("acme", {}, { onEvent: vi.fn(), onOpen });
    FakeEventSource.instances[0]!.fail(true);
    vi.advanceTimersByTime(1000);
    FakeEventSource.instances[1]!.fail(true);
    vi.advanceTimersByTime(2000);
    FakeEventSource.instances[2]!.emit("open", "");
    expect(onOpen).toHaveBeenCalledTimes(1);
    FakeEventSource.instances[2]!.fail(true);
    vi.advanceTimersByTime(1000);
    expect(FakeEventSource.instances.length).toBe(4);
    FakeEventSource.instances[3]!.fail(true);
    close();
    vi.advanceTimersByTime(60000);
    expect(FakeEventSource.instances.length).toBe(4);
  });
});
