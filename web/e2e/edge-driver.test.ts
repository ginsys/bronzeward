// Where the stable Edge leg gets its driver (ginsys/bronzeward#1121 group A).
import { describe, expect, it } from "vitest";
import { edgeDriver } from "./edge-driver.ts";

describe("edgeDriver", () => {
  it("uses the runner image's own driver when the image names one and the file is there", () => {
    const d = edgeDriver({ EDGEWEBDRIVER: "/usr/local/share/edge_driver" }, (p) => p === "/usr/local/share/edge_driver/msedgedriver");
    expect(d).toEqual({ binary: "/usr/local/share/edge_driver/msedgedriver", line: "edge driver: /usr/local/share/edge_driver/msedgedriver (image)" });
  });

  it("downloads the driver when the image names none, or the file is missing", () => {
    const download = { binary: undefined, line: "edge driver: download" };
    expect(edgeDriver({}, () => true)).toEqual(download);
    expect(edgeDriver({ EDGEWEBDRIVER: "" }, () => true)).toEqual(download);
    expect(edgeDriver({ EDGEWEBDRIVER: "/usr/local/share/edge_driver" }, () => false)).toEqual(download);
  });
});
