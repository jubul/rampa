import { describe, expect, it } from "vitest";
import es from "./es.json";
import en from "./en.json";

type Tree = { [k: string]: string | Tree };

function flatten(tree: Tree, prefix = ""): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(tree)) {
    const key = prefix ? `${prefix}.${k}` : k;
    if (typeof v === "string") out[key] = v;
    else Object.assign(out, flatten(v, key));
  }
  return out;
}

const flatEs = flatten(es as Tree);
const flatEn = flatten(en as Tree);

function placeholders(s: string): string[] {
  return Array.from(s.matchAll(/\{\{\s*(\w+)\s*\}\}/g), (m) => m[1]!).sort();
}

describe("catálogos de idioma", () => {
  it("es.json y en.json tienen exactamente las mismas claves", () => {
    const onlyEs = Object.keys(flatEs).filter((k) => !(k in flatEn));
    const onlyEn = Object.keys(flatEn).filter((k) => !(k in flatEs));
    expect({ onlyEs, onlyEn }).toEqual({ onlyEs: [], onlyEn: [] });
  });

  it("ninguna traducción está vacía", () => {
    const empty = [...Object.entries(flatEs).map(([k, v]) => ["es", k, v]), ...Object.entries(flatEn).map(([k, v]) => ["en", k, v])]
      .filter(([, , v]) => (v as string).trim() === "")
      .map(([l, k]) => `${l}:${k}`);
    expect(empty).toEqual([]);
  });

  it("cada clave usa las mismas variables en los dos idiomas", () => {
    const mismatched = Object.keys(flatEs).filter((k) => JSON.stringify(placeholders(flatEs[k]!)) !== JSON.stringify(placeholders(flatEn[k] ?? "")));
    expect(mismatched).toEqual([]);
  });

  it("el español no usa rayas largas (em-dash)", () => {
    expect(Object.values(flatEs).filter((v) => v.includes("—"))).toEqual([]);
    expect(Object.values(flatEn).filter((v) => v.includes("—"))).toEqual([]);
  });

  it("todas las claves literales usadas en el código existen", () => {
    // Todos los fuentes (menos los tests) como texto, con la misma resolución de Vite.
    const sources = import.meta.glob(["../**/*.ts", "../**/*.tsx", "!../**/*.test.ts"], { query: "?raw", import: "default", eager: true }) as Record<string, string>;
    expect(Object.keys(sources).length).toBeGreaterThan(20);
    const known = new Set(Object.keys(flatEs).map((k) => k.replace(/_(one|other|zero|few|many)$/, "")));
    const missing: string[] = [];
    for (const [f, src] of Object.entries(sources)) {
      for (const m of src.matchAll(/\bt\(\s*"([\w.]+)"/g)) if (!known.has(m[1]!)) missing.push(`${f}: ${m[1]}`);
      for (const m of src.matchAll(/i18nKey="([\w.]+)"/g)) if (!known.has(m[1]!)) missing.push(`${f}: ${m[1]}`);
    }
    expect(missing).toEqual([]);
  });
});
