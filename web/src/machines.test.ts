import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import { type Reads, ReadsProvider } from "./list.tsx";
import { machineAddress, machineHeading, type Machine, machineName, machineOption, machineOptions, MachineRef, MachineSummary, machineText, sharedAddresses } from "./machines.tsx";

// A machine as the list reads it (client contracts A9, ginsys/bronzeward#986, as ginsys/bronzeward#1055 amends them for
// the web): its name the text of its link, else its address, the whole identifier the title.
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

// A machine on an address: its endpoint, with the node ID or UUID it was recorded by.
const at = (id: string, name: unknown, talosEndpoint: unknown, hardware: Partial<Machine["hardware"]> = {}): Machine =>
  ({ ...machine(id, name), talosEndpoint, hardware: { smbiosUuid: null, talosNodeId: null, serial: null, ...hardware } }) as Machine;

describe("machineAddress: the stored form of an endpoint, without its port", () => {
  test("an IPv4 address and a bracketed IPv6 address are shown without the port", () => {
    expect(machineAddress("172.31.104.15:50000")).toBe("172.31.104.15");
    expect(machineAddress("[fd00::1]:50000")).toBe("fd00::1");
    expect(machineAddress("[::ffff:10.0.0.1]:1")).toBe("::ffff:10.0.0.1");
  });
  test("anything of another shape shows nothing: a host name, no port, a port out of range, an octet out of range, an unbracketed IPv6, text that is not text", () => {
    const bad = ["cp1.office.ginsys.net:50000", "172.31.104.15", "172.31.104.15:0", "172.31.104.15:65536", "172.31.104.256:50000", "fd00::1:50000", "[fd00::1]", "[FD00::1]:50000", "172.31.104.15:50000\n", " 172.31.104.15:50000", `${"1".repeat(64)}:5`, ""];
    for (const e of bad) expect(machineAddress(e), JSON.stringify(e)).toBeUndefined();
    for (const e of [null, undefined, 5, {}, ["10.0.0.1:1"]]) expect(machineAddress(e)).toBeUndefined();
  });
});

describe("machineText: a name, else the address, else an unnamed machine; never the identifier", () => {
  test("a valid name wins over the address", () => {
    expect(machineText(at(ID, "cp1", "172.31.104.15:50000"))).toBe("cp1");
  });
  test("no name, or a name that fails the grammar, shows the address", () => {
    expect(machineText(at(ID, null, "172.31.104.15:50000"))).toBe("172.31.104.15");
    expect(machineText(at(ID, "Web-1", "172.31.104.15:50000"))).toBe("172.31.104.15");
    expect(machineText({ id: ID, talosEndpoint: "[fd00::1]:50000" })).toBe("fd00::1");
  });
  test("no name and no usable address is an unnamed machine, and the identifier is in no text", () => {
    for (const e of [null, undefined, "not an endpoint", "cp1:50000"]) {
      const text = machineText(at(ID, null, e));
      expect(text).toBe("an unnamed machine");
      expect(text).not.toContain("mch_");
    }
  });
  test("the heading says Machine and the text, or Unnamed machine", () => {
    expect(machineHeading(at(ID, "cp1", null))).toBe("Machine cp1");
    expect(machineHeading(at(ID, null, "172.31.104.15:50000"))).toBe("Machine 172.31.104.15");
    expect(machineHeading(at(ID, null, null))).toBe("Unnamed machine");
  });
});

