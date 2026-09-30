import { describe, expect, it } from "vitest";
import { isThemeMode, resolveDark } from "./theme";

describe("resolveDark", () => {
  it("claro y oscuro son fijos", () => {
    expect(resolveDark("light", true)).toBe(false);
    expect(resolveDark("dark", false)).toBe(true);
  });
  it("sistema sigue prefers-color-scheme", () => {
    expect(resolveDark("system", true)).toBe(true);
    expect(resolveDark("system", false)).toBe(false);
  });
  it("valida los modos guardados", () => {
    expect(isThemeMode("dark")).toBe(true);
    expect(isThemeMode("sepia")).toBe(false);
    expect(isThemeMode(null)).toBe(false);
  });
});
