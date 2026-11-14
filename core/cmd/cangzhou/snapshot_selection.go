package main

import "net/http"

const snapshotSelectionJS = `"use strict";
(() => {
  const form = document.getElementById("snapshot-selection");
  if (!form) return;
  const key = "cangzhou-selection:" + form.dataset.selectionKey;
  const boxes = Array.from(form.querySelectorAll('input[name="selected"][type="checkbox"]'));
  const count = document.getElementById("selection-count");
  let chosen = new Set();
  try {
    const saved = JSON.parse(sessionStorage.getItem(key) || "[]");
    if (Array.isArray(saved)) chosen = new Set(saved.filter(x => typeof x === "string"));
  } catch (_) {}
  function update() {
    boxes.forEach(box => { box.checked = chosen.has(box.value); });
    count.textContent = "已选择 " + chosen.size + " 项（最多 100 项，可跨目录选择）。";
    try { sessionStorage.setItem(key, JSON.stringify([...chosen])); } catch (_) {}
  }
  boxes.forEach(box => box.addEventListener("change", () => {
    if (box.checked) chosen.add(box.value); else chosen.delete(box.value);
    update();
  }));
  document.getElementById("selection-clear").addEventListener("click", () => { chosen.clear(); update(); });
  form.addEventListener("submit", event => {
    if (chosen.size === 0 || chosen.size > 100) {
      event.preventDefault();
      count.textContent = "请选择 1 至 100 个文件或目录。";
      return;
    }
    const visible = new Set(boxes.filter(box => box.checked).map(box => box.value));
    form.querySelectorAll('[data-selection-extra]').forEach(el => el.remove());
    for (const path of chosen) {
      if (visible.has(path)) continue;
      const input = document.createElement("input");
      input.type = "hidden"; input.name = "selected"; input.value = path;
      input.dataset.selectionExtra = ""; form.appendChild(input);
    }
  });
  update();
})();`

func snapshotSelectionScript(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "请求方法不支持", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = w.Write([]byte(snapshotSelectionJS))
}
