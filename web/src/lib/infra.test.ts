import { describe, expect, it } from "vitest";
import {
  buildClusterRequest,
  costHint,
  initialInputValues,
  initialLogState,
  inputKind,
  inputsToPayload,
  logReducer,
  parseList,
  parseLogEvent,
  sizeInputName,
  splitModules,
  toneForRun,
  validateClusterForm,
  visibleInputs,
} from "./infra";
import type { InfraModuleInput } from "../api/types";

describe("parseLogEvent", () => {
  it("interpreta line, status y done", () => {
    expect(parseLogEvent("line", "Initializing...")).toEqual({ type: "line", text: "Initializing..." });
    expect(parseLogEvent("line", "")).toEqual({ type: "line", text: "" });
    expect(parseLogEvent("status", " planning ")).toEqual({ type: "status", status: "planning" });
    expect(parseLogEvent("done", "")).toEqual({ type: "done" });
  });
  it("ignora lo desconocido", () => {
    expect(parseLogEvent("ping", "x")).toBeNull();
    expect(parseLogEvent("status", "")).toBeNull();
    expect(parseLogEvent("line", undefined)).toEqual({ type: "line", text: "" });
  });
});

describe("logReducer", () => {
  it("agrega líneas, guarda estado y marca done", () => {
    let s = initialLogState(["a"], "queued");
    s = logReducer(s, { type: "line", text: "b" });
    s = logReducer(s, { type: "status", status: "applying" });
    expect(s).toEqual({ lines: ["a", "b"], status: "applying", done: false });
    s = logReducer(s, { type: "done" });
    expect(s.done).toBe(true);
  });
  it("reset descarta el buffer al reconectar (el servidor reenvía todo)", () => {
    const s = logReducer(initialLogState(["a", "b"], "applying"), { type: "reset" });
    expect(s.lines).toEqual([]);
    expect(s.status).toBe("applying");
  });
  it("acota el buffer", () => {
    let s = initialLogState();
    for (let i = 0; i < 5100; i++) s = logReducer(s, { type: "line", text: String(i) });
    expect(s.lines.length).toBe(5000);
    expect(s.lines.at(-1)).toBe("5099");
  });
});

const doInputs: InfraModuleInput[] = [
  { name: "region", default: "nyc3", options: ["nyc3", "ams3"] },
  { name: "size", default: "s-1vcpu-2gb", options: ["s-1vcpu-1gb", "s-1vcpu-2gb"], description: "s-1vcpu-1gb: ~6 USD/mes\ns-1vcpu-2gb: ~12 USD/mes" },
  { name: "domain", required: true },
  { name: "ssh_allowed_cidrs", type: "list" },
  { name: "install_cert_manager", type: "bool", default: false },
  { name: "disk_gb", type: "number", default: 25 },
  { name: "api_token", secret: true },
  { name: "bootstrap_token" },
  { name: "name" },
];

