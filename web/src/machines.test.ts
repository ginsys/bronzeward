import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import { type Reads, ReadsProvider } from "./list.tsx";
import { type Machine, machineName, MachineRef, MachineSummary } from "./machines.tsx";

// A machine as the list reads it (client contracts A9, ginsys/bronzeward#986): its name the text of its link,
// the whole identifier the title, and the identifier shown whenever no name is shown.
const machine = (id: string, name: unknown): Machine => ({
  id, cluster: "cl_a", name, hardware: { smbiosUuid: null, talosNodeId: "node-a", serial: null }, platform: "metal", talosEndpoint: "10.5.0.2:50000",
  desired: null, applied: null, pendingConvergence: false, observed: null, frozen: false, scopeState: "normal", openDrift: null,
}) as Machine;
const page = (node: ReturnType<typeof createElement>) => renderToStaticMarkup(createElement(ReadsProvider, { value: { go: () => {} } as unknown as Reads }, node));
const ID = "mch_abcdefghijklmnopqrstuvwxyz";
const link = (text: string) => `<a href="/machines/${ID}" title="${ID}">${text}</a>`;

describe("machineName: the grammar a name is shown by", () => {
  test("a name of 1 to 63 lowercase letters, digits and inner hyphens is shown as received", () => {
    for (const n of ["a", "5", "web-1", "a-b", "web-1-2", "a".repeat(63), `a${"-".repeat(61)}a`]) expect(machineName(n)).toBe(n);
  });
  test("any other text, and anything that is not text, is not shown", () => {
    const bad = ["", "Web-1", "WEB", "-web", "web-", "-", "a".repeat(64), "web\n1", "web-1\n", "web 1", "web_1", "web.1", "wéb", "web‮1", "web​1", " web"];
    for (const n of bad) expect(machineName(n), JSON.stringify(n)).toBeUndefined();
    for (const n of [null, undefined, 5, true, {}, ["web-1"]]) expect(machineName(n)).toBeUndefined();
  });
});

describe("MachineRef: name or identifier, the whole identifier as the title", () => {
  test("a name is the text, the identifier the title", () => {
    expect(page(createElement(MachineRef, { id: ID, name: "web-1" }))).toBe(link("web-1"));
  });
  test("no name, no member (an older server) or a name that fails the grammar shows the shortened identifier", () => {
    const shown = link("mch_abcd…wxyz");
    expect(page(createElement(MachineRef, { id: ID, name: null }))).toBe(shown);
    expect(page(createElement(MachineRef, { id: ID }))).toBe(shown);
    expect(page(createElement(MachineRef, { id: ID, name: "Web-1" }))).toBe(shown);
  });
});

describe("a cluster's machine rows", () => {
  const first = (m: Machine) => /<th scope="row">(.*?)<\/th>/.exec(page(createElement(MachineSummary, { machines: [m] })))?.[1];
  test("a named machine, an unnamed one and one served by an older server", () => {
    expect(first(machine(ID, "web-1"))).toBe(link("web-1"));
    expect(first(machine(ID, null))).toBe(link("mch_abcd…wxyz"));
    const old = machine(ID, null);
    delete (old as { name?: unknown }).name;
    expect(first(old)).toBe(link("mch_abcd…wxyz"));
  });
  test("a name failing the grammar is never shown, and a valid one beside it still is", () => {
    const rows = [["Upper-1", "uppercase"], ["ctl\n1", "control character"], ["a".repeat(64), "64 characters"], ["-lead", "leading hyphen"], ["trail-", "trailing hyphen"]];
    const html = page(createElement(MachineSummary, { machines: [machine("mch_good", "web-1"), ...rows.map(([n], i) => machine(`mch_bad${i}`, n))] }));
    expect(html).toContain('title="mch_good">web-1</a>');
    for (const [n, why] of rows) expect(html, why).not.toContain(n);
    for (let i = 0; i < rows.length; i++) expect(html).toContain(`title="mch_bad${i}">mch_bad${i}</a>`);
  });
});
