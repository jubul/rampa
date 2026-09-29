import { describe, expect, it } from "vitest";
import { eventBelongsToApp, mergeEvents } from "./events";
import type { Event } from "../api/types";

function ev(partial: Partial<Event> & Pick<Event, "id">): Event {
  return {
    kind: "deployment.requested",
    actor: { type: "user", id: "u1", name: "Juan" },
    subject_type: "deployment",
    subject_id: "d1",
    message: "",
    payload: {},
    created_at: "2026-09-29T10:00:00Z",
    ...partial,
  };
}

const app = { id: "app-1", name: "checkout-api" };

describe("eventBelongsToApp", () => {
  const deployments = new Set(["d1", "d2"]);

  it("acepta eventos cuyo sujeto es la app", () => {
    expect(eventBelongsToApp(ev({ id: "e", subject_type: "application", subject_id: "app-1" }), app, deployments)).toBe(true);
    expect(eventBelongsToApp(ev({ id: "e", subject_type: "application", subject_id: "otra" }), app, deployments)).toBe(false);
  });

  it("acepta eventos de sus deployments y rechaza los de otros", () => {
    expect(eventBelongsToApp(ev({ id: "e", subject_type: "deployment", subject_id: "d2" }), app, deployments)).toBe(true);
    expect(eventBelongsToApp(ev({ id: "e", subject_type: "deployment", subject_id: "d9" }), app, deployments)).toBe(false);
  });

  it("asocia por payload cuando el sujeto es un scope (sin id en la API)", () => {
    expect(eventBelongsToApp(ev({ id: "e", subject_type: "scope", subject_id: "s1", payload: { app: "checkout-api", scope: "dev" } }), app, deployments)).toBe(true);
    expect(eventBelongsToApp(ev({ id: "e", subject_type: "scope", subject_id: "s1", payload: { application_id: "app-1" } }), app, deployments)).toBe(true);
    expect(eventBelongsToApp(ev({ id: "e", subject_type: "scope", subject_id: "s1", payload: { app: "otra" } }), app, deployments)).toBe(false);
  });

  it("no rompe si el payload viene nulo", () => {
    const e = ev({ id: "e", subject_type: "cluster", subject_id: "c1" });
    (e as { payload: unknown }).payload = null;
    expect(eventBelongsToApp(e, app, deployments)).toBe(false);
  });
});

describe("mergeEvents", () => {
  it("une sin duplicar, ordena del más nuevo al más viejo y limita", () => {
    const live = [ev({ id: "a", created_at: "2026-09-29T10:03:00Z" }), ev({ id: "b", created_at: "2026-09-29T10:02:00Z" })];
    const history = [ev({ id: "b", created_at: "2026-09-29T10:02:00Z", message: "vieja" }), ev({ id: "c", created_at: "2026-09-29T10:01:00Z" }), ev({ id: "d", created_at: "2026-09-29T10:00:00Z" })];
    const merged = mergeEvents(live, history, 3);
    expect(merged.map((e) => e.id)).toEqual(["a", "b", "c"]);
    // La versión en vivo gana sobre la histórica.
    expect(merged[1]!.message).toBe("");
  });

  it("tolera historial indefinido", () => {
    expect(mergeEvents([ev({ id: "a" })], undefined)).toHaveLength(1);
    expect(mergeEvents([], undefined)).toEqual([]);
  });
});
