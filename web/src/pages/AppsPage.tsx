import { safeHttpUrl } from "../lib/url";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { Link, useParams } from "react-router-dom";
import { ExternalLink, Package, Plus, Upload } from "lucide-react";
import { useApps, useImportApp } from "../api/hooks";
import { useOrgRole } from "../auth/AuthContext";
import { PageHeader } from "../components/ui/PageHeader";
import { Card } from "../components/ui/Card";
import { Button } from "../components/ui/Button";
import { LinkButton } from "../components/ui/LinkButton";
import { Input, Textarea } from "../components/ui/Input";
import { Field } from "../components/ui/Field";
import { Badge } from "../components/ui/Badge";
import { Dialog } from "../components/ui/Dialog";
import { EmptyState, ErrorState, InlineError, LoadingState } from "../components/ui/States";
import { Table, TBody, TD, TH, THead, TR } from "../components/ui/Table";
import { formatRelative } from "../lib/format";

const MANIFEST_PLACEHOLDER = `apiVersion: rampa.dev/v1
kind: Application
metadata:
  name: checkout-api
  team: payments
  tier: 2
spec:
  type: api
  runtime: go
  port: 8080
  scopes:
    dev: { size: S, replicas: 1 }
    prod: { size: M, replicas: 3 }
`;

export function AppsPage() {
  const { t } = useTranslation();
  const { org = "" } = useParams();
  const { isDeveloper } = useOrgRole(org);
  const appsQ = useApps(org);
  const [importOpen, setImportOpen] = useState(false);

  return (
    <>
      <PageHeader
        title={t("nav.apps")}
        description={t("apps.description")}
        actions={
          <>
            <Button variant="secondary" onClick={() => setImportOpen(true)} disabled={!isDeveloper} title={isDeveloper ? undefined : t("common.needDeveloper")} icon={<Upload className="size-4" />}>
              {t("apps.import")}
            </Button>
            <LinkButton to={`/o/${org}/apps/new`} icon={<Plus className="size-4" />}>
              {t("apps.new")}
            </LinkButton>
          </>
        }
      />

      <Card>
        {appsQ.isPending ? (
          <LoadingState />
        ) : appsQ.isError ? (
          <ErrorState error={appsQ.error} onRetry={() => void appsQ.refetch()} />
        ) : appsQ.data.length === 0 ? (
          <EmptyState
            icon={<Package className="size-8" />}
            title={t("apps.emptyTitle")}
            description={t("apps.emptyDescription")}
            action={
              <div className="flex gap-2">
                <LinkButton to={`/o/${org}/apps/new`} icon={<Plus className="size-4" />}>
                  {t("apps.new")}
                </LinkButton>
                <Button variant="secondary" onClick={() => setImportOpen(true)} disabled={!isDeveloper} icon={<Upload className="size-4" />}>
                  {t("apps.import")}
                </Button>
              </div>
            }
          />
        ) : (
          <Table>
            <THead>
              <tr>
                <TH>{t("apps.colName")}</TH>
                <TH>{t("apps.colTeam")}</TH>
                <TH>Tier</TH>
                <TH>{t("apps.colType")}</TH>
                <TH>Scopes</TH>
                <TH>{t("apps.colRepo")}</TH>
                <TH>{t("apps.colUpdated")}</TH>
              </tr>
            </THead>
            <TBody>
              {appsQ.data.map((a) => (
                <TR key={a.id}>
                  <TD>
                    <Link to={`/o/${org}/apps/${a.name}`} className="font-medium text-slate-900 dark:text-slate-100 hover:underline">
                      {a.name}
                    </Link>
                    {a.description && <div className="max-w-xs truncate text-xs text-slate-500 dark:text-slate-400">{a.description}</div>}
                  </TD>
                  <TD>{a.team}</TD>
                  <TD>
                    <TierBadge tier={a.tier} />
                  </TD>
                  <TD>
                    <span className="font-mono text-xs">
                      {a.type}
                      {a.runtime ? ` · ${a.runtime}` : ""}
                    </span>
                  </TD>
                  <TD>
                    <div className="flex flex-wrap gap-1">
                      {a.scopes_declared.map((s) => (
                        <Badge key={s} tone={s === "prod" ? "blue" : "gray"}>
                          {s}
                        </Badge>
                      ))}
                    </div>
                  </TD>
                  <TD>
                    {safeHttpUrl(a.repo_url) ? (
                      <a href={safeHttpUrl(a.repo_url)!} target="_blank" rel="noreferrer noopener" className="inline-flex max-w-xs items-center gap-1 truncate text-xs text-slate-600 dark:text-slate-300 hover:text-slate-900 dark:hover:text-slate-100 hover:underline">
                        <span className="truncate">{a.repo_url.replace(/^https?:\/\//, "")}</span>
                        <ExternalLink className="size-3 shrink-0" />
                      </a>
                    ) : a.repo_url ? (
                      <span className="truncate text-xs text-slate-600 dark:text-slate-300">{a.repo_url}</span>
                    ) : (
                      <span className="text-xs text-slate-500 dark:text-slate-400">-</span>
                    )}
                  </TD>
                  <TD className="text-slate-500 dark:text-slate-400" title={a.updated_at}>
                    {formatRelative(a.updated_at)}
                  </TD>
                </TR>
              ))}
            </TBody>
          </Table>
        )}
      </Card>

      <ImportDialog org={org} open={importOpen} onClose={() => setImportOpen(false)} />
    </>
  );
}

export function TierBadge({ tier }: { tier: number }) {
  const { t } = useTranslation();
  const label = t(`tiers.${tier === 1 || tier === 2 ? tier : 3}.badge`, { tier });
  return <Badge tone={tier === 1 ? "red" : tier === 2 ? "amber" : "gray"}>{label}</Badge>;
}

function ImportDialog({ org, open, onClose }: { org: string; open: boolean; onClose: () => void }) {
  const { t } = useTranslation();
  return (
    <Dialog open={open} onClose={onClose} title={t("apps.import")} description={t("apps.importDescription")} size="lg">
      {open && <ImportForm org={org} onClose={onClose} />}
    </Dialog>
  );
}

function ImportForm({ org, onClose }: { org: string; onClose: () => void }) {
  const { t } = useTranslation();
  const importApp = useImportApp(org);
  const [repoUrl, setRepoUrl] = useState("");
  const [manifest, setManifest] = useState("");

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!manifest.trim()) return;
    importApp.mutate({ manifest, repo_url: repoUrl.trim() }, { onSuccess: onClose });
  };

  return (
    <form onSubmit={submit} className="space-y-4">
      <Field label={t("apps.repoUrl")} help={t("apps.repoUrlHelp")}>
        {(id) => <Input id={id} type="url" placeholder="https://github.com/acme/checkout-api" value={repoUrl} onChange={(e) => setRepoUrl(e.target.value)} />}
      </Field>
      <Field label="rampa.yaml" required>
        {(id) => (
          <Textarea
            id={id}
            required
            rows={18}
            spellCheck={false}
            placeholder={MANIFEST_PLACEHOLDER}
            value={manifest}
            onChange={(e) => setManifest(e.target.value)}
            className="font-mono text-xs"
          />
        )}
      </Field>
      {importApp.isError && <InlineError error={importApp.error} />}
      <div className="flex justify-end gap-2">
        <Button variant="secondary" onClick={onClose}>
          {t("common.cancel")}
        </Button>
        <Button type="submit" loading={importApp.isPending} icon={<Upload className="size-4" />}>
          {t("apps.importSubmit")}
        </Button>
      </div>
    </form>
  );
}
