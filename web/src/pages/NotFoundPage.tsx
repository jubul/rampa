import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";

export function NotFoundPage() {
  const { t } = useTranslation();
  return (
    <div className="flex min-h-full flex-col items-center justify-center gap-2 p-12 text-center">
      <p className="text-4xl font-semibold text-slate-300 dark:text-slate-600">404</p>
      <p className="text-sm text-slate-600 dark:text-slate-300">{t("notFound.message")}</p>
      <Link to="/" className="text-sm font-medium text-slate-900 underline dark:text-slate-100">
        {t("notFound.home")}
      </Link>
    </div>
  );
}
