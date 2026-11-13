package main

import "net/http"

const davJS = `"use strict";
document.addEventListener("DOMContentLoaded", () => {
  const form = document.getElementById("dav-form");
  const button = document.getElementById("dav-test");
  const output = document.getElementById("dav-test-result");
  if (!form || !button || !output) return;
  const protocol = document.getElementById("remote-protocol");
  const switchMode = () => document.querySelectorAll("[data-remote]").forEach(el => { el.hidden = el.dataset.remote !== protocol.value; });
  if (protocol) { protocol.addEventListener("change", switchMode); switchMode(); }
  button.addEventListener("click", async () => {
    if (!form.reportValidity()) return;
    button.disabled = true; output.hidden = false;
    output.classList.remove("warning"); output.textContent = "正在测试连接…";
    try {
      const response = await fetch("/app/cangzhou/webdav/test", {
        method: "POST", credentials: "same-origin",
        body: new URLSearchParams(new FormData(form))
      });
      const result = await response.json();
      output.textContent = result.message || "测试未完成";
      if (!response.ok || !result.ok) output.classList.add("warning");
    } catch (_) {
      output.textContent = "测试请求失败，请检查飞牛网关和网络。";
      output.classList.add("warning");
    } finally { button.disabled = false; }
  });
});`

func davScript(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "请求方法不支持", 405)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = w.Write([]byte(davJS))
}
