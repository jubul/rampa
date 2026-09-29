import { useEffect, useState } from "react";
import { Check, Copy } from "lucide-react";
import { Button } from "./Button";
import { cn } from "../../lib/cn";

interface CopyFieldProps {
  value: string;
  label?: string;
  multiline?: boolean;
  className?: string;
  /** Oculta el valor hasta que el usuario lo revela (para tokens). */
  secret?: boolean;
}

async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // Caemos al método viejo.
  }
  try {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.setAttribute("readonly", "");
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    const ok = document.execCommand("copy");
    document.body.removeChild(ta);
    return ok;
  } catch {
    return false;
  }
}

/** Valor de solo lectura con botón Copiar. Monoespaciado, pensado para comandos y tokens. */
export function CopyField({ value, label, multiline = false, className, secret = false }: CopyFieldProps) {
  const [copied, setCopied] = useState<"idle" | "ok" | "fail">("idle");
  const [revealed, setRevealed] = useState(!secret);

  useEffect(() => {
    if (copied === "idle") return;
    const t = setTimeout(() => setCopied("idle"), 2000);
    return () => clearTimeout(t);
  }, [copied]);

  const shown = revealed ? value : "•".repeat(Math.min(value.length, 40));

  return (
    <div className={cn("space-y-1", className)}>
      {label && <div className="text-xs font-medium text-slate-600">{label}</div>}
      <div className="flex items-start gap-2">
        {multiline ? (
          <pre className="min-w-0 flex-1 overflow-x-auto whitespace-pre-wrap break-all rounded-md border border-slate-200 bg-slate-50 px-3 py-2 font-mono text-xs leading-relaxed text-slate-800">
            {shown}
          </pre>
        ) : (
          <code className="min-w-0 flex-1 truncate rounded-md border border-slate-200 bg-slate-50 px-3 py-2 font-mono text-xs text-slate-800">
            {shown}
          </code>
        )}
        {secret && (
          <Button variant="secondary" size="sm" onClick={() => setRevealed((v) => !v)}>
            {revealed ? "Ocultar" : "Mostrar"}
          </Button>
        )}
        <Button
          variant="secondary"
          size="sm"
          onClick={async () => setCopied((await copyText(value)) ? "ok" : "fail")}
          icon={copied === "ok" ? <Check className="size-3.5 text-green-600" /> : <Copy className="size-3.5" />}
          aria-label="Copiar"
        >
          {copied === "ok" ? "Copiado" : copied === "fail" ? "No se pudo" : "Copiar"}
        </Button>
      </div>
    </div>
  );
}
