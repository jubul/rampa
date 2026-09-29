/** Une clases ignorando falsy. Suficiente para no depender de clsx. */
export function cn(...parts: Array<string | false | null | undefined>): string {
  return parts.filter(Boolean).join(" ");
}
