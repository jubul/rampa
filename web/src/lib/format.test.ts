import { afterEach, describe, expect, it } from "vitest";
import i18n from "../i18n";
import { formatDateTime, formatRelative, parseDate, shortId, shortImage } from "./format";

describe("shortImage", () => {
  it("deja solo repo:tag de una referencia completa", () => {
    expect(shortImage("123456789012.dkr.ecr.us-east-1.amazonaws.com/rampa/checkout-api:sha-a1b2c3d")).toBe("checkout-api:sha-a1b2c3d");
  });
  it("devuelve el tag tal cual si no tiene barras", () => {
    expect(shortImage("sha-a1b2c3d")).toBe("sha-a1b2c3d");
  });
  it("devuelve guion para vacío", () => {
    expect(shortImage(null)).toBe("-");
    expect(shortImage("")).toBe("-");
  });
});

describe("formatRelative", () => {
  const now = Date.parse("2026-09-29T12:00:00Z");
  it("usa segundos, minutos, horas y días", () => {
    expect(formatRelative("2026-09-29T11:59:50Z", now)).toMatch(/segundo|ahora/);
    expect(formatRelative("2026-09-29T11:55:00Z", now)).toContain("5 minutos");
    expect(formatRelative("2026-09-29T09:00:00Z", now)).toContain("3 horas");
    expect(formatRelative("2026-09-27T12:00:00Z", now)).toMatch(/anteayer|2 días/);
  });
  it("habla en futuro para fechas por venir (TTL)", () => {
    expect(formatRelative("2026-09-29T15:00:00Z", now)).toMatch(/^dentro de 3 horas|^en 3 horas/);
  });
  it("devuelve guion para inválidos", () => {
    expect(formatRelative(null, now)).toBe("-");
    expect(formatRelative("no es fecha", now)).toBe("-");
  });
});

describe("parseDate y otros", () => {
  it("parseDate rechaza basura", () => {
    expect(parseDate("x")).toBeNull();
    expect(parseDate("2026-09-29T12:00:00Z")?.toISOString()).toBe("2026-09-29T12:00:00.000Z");
  });
  it("shortId acorta uuids", () => {
    expect(shortId("0f4a1e2b-1234-5678-9abc-def012345678")).toBe("0f4a1e2b");
    expect(shortId(null)).toBe("-");
  });
});

describe("idioma", () => {
  afterEach(async () => {
    await i18n.changeLanguage("es");
  });
  it("formatea fechas y tiempos relativos según el idioma activo", async () => {
    const now = Date.parse("2026-09-29T12:00:00Z");
    await i18n.changeLanguage("en");
    expect(formatRelative("2026-09-29T11:55:00Z", now)).toBe("5 minutes ago");
    expect(formatDateTime("2026-09-29T15:30:00Z")).toMatch(/09\/29\/2026/);
    await i18n.changeLanguage("es");
    expect(formatRelative("2026-09-29T11:55:00Z", now)).toContain("hace 5 minutos");
    expect(formatDateTime("2026-09-29T15:30:00Z")).toMatch(/29\/9\/2026|29\/09\/2026/);
  });
});
