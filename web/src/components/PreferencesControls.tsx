import { Monitor, Moon, Sun } from "lucide-react";
import { useTranslation } from "react-i18next";
import { usePatchLocale } from "../api/hooks";
import { SUPPORTED_LOCALES, currentLocale, isLocale, setLocale } from "../i18n";
import { useTheme } from "../theme/ThemeProvider";
import type { ThemeMode } from "../theme/theme";
import { cn } from "../lib/cn";

const themeIcons: Record<ThemeMode, typeof Sun> = { light: Sun, dark: Moon, system: Monitor };
const themeOrder: ThemeMode[] = ["light", "dark", "system"];

/** Selector claro / oscuro / sistema. Se guarda en localStorage. */
export function ThemeSwitcher() {
  const { t } = useTranslation();
  const { mode, setMode } = useTheme();
  return (
    <div role="radiogroup" aria-label={t("theme.label")} className="inline-flex rounded-md border border-slate-300 bg-white p-0.5 dark:border-slate-700 dark:bg-slate-900">
      {themeOrder.map((m) => {
        const Icon = themeIcons[m];
        const active = mode === m;
        return (
          <button
            key={m}
            type="button"
            role="radio"
            aria-checked={active}
            aria-label={t(`theme.${m}`)}
            title={t(`theme.${m}`)}
            onClick={() => setMode(m)}
            className={cn(
              "flex size-7 items-center justify-center rounded",
              active
                ? "bg-slate-900 text-white dark:bg-slate-100 dark:text-slate-900"
                : "text-slate-600 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-800",
            )}
          >
            <Icon className="size-4" aria-hidden />
          </button>
        );
      })}
    </div>
  );
}

/**
 * Selector de idioma. Cambia al instante y, con sesión (persist), guarda la
 * preferencia con PATCH /me {"locale"}.
 */
export function LanguageSwitcher({ persist = true }: { persist?: boolean }) {
  const { t, i18n } = useTranslation();
  const patch = usePatchLocale();
  void i18n.language;
  const current = currentLocale();
  return (
    <select
      aria-label={t("language.label")}
      title={t("language.label")}
      value={current}
      disabled={patch.isPending}
      onChange={(e) => {
        const next = e.target.value;
        if (!isLocale(next)) return;
        setLocale(next);
        if (persist) patch.mutate(next);
      }}
      className="h-8 rounded-md border border-slate-300 bg-white px-2 text-xs font-medium text-slate-700 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-200"
    >
      {SUPPORTED_LOCALES.map((l) => (
        <option key={l} value={l}>
          {t(`language.names.${l}`, { lng: l })}
        </option>
      ))}
    </select>
  );
}

export function PreferencesControls({ persist = true }: { persist?: boolean }) {
  return (
    <div className="flex items-center gap-2">
      <LanguageSwitcher persist={persist} />
      <ThemeSwitcher />
    </div>
  );
}
