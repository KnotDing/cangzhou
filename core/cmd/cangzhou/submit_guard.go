package main

import "net/http"

const submitGuardJS = `"use strict";
(() => {
  const locked = new Map();
  function unlock(form) {
    const buttons = locked.get(form);
    if (!buttons) return;
    buttons.forEach(button => { button.disabled = false; });
    locked.delete(form);
    delete form.dataset.submitting;
  }
  function lock(form) {
    form.dataset.submitting = "true";
    const buttons = Array.from(form.querySelectorAll('button:not([type]), button[type="submit"], input[type="submit"], input[type="image"]')).filter(button => !button.disabled);
    locked.set(form, buttons);
    // Let the browser first construct the native form submission, including the submitter.
    setTimeout(() => { if (locked.has(form)) buttons.forEach(button => { button.disabled = true; }); }, 0);
  }
  document.addEventListener("submit", event => {
    const form = event.target;
    if (!(form instanceof HTMLFormElement) || event.defaultPrevented) return;
    if (form.dataset.submitting === "true") { event.preventDefault(); return; }
    if (form.method.toLowerCase() === "dialog") return;
    if (form.method.toLowerCase() === "post" && new URL(form.action, location.href).pathname === "/app/cangzhou/export/download") {
      event.preventDefault();
      const body = new URLSearchParams(new FormData(form));
      lock(form);
      let status = form.querySelector("[data-submit-status]");
      if (!status) {
        status = document.createElement("p");
        status.className = "tip warning";
        status.setAttribute("role", "status");
        status.dataset.submitStatus = "";
        form.appendChild(status);
      }
      status.hidden = true;
      fetch(form.action, {method: "POST", credentials: "same-origin", body})
        .then(async response => {
          if (!response.ok || response.redirected) throw new Error("download failed");
          const blob = await response.blob();
          const url = URL.createObjectURL(blob);
          const link = document.createElement("a");
          link.href = url; link.download = "cangzhou-settings.json";
          document.body.appendChild(link); link.click(); link.remove();
          setTimeout(() => URL.revokeObjectURL(url), 60000);
        })
        .catch(() => { status.textContent = "设置导出失败，请检查登录状态和网络后重试。"; status.hidden = false; })
        .finally(() => unlock(form));
      return;
    }
    lock(form);
  });
  window.addEventListener("pageshow", () => { for (const form of locked.keys()) unlock(form); });
})();`

func submitGuardScript(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "请求方法不支持", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = w.Write([]byte(submitGuardJS))
}
