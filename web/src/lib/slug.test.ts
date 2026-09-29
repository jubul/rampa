import { describe, expect, it } from "vitest";
import { SLUG_RE, slugify } from "./slug";

describe("slugify", () => {
  it("normaliza acentos, espacios y símbolos", () => {
    expect(slugify("Acme Región Sur!")).toBe("acme-region-sur");
    expect(slugify("  --Hola--  ")).toBe("hola");
  });
  it("corta a 40 caracteres", () => {
    expect(slugify("a".repeat(60))).toHaveLength(40);
  });
  it("produce slugs que la API acepta", () => {
    for (const name of ["Acme", "Bupalus Labs", "org 2026", "Ñandú S.A."]) {
      const s = slugify(name);
      expect(SLUG_RE.test(s), `${name} -> ${s}`).toBe(true);
    }
  });
});

describe("SLUG_RE", () => {
  it("coincide con la regla de docs/api.md", () => {
    expect(SLUG_RE.test("acme")).toBe(true);
    // La regla de la API admite un solo caracter, pero no dos.
    expect(SLUG_RE.test("a")).toBe(true);
    expect(SLUG_RE.test("ab")).toBe(false);
    expect(SLUG_RE.test("-acme")).toBe(false);
    expect(SLUG_RE.test("acme-")).toBe(false);
    expect(SLUG_RE.test("Acme")).toBe(false);
    expect(SLUG_RE.test("a".repeat(40))).toBe(true);
    expect(SLUG_RE.test("a".repeat(41))).toBe(false);
  });
});
