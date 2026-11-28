// The parsed read-only view of an imported configuration (docs/spec/configuration-editing.md §5, ginsys/bronzeward#1270): the
// tree builder reads the stored text with the schema of the cluster's contract. Every value in the fixtures is synthetic.

import { describe, expect, test } from "vitest";
import { buildConfigView, noFieldsReasons, noFieldsWords, type ConfigView, type DocView, type Field, type Other } from "./config-tree.ts";
import { noFormReasons, schemaFor, type FormSchema } from "./talos-schema.ts";

const schema = async (): Promise<FormSchema> => {
  const r = await schemaFor("v1.14");
  if (!r.form) throw new Error(`no schema: ${r.reason}`);
  return r;
};
const view = async (text: string): Promise<ConfigView> => buildConfigView(text, await schema());
const docs = async (text: string): Promise<readonly DocView[]> => {
  const v = await view(text);
  if (!v.ok) throw new Error(`parse error ${v.code}`);
  return v.docs;
};
const names = (fs: readonly Field[]) => fs.map((f) => f.name);
const field = (fs: readonly Field[], name: string): Field => {
  const f = fs.find((x) => x.name === name);
  if (f === undefined) throw new Error(`no field ${name} in ${names(fs).join(",")}`);
  return f;
};
const objectOf = (f: Field): readonly Field[] => {
  if (f.node.kind !== "object") throw new Error(`${f.name} is a ${f.node.kind}`);
  return f.node.fields;
};

const LEGACY = [
  "# header comment SYNTH-HEADER",
  "version: v1alpha1",
  "machine:",
  "  # commented example",
  "  #token: SYNTH-COMMENTED",
  "  type: controlplane",
  "  token: !bwref reg/machine-token # tail comment SYNTH-TAIL",
  "  certSANs:",
  "    - cp1.example.test",
  "    - 10.0.0.1",
  "  ca:",
  "    crt: SYNTH-CRT",
  "    key: !bwref reg/ca-key",
  "  network:",
  "    hostname: cp-1",
  "  zzz: 2",
  "cluster:",
  "  token: !bwref reg/cluster-token",
  "",
].join("\n");

describe("a legacy document", () => {
  test("is a heading, then a field for each key the schema knows, in document order, each with its help text", async () => {
    const [d] = await docs(LEGACY);
    expect(d!.heading).toBe("Document 1: v1alpha1 configuration");
    expect(names(d!.fields)).toEqual(["version", "machine", "cluster"]);
    const machine = objectOf(field(d!.fields, "machine"));
    expect(names(machine)).toEqual(["type", "token", "certSANs", "ca"]);
    expect(field(machine, "type").help).toMatch(/^Defines the role of the machine/);
    expect(field(machine, "type").node).toEqual({ kind: "text", text: "controlplane" });
  });

  test("shows a reference as its name, wherever the schema puts it, and never a value", async () => {
    const [d] = await docs(LEGACY);
    const machine = objectOf(field(d!.fields, "machine"));
    expect(field(machine, "token").node).toEqual({ kind: "ref", name: "reg/machine-token" });
    expect(field(objectOf(field(machine, "ca")), "key").node).toEqual({ kind: "ref", name: "reg/ca-key" });
    expect(field(objectOf(field(d!.fields, "cluster")), "token").node).toEqual({ kind: "ref", name: "reg/cluster-token" });
    // A reference in place of a whole object is still a reference.
    const [r] = await docs("machine:\n  ca: !bwref reg/ca\n");
    expect(field(objectOf(field(r!.fields, "machine")), "ca").node).toEqual({ kind: "ref", name: "reg/ca" });
  });

  test("hides every comment: a header, a commented example and a tail comment are nowhere in the view", async () => {
    const out = JSON.stringify(await view(LEGACY));
    for (const c of ["SYNTH-HEADER", "header comment", "commented example", "SYNTH-COMMENTED", "SYNTH-TAIL", "tail comment"]) expect(out).not.toContain(c);
  });

  test("shows a list as rows and an unset field as nothing", async () => {
    const [d] = await docs(LEGACY);
    const machine = objectOf(field(d!.fields, "machine"));
    expect(field(machine, "certSANs").node).toEqual({ kind: "list", rows: [{ kind: "text", text: "cp1.example.test" }, { kind: "text", text: "10.0.0.1" }] });
    expect(names(machine)).not.toContain("features");
  });

  test("puts a key the schema does not know under Other settings with its raw lines, in document order, and drops nothing", async () => {
    const [d] = await docs(LEGACY);
    expect(d!.other).toEqual([
      { path: "machine/network", lines: "  network:\n    hostname: cp-1" },
      { path: "machine/zzz", lines: "  zzz: 2" },
    ]);
    // Every key of the machine map is shown as a field or as an Other settings row.
    const shown = [...names(objectOf(field(d!.fields, "machine"))), ...d!.other.map((o) => o.path.split("/").pop())].sort();
    expect(shown).toEqual(["ca", "certSANs", "network", "token", "type", "zzz"]);
  });

  test("a block scalar keeps its lines", async () => {
    const [d] = await docs("machine:\n  token: |\n    line1\n    line2\n");
    expect(field(objectOf(field(d!.fields, "machine")), "token").node).toEqual({ kind: "text", text: "line1\nline2\n" });
  });
});

