// Utilidades de URLs provenientes de datos de usuario.

/** Devuelve la URL solo si es http(s); si no, null (se renderiza como texto plano). */
export function safeHttpUrl(raw: string | null | undefined): string | null {
  if (!raw) return null;
  const v = raw.trim();
  return /^https?:\/\//i.test(v) ? v : null;
}
