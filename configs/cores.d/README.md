# Core Plugin Registry — 描述文件目录

把 `*.yaml` / `*.yml` / `*.json` 能力描述文件放到这里，重启 NGG（或调用
`POST /api/v1/registry/reload`）即可注册**新内核**，无需修改 NGG Network Engine，
也无需重新编译。字段说明见 `docs/03-development.md`，完整模板见
`configs/examples/kernel-template.yaml`。

注意：只有扩展名为 `.yaml/.yml/.json` 的文件会被加载；本 README.md 会被忽略。
