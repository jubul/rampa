import type { ReactNode } from "react";
import { AlertTriangle, Inbox } from "lucide-react";
import { Button } from "./Button";
import { Spinner } from "./Spinner";
import { errorMessage, isApiError } from "../../api/client";
import { useTranslation } from "react-i18next";

export function LoadingState({ label }: { label?: string }) {
  const { t } = useTranslation();
  return (
    <div className="flex items-center justify-center gap-2 px-5 py-10 text-sm text-slate-500 dark:text-slate-400" role="status">
      <Spinner />
      <span>{label ?? t("common.loading")}...</span>
    </div>
  );
}

export function EmptyState({
  title,
  description,
  action,
  icon,
}: {
  title: string;
  description?: ReactNode;
  action?: ReactNode;
  icon?: ReactNode;
}) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 px-5 py-12 text-center">
      <div className="text-slate-300 dark:text-slate-600">{icon ?? <Inbox className="size-8" />}</div>
      <p className="text-sm font-medium text-slate-700 dark:text-slate-200">{title}</p>
      {description && <p className="max-w-sm text-xs text-slate-500 dark:text-slate-400">{description}</p>}
      {action && <div className="mt-2">{action}</div>}
    </div>
  );
}

export function ErrorState({
  title,
  error,
  onRetry,
}: {
  title?: string;
  error: unknown;
  onRetry?: () => void;
}) {
  const { t } = useTranslation();
  const details = isApiError(error) ? error.details : [];
  return (
    <div className="flex flex-col items-center justify-center gap-2 px-5 py-10 text-center" role="alert">
      <AlertTriangle className="size-7 text-red-500 dark:text-red-400" />
      <p className="text-sm font-medium text-slate-800 dark:text-slate-100">{title ?? t("common.somethingWrong")}</p>
      <p className="max-w-md text-xs text-slate-500 dark:text-slate-400">{errorMessage(error)}</p>
      {details.length > 0 && (
        <ul className="mt-1 max-w-md list-inside list-disc text-left text-xs text-red-700 dark:text-red-300">
          {details.map((d, i) => (
            <li key={i}>{d}</li>
          ))}
        </ul>
      )}
      {onRetry && (
        <Button variant="secondary" size="sm" onClick={onRetry} className="mt-2">
          {t("common.retry")}
        </Button>
      )}
    </div>
  );
}

/** Mensaje de error inline para formularios, con details de validación si hay. */
export function InlineError({ error }: { error: unknown }) {
  if (!error) return null;
  const details = isApiError(error) ? error.details : [];
  return (
    <div className="rounded-md border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-3 py-2 text-sm text-red-700 dark:text-red-300" role="alert">
      <p>{errorMessage(error)}</p>
      {details.length > 0 && (
        <ul className="mt-1.5 list-inside list-disc space-y-0.5 font-mono text-xs">
          {details.map((d, i) => (
            <li key={i}>{d}</li>
          ))}
        </ul>
      )}
    </div>
  );
}

/** Aviso destacado (por ejemplo "este token se muestra una sola vez"). */
export function Notice({ tone = "amber", children }: { tone?: "amber" | "blue" | "green"; children: ReactNode }) {
  const cls = {
    amber: "border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 text-amber-900 dark:text-amber-200",
    blue: "border-blue-200 dark:border-blue-900 bg-blue-50 dark:bg-blue-950/40 text-blue-900 dark:text-blue-200",
    green: "border-green-200 dark:border-green-900 bg-green-50 dark:bg-green-950/40 text-green-900 dark:text-green-200",
  }[tone];
  return <div className={`rounded-md border px-3 py-2 text-sm ${cls}`}>{children}</div>;
}
