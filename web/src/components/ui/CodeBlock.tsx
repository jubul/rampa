import { useEffect, useState } from "react";
import { Check, Copy } from "lucide-react";
import { Button } from "./Button";
import { copyText } from "./CopyField";
import { cn } from "../../lib/cn";
import { useTranslation } from "react-i18next";

/** Bloque monoespaciado de solo lectura con botón Copiar. Para YAML y salidas largas. */
export function CodeBlock({ value, className, maxHeight = "28rem" }: { value: string; className?: string; maxHeight?: string }) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const t = setTimeout(() => setCopied(false), 2000);
    return () => clearTimeout(t);
  }, [copied]);

  return (
    <div className={cn("relative", className)}>
      <Button
        variant="secondary"
        size="sm"
        className="absolute right-2 top-2"
        onClick={async () => setCopied(await copyText(value))}
        icon={copied ? <Check className="size-3.5 text-green-600 dark:text-green-400" /> : <Copy className="size-3.5" />}
      >
        {copied ? t("common.copied") : t("common.copy")}
      </Button>
      <pre
        className="overflow-auto rounded-md border border-slate-200 dark:border-slate-800 bg-slate-50 dark:bg-slate-800/60 p-3 pr-24 font-mono text-xs leading-relaxed text-slate-800 dark:text-slate-100"
        style={{ maxHeight }}
        tabIndex={0}
      >
        {value}
      </pre>
    </div>
  );
}
