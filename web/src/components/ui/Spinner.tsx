import { Loader2 } from "lucide-react";
import { useTranslation } from "react-i18next";

export function Spinner({ className = "size-5" }: { className?: string }) {
  return <Loader2 className={`${className} animate-spin text-slate-500 dark:text-slate-400`} aria-hidden />;
}

export function FullPageSpinner({ label }: { label?: string }) {
  const { t } = useTranslation();
  return (
    <div className="flex h-full min-h-[40vh] flex-col items-center justify-center gap-3 text-sm text-slate-500 dark:text-slate-400" role="status">
      <Spinner className="size-6" />
      <span>{label ?? t("common.loading")}...</span>
    </div>
  );
}
