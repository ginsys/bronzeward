// A schema file of talos-schema/ imported as its own text (`?raw`, a Vite import suffix). The build
// types only `node`, so the suffix needs its declaration here.
declare module "*.json?raw" {
  const text: string;
  export default text;
}
