# NekoSimulator233 开发约定

## 每次完成修改后的强制步骤
1. 构建最新 MSI 并**安装到本机**，确保运行的是刚改过的版本：
   ```powershell
   $env:PATH += ";$env:USERPROFILE\.dotnet\tools"   # WiX
   powershell.exe -NoProfile -File scripts/build-msi.ps1 -Version <当前版本>
   powershell.exe -NoProfile -File install.ps1 -MsiPath .\build\NekoSimulator233-<版本>-x64.msi -Quiet
   ```
   **MSI 默认不打包 Android 运行时**（包体约 12 MB）；运行时由 `neko_download_runtime` 或主窗口“下载运行组件…”按 manifest 独立下载到 `%LOCALAPPDATA%\NekoSimulator233-data\runtime\sdk`。只有定制离线包才用 `-WithRuntime`。
   版本号必须递增，否则 MSI 不会覆盖已装文件。
2. 用 agent-desktop（`agent-desktop status` / `list-windows` / `snapshot` / `find` / `click`）检查安装版 GUI，确认新功能可见可操作，并回读验证。
3. 跑 `go test ./... -p 1` 和 `go vet`，确认全绿。

## 产品目标
- 深度平替本机 MuMu 模拟器（参考 `D:\IDE\MuMu`），**UI 与操作尽量逐项一致**，但**不含广告、会员、推广弹窗**。
- 桌面控制统一使用 `agent-desktop`（github.com/vercel-labs/agent-desktop），不再使用已弃用的 acdesktop。
- MuMu 的逆向结论见 `docs/mumu-reverse-engineering.md`，差距与已对齐项见 `docs/mumu-parity-assessment.md`。
