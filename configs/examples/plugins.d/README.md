# 插件描述文件目录

把 `*.yaml` 插件 manifest 放到这里会自动加载（或 `POST /api/v1/plugins/reload`）。

```yaml
id: route-by-domain
name: 按域名分流
kind: javascript          # javascript | wasm
hooks: [session, dns]
permissions: [decision:write, log]
enabled: true
source: |
  var result = {};
  if (ngg.input.domain && ngg.input.domain.indexOf("video") >= 0) {
    result.core_id = "sing-box";
    result.tags = ["video"];
  }
  if (ngg.input.name === "blocked.example.com") { result.block = true; }
```

WASM 插件必须导出 `memory` 与 `ngg_hook() -> i32`（返回 1 表示阻断）。
JS 沙箱只暴露 `ngg.input` / `ngg.log` / `console.log` / `result`，无文件与网络能力。
