import { useEffect, useState } from "react";
import { cn } from "../lib/cn";

// Marca de Rampa. Si existen /logo.svg (fondos claros) y /logo-dark.svg
// (fondos oscuros) en public/, se usan; si no, se muestra la marca de texto.
// Probamos con Image porque el servidor de la SPA responde index.html (200)
// a rutas que no existen, y eso hace que la carga de la imagen falle.

const probeCache = new Map<string, boolean>();

function useImageExists(src: string): boolean {
  const [ok, setOk] = useState<boolean>(probeCache.get(src) ?? false);
  useEffect(() => {
    const cached = probeCache.get(src);
    if (cached !== undefined) {
      setOk(cached);
      return;
    }
    let alive = true;
    const img = new Image();
    img.onload = () => {
      probeCache.set(src, true);
      if (alive) setOk(true);
    };
    img.onerror = () => {
      probeCache.set(src, false);
      if (alive) setOk(false);
    };
    img.src = src;
    return () => {
      alive = false;
    };
  }, [src]);
  return ok;
}

export function Logo({ className, size = "md" }: { className?: string; size?: "md" | "lg" }) {
  const light = useImageExists("/logo.svg");
  const dark = useImageExists("/logo-dark.svg");
  const height = size === "lg" ? "h-9" : "h-6";
  return (
    <span className={cn("inline-flex items-center", className)}>
      {light && <img src="/logo.svg" alt="Rampa" className={cn(height, "block w-auto dark:hidden")} />}
      {dark && <img src="/logo-dark.svg" alt="Rampa" className={cn(height, "hidden w-auto dark:block")} />}
      <span
        className={cn(
          "items-center gap-2 font-semibold tracking-tight text-slate-900 dark:text-slate-100",
          size === "lg" ? "text-2xl" : "text-base",
          light ? "hidden" : "inline-flex",
          dark ? "dark:hidden" : "dark:inline-flex",
        )}
      >
        <svg viewBox="0 0 32 32" className={size === "lg" ? "size-7" : "size-5"} aria-hidden>
          <path d="M7 23 L25 9 M7 23 H25" className="stroke-accent-600 dark:stroke-accent-500" strokeWidth="4" strokeLinecap="round" fill="none" />
        </svg>
        <span>
          Ramp<span className="text-accent-700 dark:text-accent-500">a</span>
        </span>
      </span>
    </span>
  );
}
