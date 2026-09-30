// Terminología por proveedor. GET /providers devuelve, por cada nube, cómo
// llama a sus cosas en es y en (cuenta de AWS, Droplet, máquina virtual...).
// La web usa esos términos en todos sus textos; sin provider en contexto (o
// si el provider no trae un término) cae a los genéricos del catálogo (generic.*).

import i18n, { currentLocale, type Locale } from "../i18n";
import type { Provider, TerminologyKey } from "../api/types";

export interface Terms {
  account: string;
  cluster: string;
  vm: string;
  registry: string;
  secretStore: string;
  region: string;
}

const KEYS: [keyof Terms, TerminologyKey][] = [
  ["account", "account"],
  ["cluster", "cluster"],
  ["vm", "vm"],
  ["registry", "registry"],
  ["secretStore", "secret_store"],
  ["region", "region"],
];

/** Términos genéricos (sin provider en contexto) en el idioma activo. */
export function genericTerms(locale: Locale = currentLocale()): Terms {
  const t = i18n.getFixedT(locale);
  return {
    account: t("generic.account"),
    cluster: t("generic.cluster"),
    vm: t("generic.vm"),
    registry: t("generic.registry"),
    secretStore: t("generic.secretStore"),
    region: t("generic.region"),
  };
}

/** Términos del provider en el idioma activo; lo que falte cae al genérico. */
export function termsFor(provider: Provider | undefined | null, locale: Locale = currentLocale()): Terms {
  const out = genericTerms(locale);
  const term = provider?.terminology;
  if (!term) return out;
  for (const [field, key] of KEYS) {
    const v = term[key]?.[locale]?.trim();
    if (v) out[field] = v;
  }
  return out;
}

/** Primera letra en mayúscula, para usar un término al principio de una frase o en un título. */
export function cap(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}
