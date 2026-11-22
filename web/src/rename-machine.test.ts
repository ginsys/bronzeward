// @vitest-environment happy-dom
// The rename form on a machine's page (ginsys/bronzeward#1055, client contracts A9 item 6): the offer by the machine's
// state, what is sent, what each refusal says, the grammar tested before anything is sent, and the three sentences
// that say when the name reaches the machine, compared with the contract's own text.

import { readFileSync } from "node:fs";
import { act, createElement as h } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, type Mock, test, vi } from "vitest";
import { RenameMachine, WritesProvider } from "./forms.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import { Client } from "./request.ts";
import { ShellContext } from "./shell.tsx";
import { NAME_THIS_MACHINE, RENAME_MACHINE, RENAME_REACHES } from "./words.ts";
import { Writes } from "./write.ts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const EPOCH = "e1";
const MCH = "mch_b";
const HOLDER = "mch_holder";
const base = { id: MCH, cluster: "cl_a", hardware: { smbiosUuid: null, talosNodeId: "node-a", serial: null }, platform: "metal", talosEndpoint: "10.5.0.2:50000" };
const unnamed = { ...base, name: null } as never;
const named = { ...base, name: "cp-1" } as never;

const json = (status: number, body: unknown, type = "application/json") => new Response(JSON.stringify(body), { status, headers: { "Content-Type": type, "Bronzeward-Epoch": EPOCH } });
const refusal = (status: number, code: string, extra: Record<string, unknown> = {}) => json(status, { type: `urn:bronzeward:problem:${code}`, title: code, status, ...extra }, "application/problem+json");
const recovery = () => json(200, { mode: "normal" });
const changed = (previousName: string | null, name: string) => json(200, { machine: MCH, revision: 7, previousName, name, changedBy: "p", role: "author", act: "act_1", epoch: EPOCH, at: "2026-11-22T07:00:00Z" });

let root: Root;
let box: HTMLElement;
let sent: { method: string; path: string; body: unknown }[];
let answers: Response[];
let onChanged: Mock<() => void>;

const reads = { client: { read: async () => ({ kind: "refused", status: 404, problem: null }) }, settle: () => false, undropped: () => () => true, view: "v", again: 0, regained: 0, go: () => {}, failure: () => "FAILED" } as unknown as Reads;

const mount = async (machine: never) => {
  const fetchMock = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    const path = String(url).replace("/api/v1", "");
    if (path === "/recovery") return recovery();
    sent.push({ method: init?.method ?? "GET", path, body: init?.body === undefined ? undefined : JSON.parse(String(init.body)) });
    return answers.shift()!;
  });
  const writes = new Writes(() => "p", async () => {});
  writes.client = new Client({ token: () => "SYNTH-token", fetch: fetchMock as unknown as typeof fetch });
  await act(async () => {
    root.render(h(WritesProvider, { value: writes }, h(ShellContext, { value: { roles: ["author"], offline: false } }, h(ReadsProvider, { value: reads }, h(RenameMachine, { machine, onChanged })))));
  });
};
const input = () => box.querySelector<HTMLInputElement>('input[name="name"]')!;
const send = async (typed: string) => {
  const el = input();
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(el, typed);
  await act(async () => {
    box.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await new Promise((r) => setTimeout(r, 30));
  });
};
const text = (id: string) => box.querySelector(`[data-testid="${id}"]`)?.textContent;

beforeEach(() => {
  box = document.createElement("div");
  document.body.append(box);
  root = createRoot(box);
  sent = [];
  answers = [];
  onChanged = vi.fn<() => void>();
});
afterEach(async () => {
  await act(async () => root.unmount());
  box.remove();
});

describe("the offer", () => {
  test("an unnamed machine is offered Name this machine, with an empty field", async () => {
    await mount(unnamed);
    expect(box.querySelector("form")!.getAttribute("aria-label")).toBe(NAME_THIS_MACHINE);
    expect(box.querySelector("button")!.textContent).toBe(NAME_THIS_MACHINE);
    expect(input().value).toBe("");
  });

  test("a named machine is offered Rename machine, with its name in the field", async () => {
    await mount(named);
    expect(box.querySelector("button")!.textContent).toBe(RENAME_MACHINE);
    expect(input().value).toBe("cp-1");
  });
});

