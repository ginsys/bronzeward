import { createRoot } from "react-dom/client";
import { App } from "./app.tsx";

// No StrictMode: its doubled effects in development would hand the callback's one-time pending
// entry to the first run and refuse the second (client contracts §9.3 item 1).
const root = document.getElementById("root");
if (root !== null) createRoot(root).render(<App />);
