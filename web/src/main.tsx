import { StrictMode, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { loadClientConfig } from "./config.ts";
import { Client, TRANSPORT_FAILURE } from "./request.ts";
import type { components } from "./api/v1";

type Server = components["schemas"]["ServerBody"];

// A placeholder until the shell lands: it reads the client configuration and GET /server.
function Placeholder() {
  const [status, setStatus] = useState("Loading");
  useEffect(() => {
    let live = true;
    const client = new Client();
    client.setView({ principal: "", route: location.pathname, query: location.search });
    void (async () => {
      const config = await loadClientConfig();
      if (config.kind !== "ok") {
        if (live) setStatus(config.kind === "transport" ? TRANSPORT_FAILURE : config.reason);
        return;
      }
      const res = await client.read<Server>("/server");
      if (!live || res.kind === "discarded") return;
      if (res.kind === "ok") setStatus(`Server ${res.data.version}, API revision ${res.data.apiRevision}`);
      else if (res.kind === "transport") setStatus(TRANSPORT_FAILURE);
      else setStatus(`The server read failed (${res.kind})`);
    })();
    return () => {
      live = false;
    };
  }, []);
  return (
    <main>
      <h1>Bronzeward</h1>
      <p role="status" data-testid="status">
        {status}
      </p>
    </main>
  );
}

const root = document.getElementById("root");
if (root !== null) {
  createRoot(root).render(
    <StrictMode>
      <Placeholder />
    </StrictMode>,
  );
}
