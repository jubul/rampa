// Formateo de fechas y textos cortos con Intl según el idioma activo.

import { currentLocale, type Locale } from "../i18n";

// Locale de Intl por idioma de la web.
const INTL_LOCALE: Record<Locale, string> = { es: "es-AR", en: "en-US" };

const cache = new Map<string, Intl.DateTimeFormat | Intl.RelativeTimeFormat>();

function dateTimeFmt(): Intl.DateTimeFormat {
  const loc = INTL_LOCALE[currentLocale()];
  const key = `dt:${loc}`;
  let f = cache.get(key);
  if (!f) {
    f = new Intl.DateTimeFormat(loc, { day: "2-digit", month: "2-digit", year: "numeric", hour: "2-digit", minute: "2-digit" });
    cache.set(key, f);
  }
  return f as Intl.DateTimeFormat;
}

function timeFmt(): Intl.DateTimeFormat {
  const loc = INTL_LOCALE[currentLocale()];
  const key = `t:${loc}`;
  let f = cache.get(key);
  if (!f) {
    f = new Intl.DateTimeFormat(loc, { hour: "2-digit", minute: "2-digit", second: "2-digit" });
    cache.set(key, f);
  }
  return f as Intl.DateTimeFormat;
}

function relativeFmt(): Intl.RelativeTimeFormat {
  const loc = currentLocale();
  const key = `rt:${loc}`;
  let f = cache.get(key);
  if (!f) {
    f = new Intl.RelativeTimeFormat(loc, { numeric: "auto" });
    cache.set(key, f);
  }
  return f as Intl.RelativeTimeFormat;
}

export function parseDate(value: string | null | undefined): Date | null {
  if (!value) return null;
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? null : d;
}

export function formatDateTime(value: string | null | undefined): string {
  const d = parseDate(value);
  return d ? dateTimeFmt().format(d) : "-";
}

export function formatTime(value: string | null | undefined): string {
  const d = parseDate(value);
  return d ? timeFmt().format(d) : "-";
}

/** "hace 3 minutos", "hace 2 días". Para fechas futuras: "en 5 horas". */
export function formatRelative(value: string | null | undefined, now: number = Date.now()): string {
  const d = parseDate(value);
  if (!d) return "-";
  const diffSec = Math.round((d.getTime() - now) / 1000);
  const abs = Math.abs(diffSec);
  if (abs < 45) return relativeFmt().format(Math.trunc(diffSec / 1), "second");
  if (abs < 3600) return relativeFmt().format(Math.round(diffSec / 60), "minute");
  if (abs < 86400) return relativeFmt().format(Math.round(diffSec / 3600), "hour");
  if (abs < 86400 * 30) return relativeFmt().format(Math.round(diffSec / 86400), "day");
  return dateTimeFmt().format(d);
}

/** Acorta una referencia de imagen larga para tablas: registry/.../repo:tag -> repo:tag. */
export function shortImage(image: string | null | undefined): string {
  if (!image) return "-";
  const lastSlash = image.lastIndexOf("/");
  return lastSlash >= 0 ? image.slice(lastSlash + 1) : image;
}

export function shortId(id: string | null | undefined): string {
  if (!id) return "-";
  return id.length > 8 ? id.slice(0, 8) : id;
}
