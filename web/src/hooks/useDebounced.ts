import { useEffect, useState } from "react";

/** Devuelve `value` con retraso: cambia solo cuando pasaron `ms` sin nuevos cambios. */
export function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return v;
}
