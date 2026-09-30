import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate } from "react-router-dom";
import { useAuth } from "../auth/AuthContext";
import { useCreateOrg } from "../api/hooks";
import { Button } from "../components/ui/Button";
import { Input } from "../components/ui/Input";
import { Field } from "../components/ui/Field";
import { InlineError } from "../components/ui/States";
import { SLUG_RE, slugify } from "../lib/slug";
import { PreferencesControls } from "../components/PreferencesControls";

export function NewOrgPage() {
  const { t } = useTranslation();
  const me = useAuth();
  const navigate = useNavigate();
  const createOrg = useCreateOrg();
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [slugTouched, setSlugTouched] = useState(false);

  const slugError = slug !== "" && !SLUG_RE.test(slug) ? t("newOrg.slugError") : undefined;

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (slugError || !slug || !name.trim()) return;
    createOrg.mutate({ slug, name: name.trim() }, { onSuccess: (org) => navigate(`/o/${org.slug}`, { replace: true }) });
  };

  return (
    <div className="relative flex min-h-full items-center justify-center bg-slate-50 dark:bg-slate-950 px-4 py-12">
      <div className="absolute right-4 top-4">
        <PreferencesControls />
      </div>
      <div className="w-full max-w-md">
        <h1 className="text-xl font-semibold tracking-tight">{t("newOrg.title")}</h1>
        <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
          {me.memberships.length === 0
            ? t("newOrg.noOrgs")
            : t("newOrg.willBeOwner")}
        </p>
        <form onSubmit={submit} className="mt-6 space-y-4 rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 p-6 shadow-sm">
          <Field label={t("newOrg.name")} required>
            {(id) => (
              <Input
                id={id}
                required
                placeholder="Acme"
                value={name}
                onChange={(e) => {
                  setName(e.target.value);
                  if (!slugTouched) setSlug(slugify(e.target.value));
                }}
              />
            )}
          </Field>
          <Field label={t("newOrg.slug")} required help={t("newOrg.slugHelp")} error={slugError}>
            {(id) => (
              <Input
                id={id}
                required
                placeholder="acme"
                value={slug}
                aria-invalid={Boolean(slugError)}
                onChange={(e) => {
                  setSlugTouched(true);
                  setSlug(e.target.value.toLowerCase());
                }}
              />
            )}
          </Field>
          {createOrg.isError && <InlineError error={createOrg.error} />}
          <div className="flex items-center justify-between gap-2 pt-1">
            {me.memberships.length > 0 ? (
              <Link to="/" className="text-sm text-slate-600 dark:text-slate-300 hover:underline">
                {t("common.back")}
              </Link>
            ) : (
              <span />
            )}
            <Button type="submit" loading={createOrg.isPending}>
              {t("newOrg.create")}
            </Button>
          </div>
        </form>
      </div>
    </div>
  );
}