describe("inputs de módulo a formulario", () => {
  it("clasifica y oculta secretos y campos fijos", () => {
    expect(visibleInputs(doInputs).map((i) => i.name)).toEqual(["region", "size", "domain", "ssh_allowed_cidrs", "install_cert_manager", "disk_gb"]);
    expect(inputKind(doInputs[0]!)).toBe("select");
    expect(inputKind(doInputs[3]!)).toBe("list");
    expect(inputKind(doInputs[4]!)).toBe("bool");
    expect(inputKind(doInputs[5]!)).toBe("number");
    expect(inputKind({ name: "admin_principal_arns" })).toBe("list");
    expect(inputKind({ name: "domain" })).toBe("text");
  });
  it("toma defaults y la primera opción sin default", () => {
    const v = initialInputValues(doInputs);
    expect(v.region).toBe("nyc3");
    expect(v.size).toBe("s-1vcpu-2gb");
    expect(v.install_cert_manager).toBe("false");
    expect(v.disk_gb).toBe("25");
    expect(initialInputValues([{ name: "v", options: ["1.31", "1.32"] }]).v).toBe("1.31");
    expect(initialInputValues(undefined)).toEqual({});
  });
  it("arma inputs con tipos y omite opcionales vacíos", () => {
    const values = { ...initialInputValues(doInputs), domain: " apps.acme.com ", ssh_allowed_cidrs: "10.0.0.0/8,\n10.0.0.0/8\n192.168.0.0/16" };
    expect(inputsToPayload(doInputs, values)).toEqual({
      region: "nyc3",
      size: "s-1vcpu-2gb",
      domain: "apps.acme.com",
      ssh_allowed_cidrs: ["10.0.0.0/8", "192.168.0.0/16"],
      install_cert_manager: false,
      disk_gb: 25,
    });
  });
  it("el request lleva module e inputs y repite los campos históricos", () => {
    const inputs: InfraModuleInput[] = [
      { name: "kubernetes_version", default: "1.33" },
      { name: "domain" },
      { name: "ingress_class", default: "nginx" },
      { name: "admin_principal_arns", type: "list" },
    ];
    const values = { ...initialInputValues(inputs), domain: "apps.dev.acme.com", admin_principal_arns: "arn:aws:iam::1:user/a,\n arn:aws:iam::1:user/a\narn:aws:iam::1:user/b" };
    const req = buildClusterRequest({ cloud_account_id: "c1", name: " rampa-nonprod ", env: "nonprod" }, { module: "aws/eks-cluster", inputs }, values);
    expect(req).toEqual({
      cloud_account_id: "c1",
      name: "rampa-nonprod",
      env: "nonprod",
      module: "aws/eks-cluster",
      inputs: {
        kubernetes_version: "1.33",
        domain: "apps.dev.acme.com",
        ingress_class: "nginx",
        admin_principal_arns: ["arn:aws:iam::1:user/a", "arn:aws:iam::1:user/b"],
      },
      kubernetes_version: "1.33",
      domain: "apps.dev.acme.com",
      ingress_class: "nginx",
      admin_principal_arns: ["arn:aws:iam::1:user/a", "arn:aws:iam::1:user/b"],
    });
    expect(parseList("")).toEqual([]);
  });
  it("valida cuenta, nombre, requeridos, números y ARNs", () => {
    const base = { cloud_account_id: "c1", name: "rampa-nonprod", env: "nonprod" as const };
    const values = { ...initialInputValues(doInputs), domain: "x.com" };
    expect(validateClusterForm(base, doInputs, values)).toEqual({});
    expect(validateClusterForm({ ...base, name: "Bad Name" }, doInputs, values).name).toBeDefined();
    expect(validateClusterForm({ ...base, cloud_account_id: "" }, doInputs, values).cloud_account_id).toBeDefined();
    expect(validateClusterForm(base, doInputs, { ...values, domain: "" }).domain).toBeDefined();
    expect(validateClusterForm(base, doInputs, { ...values, disk_gb: "abc" }).disk_gb).toBeDefined();
    const arns: InfraModuleInput[] = [{ name: "admin_principal_arns", type: "list" }];
    expect(validateClusterForm(base, arns, { admin_principal_arns: "juan" }).admin_principal_arns).toBeDefined();
  });
  it("saca el costo aproximado del tamaño elegido desde la descripción", () => {
    expect(sizeInputName(doInputs)).toBe("size");
    expect(costHint({ inputs: doInputs }, { input: "size", value: "s-1vcpu-1gb" })).toBe("s-1vcpu-1gb: ~6 USD/mes");
    expect(costHint({ inputs: doInputs }, { input: "size", value: "s-1vcpu-2gb" })).toBe("s-1vcpu-2gb: ~12 USD/mes");
    expect(costHint({ inputs: [{ name: "size" }] }, { input: "size", value: "x" })).toBeNull();
  });
});

describe("módulos y estados", () => {
  it("separa disponibles y próximamente por kind y provider", () => {
    const items = [
      { provider: "aws", kind: "cluster", available: true },
      { provider: "aws", kind: "vm", available: true },
      { provider: "gcp", kind: "cluster", available: false },
      { provider: "aws", kind: "db", available: true },
    ];
    const r = splitModules(items, "cluster");
    expect(r.available.map((m) => m.provider)).toEqual(["aws"]);
    expect(r.upcoming.map((m) => m.provider)).toEqual(["gcp"]);
    expect(splitModules(items, "vm", "aws").available).toHaveLength(1);
    expect(splitModules(items, "vm", "gcp").available).toHaveLength(0);
  });
  it("mapea tonos", () => {
    expect(toneForRun("planning")).toBe("amber");
    expect(toneForRun("succeeded")).toBe("green");
    expect(toneForRun("failed")).toBe("red");
    expect(toneForRun("destroyed")).toBe("gray");
  });
});

describe("domain e ingress_class en el formulario de cluster", () => {
  const mod = { module: "digitalocean/droplet-k3s", inputs: [{ name: "size", type: "string", required: true }] };
  it("un cluster administrado exige dominio; una máquina no", () => {
    const base = { cloud_account_id: "a", name: "rampa-vm", env: "nonprod" as const };
    expect(validateClusterForm({ ...base, kind: "cluster" }, mod.inputs, { size: "s" }).domain).toBeTruthy();
    expect(validateClusterForm({ ...base, kind: "vm" }, mod.inputs, { size: "s" }).domain).toBeUndefined();
  });
  it("valida el formato del dominio cuando viene", () => {
    const base = { cloud_account_id: "a", name: "rampa-vm", env: "nonprod" as const, kind: "vm" as const };
    expect(validateClusterForm({ ...base, domain: "no es un dominio" }, mod.inputs, { size: "s" }).domain).toBeTruthy();
    expect(validateClusterForm({ ...base, domain: "Apps.Dev.Acme.com" }, mod.inputs, { size: "s" }).domain).toBeUndefined();
  });
  it("manda domain e ingress_class al nivel raíz, en minúsculas, y los omite si están vacíos", () => {
    const req = buildClusterRequest({ cloud_account_id: "a", name: "x", env: "nonprod", kind: "vm", domain: " Apps.Acme.com ", ingress_class: "traefik" }, mod, { size: "s" });
    expect(req.domain).toBe("apps.acme.com");
    expect(req.ingress_class).toBe("traefik");
    const empty = buildClusterRequest({ cloud_account_id: "a", name: "x", env: "nonprod", kind: "vm" }, mod, { size: "s" });
    expect(empty.domain).toBeUndefined();
  });
});
