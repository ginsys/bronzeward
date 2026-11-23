// Where the stable Edge leg gets its driver (ginsys/bronzeward#1121 group A).
import { chmodSync, mkdirSync, mkdtempSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { afterAll, describe, expect, it } from "vitest";
import { edgeCapability, edgeDriver, executableFile } from "./edge-driver.ts";

describe("edgeDriver", () => {
  it("uses the runner image's own driver when the image names one and the file is usable", () => {
    const d = edgeDriver({ EDGEWEBDRIVER: "/usr/local/share/edge_driver" }, (p) => p === "/usr/local/share/edge_driver/msedgedriver");
    expect(d).toEqual({ binary: "/usr/local/share/edge_driver/msedgedriver", line: "edge driver: /usr/local/share/edge_driver/msedgedriver (image)" });
  });

  it("downloads the driver when the image names none, or the file is not usable", () => {
    const download = { binary: undefined, line: "edge driver: download" };
    expect(edgeDriver({}, () => true)).toEqual(download);
    expect(edgeDriver({ EDGEWEBDRIVER: "" }, () => true)).toEqual(download);
    expect(edgeDriver({ EDGEWEBDRIVER: "/usr/local/share/edge_driver" }, () => false)).toEqual(download);
  });
});

describe("edgeCapability", () => {
  it("names the driver binary only when one was chosen", () => {
    expect(edgeCapability(true, { binary: "/x/msedgedriver" })).toEqual({
      browserName: "MicrosoftEdge", "ms:edgeOptions": { args: ["--headless=new"] }, "wdio:edgedriverOptions": { binary: "/x/msedgedriver" },
    });
    expect(edgeCapability(false, { binary: undefined })).toEqual({ browserName: "MicrosoftEdge", "ms:edgeOptions": { args: [] } });
  });
});

describe("executableFile", () => {
  const dir = mkdtempSync(`${tmpdir()}/bw-edge-driver-`);
  afterAll(() => rmSync(dir, { recursive: true, force: true }));
  const file = (name: string, mode: number) => {
    writeFileSync(`${dir}/${name}`, "#!/bin/sh\n");
    chmodSync(`${dir}/${name}`, mode);
    return `${dir}/${name}`;
  };

  it("accepts an executable file and a symlink to one", () => {
    const real = file("real", 0o755);
    symlinkSync(real, `${dir}/link`);
    expect(executableFile(real)).toBe(true);
    expect(executableFile(`${dir}/link`)).toBe(true);
  });

  it("refuses a file without the execute bit, a directory and a missing path", () => {
    mkdirSync(`${dir}/dir`);
    expect(executableFile(file("plain", 0o644))).toBe(false);
    expect(executableFile(`${dir}/dir`)).toBe(false);
    expect(executableFile(`${dir}/missing`)).toBe(false);
  });
});
