// Formateo de fechas y textos cortos. Todo en español.

const dateTimeFmt = new Intl.DateTimeFormat("es-AR", {
  day: "2-digit",
  month: "2-digit",
  year: "numeric",
  hour: "2-digit",
  minute: "2-digit",
});

const timeFmt = new Intl.DateTimeFormat("es-AR", {
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
});

const rtf = new Intl.RelativeTimeFormat("es", { numeric: "auto" });

export function parseDate(value: string | null | undefined): Date | null {
  if (!value) return null;
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? null : d;
}

export function formatDateTime(value: string | null | undefined): string {
  const d = parseDate(value);
  return d ? dateTimeFmt.format(d) : "-";
}

export function formatTime(value: string | null | undefined): string {
  const d = parseDate(value);
  return d ? timeFmt.format(d) : "-";
}

/** "hace 3 minutos", "hace 2 días". Para fechas futuras: "en 5 horas". */
export function formatRelative(value: string | null | undefined, now: number = Date.now()): string {
  const d = parseDate(value);
  if (!d) return "-";
  const diffSec = Math.round((d.getTime() - now) / 1000);
  const abs = Math.abs(diffSec);
  if (abs < 45) return rtf.format(Math.trunc(diffSec / 1), "second");
  if (abs < 3600) return rtf.format(Math.round(diffSec / 60), "minute");
  if (abs < 86400) return rtf.format(Math.round(diffSec / 3600), "hour");
  if (abs < 86400 * 30) return rtf.format(Math.round(diffSec / 86400), "day");
  return dateTimeFmt.format(d);
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

export function pluralize(n: number, singular: string, plural: string): string {
  return `${n} ${n === 1 ? singular : plural}`;
}
