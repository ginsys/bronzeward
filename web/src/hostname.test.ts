import { readFileSync } from "node:fs";
import { createElement as h, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import type { components } from "./api/v1";
import { type Applied, hostnameLines } from "./hostname.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import type { Position } from "./nextstep.tsx";
import { appliedHostname, ReviewBody } from "./review.tsx";
import { HOSTNAME_UNREAD, RENAME_REACHES } from "./words.ts";

type Hostname = components["schemas"]["HostnameBody"];
type Review = components["schemas"]["ReviewBody"];
type Release = components["schemas"]["ReleaseBody"];

// A9 item 7 of the contract, whitespace collapsed so a sentence the document wraps still matches.
const itemSeven = () => {
  const spec = readFileSync(`${import.meta.dirname}/../../docs/spec/client-contracts.md`, "utf8");
  const from = spec.indexOf("7. **A release says where each machine's hostname came from**");
  const to = spec.indexOf("8. **A plan says when it waits");
  expect(from).toBeGreaterThan(0);
  expect(to).toBeGreaterThan(from);
  return spec.slice(from, to).replace(/\s+/g, " ");
};

const hb = (source: string, name?: string): Hostname => ({ source, ...(name === undefined ? {} : { name }) }) as Hostname;
const none: Applied = { state: "none" };
const was = (hostname: Hostname): Applied => ({ state: "known", hostname });
const node = "The machine's Kubernetes node name normally follows the hostname, so it may change too.";
const taken = "Hostname: node-a, taken from the machine's name.";
const sets = "This release sets the machine's hostname to node-a, taken from its name.";
const restart = RENAME_REACHES[1];

describe("what a release review says of a machine's hostname (client contracts A9 item 7)", () => {
  const cases: [string, Hostname | undefined, Applied, string | null | undefined, string[]][] = [
    ["machine-name, nothing applied", hb("machine-name", "node-a"), none, "node-a", [taken]],
    ["applied none", hb("machine-name", "node-a"), was(hb("none")), "node-a", [taken, `${sets} The last release applied to it left the hostname to the node. ${node}`, restart]],
    ["applied legacy-stable", hb("machine-name", "node-a"), was(hb("legacy-stable")), "node-a", [taken, `${sets} The last release applied to it left the hostname to the node. ${node}`, restart]],
    ["applied unrecorded", hb("machine-name", "node-a"), was(hb("unrecorded")), "node-a", [taken, sets, restart]],
    ["applied configuration", hb("machine-name", "node-a"), was(hb("configuration")), "node-a",
      [taken, "This release now takes the machine's hostname from its name, node-a, in place of the setting it had before.", node, restart]],
    ["applied another name", hb("machine-name", "node-b"), was(hb("machine-name", "node-a")), "node-b",
      ["Hostname: node-b, taken from the machine's name.", "This release sets the machine's hostname to node-b, taken from its name. The last release applied to it set node-a.", node, restart]],
    ["applied the same name", hb("machine-name", "node-a"), was(hb("machine-name", "node-a")), "node-a", [taken]],
    ["renamed since", hb("machine-name", "node-a"), was(hb("machine-name", "node-a")), "node-b",
      [taken, "This release used the earlier name node-a. While its hostname comes from the machine's name, the next release will use node-b."]],
    ["comparison unread", hb("machine-name", "node-a"), { state: "unread" }, "node-a", [taken, HOSTNAME_UNREAD]],
    ["comparison unread and renamed", hb("machine-name", "node-a"), { state: "unread" }, "node-b", [taken, HOSTNAME_UNREAD]],
    ["comparison still being read", hb("machine-name", "node-a"), { state: "pending" }, "node-a", [taken]],
    ["configuration", hb("configuration"), none, null, ["Hostname: set in the configuration. The machine's name does not change it."]],
    ["none", hb("none"), none, null, ["Hostname: not set in the configuration; the node chooses its own."]],
    ["legacy-stable", hb("legacy-stable"), none, null,
      ["Hostname: the configuration holds the legacy stable-hostname setting, so the machine's name is not used. Remove that setting to use the name."]],
    ["unrecorded", hb("unrecorded"), none, null, ["Hostname: not recorded for this release."]],
    ["a source this client does not know", hb("from-the-moon"), none, null, ["Hostname source: from-the-moon"]],
    ["a review that carries no hostname", undefined, none, null, []],
  ];

  test("each line is what the table says, and each is a sentence of item 7 word for word (the restart sentence is item 6's)", () => {
    const item = itemSeven();
    for (const [name, host, applied, current, want] of cases) {
      const got = hostnameLines(host, applied, current);
      expect(got, name).toEqual(want);
      if ((host?.source as string | undefined) === "from-the-moon") continue;
      for (const line of got) {
        // The document gives the sentences once, for node-a.
        const l = line.replace("Hostname: node-b,", "Hostname: node-a,");
        if (line !== restart) expect(item, `${name}: ${line}`).toContain(l);
      }
    }
  });

  test("a hostname taken from the configuration prints no value, and no line names an identifier or a field", () => {
    expect(hostnameLines(hb("configuration"), none, null).join(" ")).not.toMatch(/node|[a-z]+_[A-Za-z0-9]/);
    for (const [name, host, applied, current] of cases) {
      if ((host?.source as string | undefined) === "from-the-moon") continue; // that one shows the raw value by design
      const text = hostnameLines(host, applied, current).join(" ");
      expect(text, name).not.toMatch(/\b(source|machine-name|legacy-stable|hostname:|rel_|mch_)/);
    }
  });

  test("a name that is not safe text is never shown as it is", () => {
    const [line] = hostnameLines(hb("machine-name", "node‮a"), none, "node-a");
    expect(line).not.toContain("‮");
  });
});

const reads: Reads = { client: { read: () => new Promise(() => {}) }, settle: () => false, undropped: () => () => true, view: "", again: 0, regained: 0, go: () => {}, failure: () => null };
const html = (e: ReactNode) => renderToStaticMarkup(h(ReadsProvider, { value: reads }, e));
const review = (hostname: Hostname | undefined): Review =>
  ({ release: "rel_BWSYNTH-h7rt2bd", machine: "mch_BWSYNTH-b3caa2q", mode: "metal", configuration: "machine: {}\n", provenance: [], ...(hostname === undefined ? {} : { hostname }) }) as Review;

describe("the review page shows the hostname lines after its heading facts", () => {
  test("the lines are on the page in order, in their own block", () => {
    const page = html(h(ReviewBody, { r: review(hb("machine-name", "node-a")), machine: { id: "mch_BWSYNTH-b3caa2q", name: "node-b" }, applied: was(hb("none")) }));
    const block = /<div class="hostname" data-testid="hostname">(.*?)<\/div>/.exec(page)?.[1] ?? "";
    expect(block.startsWith("<h3>Hostname</h3>")).toBe(true);
    expect([...block.matchAll(/<p>(.*?)<\/p>/g)].map((m) => m[1]!.replaceAll("&#x27;", "'"))).toEqual(
      hostnameLines(hb("machine-name", "node-a"), was(hb("none")), "node-b"),
    );
    expect(page.indexOf("Validated in mode")).toBeLessThan(page.indexOf('data-testid="hostname"'));
  });

  test("a review with no hostname, or a withheld one, adds no block of its own", () => {
    expect(html(h(ReviewBody, { r: review(undefined) }))).not.toContain('data-testid="hostname"');
  });
});

describe("what the machine's last applied release recorded, from the position and that release's read", () => {
  const pos = (applied: boolean): { kind: "ok"; data: Position } =>
    ({ kind: "ok", data: { machine: { applied: applied ? { release: "rel_BWSYNTH-old", source: "bronzeward", importBase: null } : null } } as unknown as Position });
  const release = (machine: string): Release => ({ machines: [{ machine, hostname: hb("none") }] }) as unknown as Release;
  test("pending while the position or the release is being read, none without an applied release", () => {
    expect(appliedHostname({ kind: "loading" }, undefined, "mch_a")).toEqual({ state: "pending" });
    expect(appliedHostname(pos(true), undefined, "mch_a")).toEqual({ state: "pending" });
    expect(appliedHostname(pos(false), undefined, "mch_a")).toEqual({ state: "none" });
  });
  test("unread when a read gave nothing or the release holds no entry for the machine; known otherwise", () => {
    expect(appliedHostname({ kind: "error" } as never, undefined, "mch_a")).toEqual({ state: "unread" });
    expect(appliedHostname(pos(true), null, "mch_a")).toEqual({ state: "unread" });
    expect(appliedHostname(pos(true), release("mch_other"), "mch_a")).toEqual({ state: "unread" });
    expect(appliedHostname(pos(true), release("mch_a"), "mch_a")).toEqual({ state: "known", hostname: hb("none") });
  });
});