describe("what the view cannot show goes to Other settings, with the raw lines of that key only", () => {
  const other = async (body: string) => {
    const [d] = await docs(`machine:\n${body}`);
    return { fields: names(objectOf(field(d!.fields, "machine"))), other: d!.other };
  };
  test.each([
    ["an anchor", "  type: &t worker\n  token: x\n", "machine/type", "  type: &t worker"],
    ["an alias (its anchor is shown raw too)", "  token: &a x\n  type: *a\n  certSANs: [a.example.test]\n", "machine/type", "  type: *a", { path: "machine/token", lines: "  token: &a x" }],
    ["a tag other than the reference tag", "  type: !custom worker\n  token: x\n", "machine/type", "  type: !custom worker"],
    ["a $patch directive", "  $patch: delete\n  type: worker\n", "machine/$patch", "  $patch: delete"],
    ["a value of another type than the schema says (a number for a string)", "  token: 5\n  type: worker\n", "machine/token", "  token: 5"],
    ["a value of another type than the schema says (a string for a list)", "  certSANs: notalist\n  type: worker\n", "machine/certSANs", "  certSANs: notalist"],
    ["a choice the schema does not list", "  type: gateway\n  token: x\n", "machine/type", "  type: gateway"],
    ["a list with a row of another type", "  certSANs:\n    - a.example.test\n    - 7\n  type: worker\n", "machine/certSANs", "  certSANs:\n    - a.example.test\n    - 7"],
    ["a reference tag on a map", "  token: !bwref\n    a: b\n  type: worker\n", "machine/token", "  token: !bwref\n    a: b"],
  ] as [string, string, string, string, Other?][])("%s", async (_, body, path, lines, anchor) => {
    const got = await other(body);
    expect(got.other).toEqual([...(anchor === undefined ? [] : [anchor]), { path, lines }]);
    expect(got.fields).not.toContain(path.split("/").pop());
    expect(got.fields.length).toBeGreaterThan(0);
  });

  test("the keys around the one it cannot show keep their order", async () => {
    const got = await other("  type: worker\n  a-unknown: 1\n  token: x\n  b-unknown: 2\n");
    expect(got.fields).toEqual(["type", "token"]);
    expect(got.other.map((o) => o.path)).toEqual(["machine/a-unknown", "machine/b-unknown"]);
  });
});

describe("maps", () => {
  test("a map is rows of key and value, each value read by the schema's alternatives", async () => {
    const [d] = await docs("cluster:\n  etcd:\n    extraArgs:\n      listen-metrics-urls: http://0.0.0.0:2381\n      names: [a, b]\n");
    const etcd = objectOf(field(d!.fields, "cluster"));
    const extra = field(objectOf(field(etcd, "etcd")), "extraArgs").node;
    expect(extra).toEqual({
      kind: "map",
      rows: [
        { key: "listen-metrics-urls", node: { kind: "text", text: "http://0.0.0.0:2381" } },
        { key: "names", node: { kind: "list", rows: [{ kind: "text", text: "a" }, { kind: "text", text: "b" }] } },
      ],
    });
  });
});

