package main

import "net/http"

const liveRefreshJS = `"use strict";
(() => {
  if (document.body.dataset.liveBusy !== "true") return;
  let timer;
  let stopped = false;
  const schedule = (delay = 3000) => { if (!stopped) timer = setTimeout(update, delay); };
  async function update() {
    if (document.hidden) { schedule(); return; }
    try {
      const response = await fetch(location.href, { credentials: "same-origin", cache: "no-store", headers: { "X-Requested-With": "CangzhouLiveRefresh" } });
      if (response.status === 401 || response.status === 403) { stopped = true; return; }
      if (!response.ok || response.redirected) { schedule(); return; }
      const next = new DOMParser().parseFromString(await response.text(), "text/html");
      if (!next.body || !next.body.hasAttribute("data-live-busy")) { schedule(); return; }
      let deferred = false;
      document.querySelectorAll("[data-live-region][id]").forEach(current => {
        const replacement = next.getElementById(current.id);
        if (!replacement || replacement.innerHTML === current.innerHTML) return;
        if (current.contains(document.activeElement) || current.querySelector("details[open]")) { deferred = true; return; }
        current.replaceChildren(...Array.from(replacement.childNodes, node => document.importNode(node, true)));
      });
      document.querySelectorAll("[data-live-disabled][id]").forEach(current => {
        const replacement = next.getElementById(current.id);
        if (replacement) {
          current.disabled = replacement.disabled;
        } else if (next.body.dataset.liveBusy !== "true" && (current.id === "live-setup-fieldset" || current.id === "live-setup-submit")) {
          // A successful first task hides the blank creation form on a full reload.
          // Keep any values in the current form, but restore its usable state.
          current.disabled = false;
          if (current.id === "live-setup-submit") current.textContent = "创建任务 →";
        }
      });
      document.body.dataset.liveBusy = next.body.dataset.liveBusy;
      if (next.body.dataset.liveBusy === "true" || deferred) schedule(); else stopped = true;
    } catch (_) { schedule(5000); }
  }
  window.addEventListener("pagehide", () => { stopped = true; clearTimeout(timer); }, { once: true });
  schedule();
})();`

func liveRefreshScript(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "请求方法不支持", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = w.Write([]byte(liveRefreshJS))
}
