// Tema claro / oscuro / sistema. Lógica pura y helpers del DOM; el script
// inline de index.html hace lo mismo antes de que cargue React para evitar
// el flash. Si cambiás la clave o la lógica, cambiala también ahí.

export type ThemeMode = "light" | "dark" | "system";
export const THEME_MODES: readonly ThemeMode[] = ["light", "dark", "system"];
export const THEME_STORAGE_KEY = "rampa.theme";

export function isThemeMode(v: unknown): v is ThemeMode {
  return v === "light" || v === "dark" || v === "system";
}

/** ¿Hay que usar oscuro? En modo sistema manda prefers-color-scheme. */
export function resolveDark(mode: ThemeMode, systemPrefersDark: boolean): boolean {
  if (mode === "dark") return true;
  if (mode === "light") return false;
  return systemPrefersDark;
}

export function readStoredTheme(): ThemeMode {
  try {
    const v = localStorage.getItem(THEME_STORAGE_KEY);
    return isThemeMode(v) ? v : "system";
  } catch {
    return "system";
  }
}

export function storeTheme(mode: ThemeMode): void {
  try {
    localStorage.setItem(THEME_STORAGE_KEY, mode);
  } catch {
    // Sin localStorage: el tema vale solo para esta sesión.
  }
}

export function systemPrefersDark(): boolean {
  return typeof window !== "undefined" && typeof window.matchMedia === "function"
    ? window.matchMedia("(prefers-color-scheme: dark)").matches
    : false;
}

/** Aplica la clase dark en <html> y el color-scheme nativo (scrollbars, controles). */
export function applyTheme(mode: ThemeMode): boolean {
  const dark = resolveDark(mode, systemPrefersDark());
  const root = document.documentElement;
  root.classList.toggle("dark", dark);
  root.style.colorScheme = dark ? "dark" : "light";
  return dark;
}
