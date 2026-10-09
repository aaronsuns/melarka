import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router";
import App from "./App";
import { api } from "./api/client";
import { setChannelsEnabled } from "./channels/channelsSwitch";
import { setServerDefault } from "./i18n/i18n";
import { applyOfflineSwitch, offlineOnFrom } from "./offline/register";
import "./styles.css";

// Fire-and-forget: setServerDefault no-ops until this answers (or never, if
// the network is down), so the app renders immediately in whatever locale
// this browser last used instead of blocking on the network.
// The same answer carries the offline-cache kill switch: the service worker
// is registered only once the server says the feature is on (offline at
// open, an already registered worker keeps working). channels false (the
// server has Channels off) hides the 频道 tab.
api
  .info()
  .then((i) => {
    setServerDefault(i.language);
    void applyOfflineSwitch(offlineOnFrom(i));
    setChannelsEnabled(i.channels !== false);
  })
  .catch(() => {});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <BrowserRouter>
      <App />
    </BrowserRouter>
  </StrictMode>,
);
