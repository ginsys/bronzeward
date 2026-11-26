// @wdio/utils 10.1.0 names a declaration file for "./node" that the package does not ship (its own are under
// build/node/), so the one function used from it is declared here.
declare module "@wdio/utils/node" {
  export function setupBrowser(options: { cacheDir?: string }, capabilities: WebdriverIO.Capabilities[]): Promise<unknown[]> | undefined;
}