describe("a stream of documents", () => {
  const STREAM = `${LEGACY}---\napiVersion: v1alpha1\nkind: HostnameConfig\nauto: stable\nhostname: cp-1\n`;
  test("each document is placed by its apiVersion and kind", async () => {
    const ds = await docs(STREAM);
    expect(ds.map((d) => d.heading)).toEqual(["Document 1: v1alpha1 configuration", "Document 2: HostnameConfig"]);
    expect(names(ds[1]!.fields)).toEqual(["apiVersion", "kind", "auto", "hostname"]);
    expect(field(ds[1]!.fields, "auto").node).toEqual({ kind: "text", text: "stable" });
  });

  test("a document the schema cannot place is one Other settings row of its raw lines, and the others still show as fields", async () => {
    const ds = await docs(`${LEGACY}---\napiVersion: v9\nkind: Nope\nx: 1\n`);
    expect(names(ds[0]!.fields)).toEqual(["version", "machine", "cluster"]);
    expect(ds[1]!.heading).toBe("Document 2");
    expect(ds[1]!.fields).toEqual([]);
    expect(ds[1]!.other).toEqual([{ path: "document", lines: "apiVersion: v9\nkind: Nope\nx: 1" }]);
  });

  test("a document with one of apiVersion and kind only is not the legacy document", async () => {
    const [d] = await docs("kind: HostnameConfig\nhostname: x\n");
    expect(d!.fields).toEqual([]);
    expect(d!.other).toHaveLength(1);
  });

  test("a document that is not a map is one Other settings row", async () => {
    const [d] = await docs("- a\n- b\n");
    expect(d!.fields).toEqual([]);
    expect(d!.other).toEqual([{ path: "document", lines: "- a\n- b" }]);
  });
});

describe("text with no settings or no parse", () => {
  test.each([["nothing", ""], ["only comments", "# machine:\n#   token: SYNTH-NOPE\n"]])("%s has no documents", async (_, text) => {
    expect(await view(text)).toEqual({ ok: true, docs: [] });
  });

  test.each([
    ["a duplicate key", "a: 1\na: SYNTH-DUP\n", "DUPLICATE_KEY"],
    ["a syntax error", "a: [1, 2\nb: SYNTH-SYNTAX\n", "BAD_INDENT"],
  ])("%s is a parse error that names a code and a line and quotes none of the text", async (_, text, code) => {
    const v = await view(text);
    expect(v).toMatchObject({ ok: false, code });
    if (v.ok) throw new Error("unreachable");
    expect(typeof v.line).toBe("number");
    expect(JSON.stringify(v)).not.toMatch(/SYNTH/);
  });

  test("an error in a later document is the whole text's error", async () => {
    expect(await view("a: 1\n---\nb: [\n")).toMatchObject({ ok: false });
  });
});

describe("the schema keeps no document value", () => {
  test("after a view is built, neither the schema object nor the reader's next answer holds a value of the text; the view does (the control)", async () => {
    const text = LEGACY.replace("SYNTH-CRT", "SYNTH-CACHE-CHECK");
    const s = await schema();
    const built = buildConfigView(text, s);
    expect(JSON.stringify(built)).toContain("SYNTH-CACHE-CHECK");
    expect(JSON.stringify(s)).not.toContain("SYNTH-CACHE-CHECK");
    expect(JSON.stringify(await schemaFor("v1.14"))).not.toContain("SYNTH-CACHE-CHECK");
  });
});

describe("the sentence when there are no fields", () => {
  test("has one for every reason of the schema reader and for an unread contract, each a read-only sentence", () => {
    const reasons = [...noFormReasons, "contract-unread"] as const;
    expect([...noFieldsReasons].sort()).toEqual([...reasons].sort());
    for (const r of noFieldsReasons) {
      const s = noFieldsWords(r);
      expect(s).toMatch(/no fields/);
      expect(s).toMatch(/\.$/);
      expect(s).not.toMatch(/[Ee]dit/);
      expect(s).toMatch(/text is shown\.$/);
    }
  });
});
