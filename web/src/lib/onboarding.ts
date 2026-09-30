import type { TFunction } from "i18next";

/** Título del paso de onboarding por clave (catálogo), con el de la API como respaldo. */
export function stepTitle(t: TFunction, key: string | undefined, apiTitle?: string): string | undefined {
  if (!key) return undefined;
  return t(`onboarding.steps.${key}`, { defaultValue: apiTitle ?? key });
}
