// Configuración de i18next. Idiomas: es (default) y en.
// Prioridad al elegir idioma: preferencia del usuario (GET /me locale, que se
// guarda en localStorage para no parpadear en la próxima carga) > idioma del
// navegador > es.

import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import LanguageDetector from "i18next-browser-languagedetector";
import es from "./es.json";
import en from "./en.json";

export const SUPPORTED_LOCALES = ["es", "en"] as const;
export type Locale = (typeof SUPPORTED_LOCALES)[number];
export const DEFAULT_LOCALE: Locale = "es";
export const LOCALE_STORAGE_KEY = "rampa.locale";

export function isLocale(v: unknown): v is Locale {
  return typeof v === "string" && (SUPPORTED_LOCALES as readonly string[]).includes(v);
}

/** Reduce "es-AR" o "en-US" a es / en; undefined si no está soportado. */
export function normalizeLocale(v: string | null | undefined): Locale | undefined {
  if (!v) return undefined;
  const base = v.toLowerCase().split(/[-_]/)[0];
  return isLocale(base) ? base : undefined;
}

void i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources: { es: { translation: es }, en: { translation: en } },
    fallbackLng: DEFAULT_LOCALE,
    supportedLngs: [...SUPPORTED_LOCALES],
    nonExplicitSupportedLngs: true,
    load: "languageOnly",
    interpolation: { escapeValue: false },
    detection: {
      order: ["localStorage", "navigator"],
      lookupLocalStorage: LOCALE_STORAGE_KEY,
      // No cacheamos la detección: solo guardamos lo que el usuario elige.
      caches: [],
    },
    returnNull: false,
  });

/** Idioma activo ya normalizado (es o en). */
export function currentLocale(): Locale {
  return normalizeLocale(i18n.resolvedLanguage ?? i18n.language) ?? DEFAULT_LOCALE;
}

/** Cambia el idioma al instante y lo recuerda para la próxima carga. */
export function setLocale(locale: Locale): void {
  try {
    localStorage.setItem(LOCALE_STORAGE_KEY, locale);
  } catch {
    // Sin localStorage (modo privado estricto): seguimos igual.
  }
  void i18n.changeLanguage(locale);
  if (typeof document !== "undefined") document.documentElement.lang = locale;
}

if (typeof document !== "undefined") {
  document.documentElement.lang = currentLocale();
  i18n.on("languageChanged", () => {
    document.documentElement.lang = currentLocale();
  });
}

export const t = i18n.t.bind(i18n);
export default i18n;
