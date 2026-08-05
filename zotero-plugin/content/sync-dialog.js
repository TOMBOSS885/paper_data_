(function () {
  const io = window.arguments[0];
  let rows = io.rows || [];
  let filter = "all";
  const selected = new Set();
  const body = document.getElementById("rows");

  const statusLabel = {
    local_only: "仅本地",
    server_only: "仅服务器",
    both_same: "双方都有/一致",
    both_changed: "双方都有/有差异",
    conflict: "冲突",
  };

  function text(value) {
    return value == null ? "" : String(value);
  }

  function rowStatus(row) {
    if (filter === "all") return true;
    if (filter === "both") return row.status === "both_same" || row.status === "both_changed";
    if (filter === "conflict") return row.status === "conflict" || row.status === "both_changed";
    return row.status === filter;
  }

  function makeCell(value, className) {
    const cell = document.createElementNS("http://www.w3.org/1999/xhtml", "td");
    if (className) cell.className = className;
    cell.textContent = text(value);
    return cell;
  }

  function render() {
    body.replaceChildren();
    const visible = rows.filter(rowStatus);
    for (const row of visible) {
      const tr = document.createElementNS("http://www.w3.org/1999/xhtml", "tr");
      tr.dataset.status = row.status;
      const checkCell = document.createElementNS("http://www.w3.org/1999/xhtml", "td");
      const check = document.createElementNS("http://www.w3.org/1999/xhtml", "input");
      check.type = "checkbox";
      check.checked = selected.has(row.rowId);
      check.addEventListener("change", () => { check.checked ? selected.add(row.rowId) : selected.delete(row.rowId); updateSummary(); });
      checkCell.appendChild(check); tr.appendChild(checkCell);
      tr.appendChild(makeCell(statusLabel[row.status] || row.status, "status"));
      const md = row.local?.metadata || row.server?.metadata || {};
      tr.appendChild(makeCell(md.title));
      tr.appendChild(makeCell(`${(md.authors || []).slice(0, 2).join(", ")}${md.year ? ` / ${md.year}` : ""}`));
      tr.appendChild(makeCell(md.doi));
      const file = row.server?.file;
      tr.appendChild(makeCell(row.local?.filePath ? "本地 PDF" : (file?.available ? "服务器 PDF" : "无 PDF"), "muted"));
      const action = document.createElementNS("http://www.w3.org/1999/xhtml", "td");
      if (row.status === "local_only" || row.status === "both_changed") {
        const upload = document.createElementNS("http://www.w3.org/1999/xhtml", "button"); upload.textContent = "上传"; upload.addEventListener("click", () => run("upload", row)); action.appendChild(upload);
      }
      if (row.status === "server_only" || row.status === "both_changed") {
        const pull = document.createElementNS("http://www.w3.org/1999/xhtml", "button"); pull.textContent = "拉取"; pull.addEventListener("click", () => run("pull", row)); action.appendChild(pull);
      }
      tr.appendChild(action); body.appendChild(tr);
    }
    document.getElementById("summary").value = `${rows.length} 项：${count("local_only")} 仅本地，${count("server_only")} 仅服务器，${count("both_same")} 一致，${count("both_changed")} 有差异`;
    updateSummary();
  }

  function count(status) { return rows.filter(row => row.status === status).length; }
  function updateSummary() { document.getElementById("selected").value = `已选 ${selected.size} 项`; }
  async function run(action, row) {
    if (action === "pull" && row.status === "both_changed" && !window.confirm("拉取会用服务器的书目元数据和主 PDF 更新本地条目。继续？")) return;
    try { await (action === "upload" ? io.upload(row) : io.pull(row)); alert(action === "upload" ? "上传完成" : "拉取完成"); rows = await io.refresh(); render(); }
    catch (error) { alert(`同步失败：${error}`); }
  }
  async function runSelected(action) {
    const targets = rows.filter(row => selected.has(row.rowId));
    for (const row of targets) {
      if (action === "upload" && row.status !== "local_only" && row.status !== "both_changed") continue;
      if (action === "pull" && row.status !== "server_only" && row.status !== "both_changed") continue;
      await run(action, row);
    }
    selected.clear(); render();
  }

  for (const button of document.querySelectorAll(".filter")) button.addEventListener("click", () => { filter = button.dataset.status; render(); });
  document.getElementById("upload").addEventListener("click", () => runSelected("upload"));
  document.getElementById("pull").addEventListener("click", () => runSelected("pull"));
  document.getElementById("refresh").addEventListener("click", async () => { rows = await io.refresh(); render(); });
  document.getElementById("close").addEventListener("click", () => window.close());
  render();
})();
