import { describe, expect, it } from "vitest";
import {
  formValuesToRequest,
  initialLogState,
  inputsToFormValues,
  logReducer,
  parseArns,
  parseLogEvent,
  splitClusterModules,
  toneForRun,
  validateClusterForm,
} from "./infra";

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

describe("inputs de módulo a formulario", () => {
  it("toma defaults y opciones del módulo", () => {
    const v = inputsToFormValues([
      { name: "kubernetes_version", default: "1.33", options: ["1.31", "1.33"] },
      { name: "ingress_class", default: "traefik" },
    ]);
    expect(v.kubernetes_version).toBe("1.33");
    expect(v.ingress_class).toBe("traefik");
    expect(v.env).toBe("nonprod");
  });
  it("usa la primera opción sin default y fallbacks sin inputs", () => {
    expect(inputsToFormValues([{ name: "kubernetes_version", options: ["1.31", "1.32"] }]).kubernetes_version).toBe("1.31");
    const v = inputsToFormValues(undefined);
    expect(v.kubernetes_version).toBe("1.33");
    expect(v.ingress_class).toBe("nginx");
  });
  it("arma el request con ARNs limpios", () => {
    const v = { ...inputsToFormValues([]), cloud_account_id: "c1", name: " rampa-nonprod ", domain: " apps.dev.acme.com ", admin_arns: "arn:aws:iam::1:user/a,\n arn:aws:iam::1:user/a\narn:aws:iam::1:user/b" };
    expect(formValuesToRequest(v)).toEqual({
      cloud_account_id: "c1",
      name: "rampa-nonprod",
      env: "nonprod",
      kubernetes_version: "1.33",
      domain: "apps.dev.acme.com",
      ingress_class: "nginx",
      admin_principal_arns: ["arn:aws:iam::1:user/a", "arn:aws:iam::1:user/b"],
    });
    expect(parseArns("")).toEqual([]);
  });
  it("valida el formulario", () => {
    const base = { ...inputsToFormValues([]), cloud_account_id: "c1", name: "rampa-nonprod", domain: "x.com" };
    expect(validateClusterForm(base)).toEqual({});
    expect(validateClusterForm({ ...base, name: "Bad Name" }).name).toBeDefined();
    expect(validateClusterForm({ ...base, admin_arns: "juan" }).admin_arns).toBeDefined();
    expect(validateClusterForm({ ...base, cloud_account_id: "" }).cloud_account_id).toBeDefined();
  });
});

describe("módulos y estados", () => {
  it("separa disponibles y próximamente", () => {
    const r = splitClusterModules([
      { provider: "aws", kind: "cluster", available: true },
      { provider: "gcp", kind: "cluster", available: false },
      { provider: "aws", kind: "db", available: true },
    ]);
    expect(r.available.map((m) => m.provider)).toEqual(["aws"]);
    expect(r.upcoming.map((m) => m.provider)).toEqual(["gcp"]);
  });
  it("mapea tonos", () => {
    expect(toneForRun("planning")).toBe("amber");
    expect(toneForRun("succeeded")).toBe("green");
    expect(toneForRun("failed")).toBe("red");
    expect(toneForRun("destroyed")).toBe("gray");
  });
});