describe("sharedAddresses: two unnamed machines of the list in hand on one address", () => {
  const a = at("mch_a", null, "172.31.104.15:50000", { talosNodeId: "4c1f9e2a77" });
  const b = at("mch_b", null, "172.31.104.15:50001", { smbiosUuid: "9a3e1c5d-0000" });
  const c = at("mch_c", null, "172.31.104.16:50000", { talosNodeId: "other" });
  test("the shared address gets the shortened node ID or UUID, and an address held once gets none", () => {
    const shared = sharedAddresses([a, b, c]);
    expect([...shared.keys()]).toEqual(["172.31.104.15"]);
    expect(machineText(a, shared)).toBe("172.31.104.15 (node 4c1f…)");
    expect(machineText(b, shared)).toBe("172.31.104.15 (UUID 9a3e…)");
    expect(machineText(c, shared)).toBe("172.31.104.16");
  });
  test("a short value is shown whole, and a machine with neither is called unknown", () => {
    const shared = sharedAddresses([at("mch_d", null, "10.0.0.1:1", { talosNodeId: "n1" }), at("mch_e", null, "10.0.0.1:2")]);
    expect(machineText(at("mch_d", null, "10.0.0.1:1", { talosNodeId: "n1" }), shared)).toBe("10.0.0.1 (node n1)");
    expect(machineText(at("mch_e", null, "10.0.0.1:2"), shared)).toBe("10.0.0.1 (UUID unknown)");
  });
  test("machines whose values share a prefix get a longer suffix, until they differ", () => {
    const vm = (id: string, uuid: string) => at(id, null, "10.0.0.3:50000", { smbiosUuid: uuid });
    const pair = [vm("mch_h", "564d1a2b-0000-0000-0000-000000000001"), vm("mch_i", "564d1a2c-0000-0000-0000-000000000002")];
    const shared = sharedAddresses(pair);
    expect(machineText(pair[0]!, shared)).toBe("10.0.0.3 (UUID 564d1a2b…)");
    expect(machineText(pair[1]!, shared)).toBe("10.0.0.3 (UUID 564d1a2c…)");
  });
  test("two machines with one value cannot be told apart, and the cut stays at four", () => {
    const same = [at("mch_j", null, "10.0.0.4:1", { talosNodeId: "abcdef0123" }), at("mch_k", null, "10.0.0.4:2", { talosNodeId: "abcdef0123" })];
    expect(machineText(same[0]!, sharedAddresses(same))).toBe("10.0.0.4 (node abcd…)");
  });
  test("machines that can never differ do not drag a distinct one out to its whole value", () => {
    const three = [
      at("mch_l", null, "10.0.0.5:1", { talosNodeId: "bad‮id-one" }),
      at("mch_m", null, "10.0.0.5:2", { talosNodeId: "bad‮id-two" }),
      at("mch_n", null, "10.0.0.5:3", { smbiosUuid: "9a3e1c5d-0000-0000-0000-000000000009" }),
    ];
    const shared = sharedAddresses(three);
    expect(machineText(three[0]!, shared)).toBe("10.0.0.5 (node unknown)");
    expect(machineText(three[2]!, shared)).toBe("10.0.0.5 (UUID 9a3e…)");
  });
  test("a node ID or UUID that is not safe text, or not text, reads unknown in the suffix", () => {
    const pair = [at("mch_f", null, "10.0.0.2:1", { talosNodeId: "bad‮id" }), at("mch_g", null, "10.0.0.2:2", { talosNodeId: null, smbiosUuid: "ok\nid" })];
    const shared = sharedAddresses(pair);
    expect(machineText(pair[0]!, shared)).toBe("10.0.0.2 (node unknown)");
    expect(machineText(pair[1]!, shared)).toBe("10.0.0.2 (UUID unknown)");
  });
  test("a named machine neither counts nor gets a suffix, and a pair on different pages gets none", () => {
    expect(sharedAddresses([at("mch_a", "cp1", "172.31.104.15:50000"), at("mch_b", null, "172.31.104.15:50000")]).size).toBe(0);
    expect(machineText(a)).toBe("172.31.104.15");
    expect(machineText(a, sharedAddresses([a]))).toBe("172.31.104.15");
  });
});

describe("machineOption: the text, and the endpoint beside it where the text is a name", () => {
  test("a name shows its endpoint; an address is not repeated", () => {
    expect(machineOption(at(ID, "cp1", "172.31.104.15:50000"))).toBe("cp1 172.31.104.15:50000");
    // An endpoint that is not of its stored form is not shown beside the name.
    expect(machineOption(at(ID, "cp1", "172.31.104.15:50000‮"))).toBe("cp1");
    expect(machineOption(at(ID, "cp1", "cp1.office:50000"))).toBe("cp1");
    expect(machineOption(at(ID, null, "172.31.104.15:50000"))).toBe("172.31.104.15");
    expect(machineOption(at(ID, null, null))).toBe("an unnamed machine");
  });
});