describe("what is sent", () => {
  test("the trimmed name goes to the machine's names route, and the page reads the machine again", async () => {
    answers.push(changed(null, "cp-2"));
    await mount(unnamed);
    await send("  cp-2 ");
    expect(sent).toEqual([{ method: "POST", path: `/machines/${MCH}/names`, body: { name: "cp-2" } }]);
    expect(text("write-result")).toBe("Machine named cp-2 (revision 7).");
    expect(onChanged).toHaveBeenCalled();
  });

  test("a rename says the name it replaced", async () => {
    answers.push(changed("cp-1", "cp-2"));
    await mount(named);
    await send("cp-2");
    expect(text("write-result")).toBe("Machine renamed from cp-1 to cp-2 (revision 7).");
  });

  test("a name outside the grammar is not sent, and the form says so", async () => {
    await mount(unnamed);
    for (const typed of ["CP-1", "cp_1", "-cp", "cp-", "a..b", "10.0.0.1", "x".repeat(64)]) {
      await send(typed);
      expect(text("rename-not-sent")).toContain(`Not sent: "${typed}" is not a name the server takes`);
    }
    expect(sent).toEqual([]);
    expect(onChanged).not.toHaveBeenCalled();
  });

  test("a name in the grammar clears the not-sent line", async () => {
    answers.push(changed(null, "cp-2"));
    await mount(unnamed);
    await send("CP");
    expect(text("rename-not-sent")).toBeDefined();
    await send("cp-2");
    expect(text("rename-not-sent")).toBeUndefined();
  });
});

describe("what a refusal says", () => {
  test("name-in-use says another machine has the name, and names that machine as a link rather than an identifier", async () => {
    answers.push(refusal(409, "name-in-use", { machine: HOLDER }));
    await mount(unnamed);
    await send("cp-2");
    expect(text("write-result")).toContain("Another machine in this cluster already has this name.");
    expect(text("write-result")).toContain("Choose a different name.");
    expect(text("rename-holder")).toContain("The machine that has this name:");
    expect(box.querySelector('[data-testid="rename-holder"] a')!.getAttribute("href")).toBe(`/machines/${HOLDER}`);
    expect(onChanged).not.toHaveBeenCalled();
  });

  test("the machine's own current name is refused as a conflict and shows no holder", async () => {
    answers.push(refusal(409, "conflict", { machine: MCH }));
    await mount(named);
    await send("cp-1");
    expect(text("write-result")).toContain("Not saved");
    expect(text("rename-holder")).toBeUndefined();
    expect(text("write-existing")).toBeUndefined();
  });

  test("a 400 from the server shows as it words it, and no holder", async () => {
    answers.push(refusal(400, "invalid-request", { detail: "name is not a hostname" }));
    await mount(unnamed);
    await send("cp-2");
    expect(text("write-result")).toContain("name is not a hostname");
    expect(text("rename-holder")).toBeUndefined();
  });

  test("any other refusal is the form's own line", async () => {
    answers.push(refusal(403, "forbidden"));
    await mount(unnamed);
    await send("cp-2");
    expect(text("write-result")).toContain("Not saved");
    expect(onChanged).not.toHaveBeenCalled();
  });
});

describe("when the name reaches the machine (client contracts A9 item 6)", () => {
  const spec = readFileSync(`${import.meta.dirname}/../../docs/spec/client-contracts.md`, "utf8").replace(/\s+/g, " ");

  test("the three sentences are on the form for an unnamed and a named machine, word for word", async () => {
    for (const machine of [unnamed, named]) {
      await mount(machine);
      expect(text("rename-reaches")).toBe(RENAME_REACHES.join(" "));
    }
  });

  test("each sentence is the contract's own text", () => {
    for (const s of RENAME_REACHES) expect(spec).toContain(s);
  });
});