describe("machineOptions: choices of a list across clusters tell alike named machines apart", () => {
  const two = [
    at("mch_1", "cp1", "10.0.0.1:50000", { talosNodeId: "4c1f9a2b3c4d" }),
    at("mch_2", "cp1", "10.0.0.1:50000", { talosNodeId: "9b22d0aa11bb" }),
  ];
  test("two machines named alike with one endpoint carry their node ID, and no other choice does", () => {
    const three = [...two, at("mch_3", "cp2", "10.0.0.1:50000", { talosNodeId: "7777aa889900" })];
    const shown = machineOptions(three);
    expect(shown.get("mch_1")).toBe("cp1 10.0.0.1:50000 (node 4c1f…)");
    expect(shown.get("mch_2")).toBe("cp1 10.0.0.1:50000 (node 9b22…)");
    expect(shown.get("mch_3")).toBe("cp2 10.0.0.1:50000");
  });
  test("the suffix is lengthened until the two differ, and a UUID is named as one", () => {
    const shown = machineOptions([
      at("mch_1", "cp1", null, { smbiosUuid: "564d1111-aaaa" }),
      at("mch_2", "cp1", null, { smbiosUuid: "564d2222-bbbb" }),
    ]);
    expect(shown.get("mch_1")).toBe("cp1 (UUID 564d1…)");
    expect(shown.get("mch_2")).toBe("cp1 (UUID 564d2…)");
  });
  test("a list with no alike choices is the plain options; unnamed machines keep the suffix of their address only", () => {
    expect([...machineOptions([at("mch_1", "cp1", "10.0.0.1:50000"), at("mch_2", "cp2", "10.0.0.1:50000")]).values()]).toEqual(["cp1 10.0.0.1:50000", "cp2 10.0.0.1:50000"]);
    const unnamed = [at("mch_1", null, "10.0.0.1:50000", { talosNodeId: "aaaa1111" }), at("mch_2", null, "10.0.0.1:50000", { talosNodeId: "bbbb2222" })];
    expect(machineOptions(unnamed, sharedAddresses(unnamed)).get("mch_1")).toBe("10.0.0.1 (node aaaa1111)");
  });
});

describe("MachineRef: the text, the whole identifier as the title", () => {
  test("a name is the text, the identifier the title", () => {
    expect(page(createElement(MachineRef, { machine: machine(ID, "web-1") }))).toBe(link("web-1"));
  });
  test("no name, no member (an older server) or a name that fails the grammar shows the address", () => {
    const shown = link("10.5.0.2");
    expect(page(createElement(MachineRef, { machine: machine(ID, null) }))).toBe(shown);
    const old = machine(ID, null);
    delete (old as { name?: unknown }).name;
    expect(page(createElement(MachineRef, { machine: old }))).toBe(shown);
    expect(page(createElement(MachineRef, { machine: machine(ID, "Web-1") }))).toBe(shown);
  });
  test("no address either shows an unnamed machine, never the identifier", () => {
    expect(page(createElement(MachineRef, { machine: at(ID, null, null) }))).toBe(link("an unnamed machine"));
  });
});

describe("a cluster's machine rows", () => {
  const first = (m: Machine) => /<th scope="row">(.*?)<\/th>/.exec(page(createElement(MachineSummary, { machines: [m] })))?.[1];
  test("a named machine, an unnamed one and one served by an older server", () => {
    expect(first(machine(ID, "web-1"))).toBe(link("web-1"));
    expect(first(machine(ID, null))).toBe(link("10.5.0.2"));
    const old = machine(ID, null);
    delete (old as { name?: unknown }).name;
    expect(first(old)).toBe(link("10.5.0.2"));
  });
  test("a name failing the grammar is never shown, and a valid one beside it still is", () => {
    const rows = [["Upper-1", "uppercase"], ["ctl\n1", "control character"], ["a".repeat(64), "64 characters"], ["-lead", "leading hyphen"], ["trail-", "trailing hyphen"]];
    const html = page(createElement(MachineSummary, { machines: [machine("mch_good", "web-1"), ...rows.map(([n], i) => machine(`mch_bad${i}`, n))] }));
    expect(html).toContain('title="mch_good">web-1</a>');
    for (const [n, why] of rows) expect(html, why).not.toContain(n);
    for (let i = 0; i < rows.length; i++) expect(html).toContain(`title="mch_bad${i}">10.5.0.2 (node node-a)</a>`);
  });
  test("two unnamed machines on one address are told apart in the same list", () => {
    const html = page(createElement(MachineSummary, { machines: [at("mch_a", null, "10.0.0.9:50000", { talosNodeId: "4c1f9e2a77" }), at("mch_b", null, "10.0.0.9:50000", { talosNodeId: "5d2a0b3c88" })] }));
    expect(html).toContain('title="mch_a">10.0.0.9 (node 4c1f…)</a>');
    expect(html).toContain('title="mch_b">10.0.0.9 (node 5d2a…)</a>');
  });
});
