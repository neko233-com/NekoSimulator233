# NekoSimulator233 开发约定与永久记忆（AGENTS.md）

> 本文档为本项目最高优先级约束与永久记忆，所有 Agent 与后续开发会话必须严格遵守。

---

## 一、不可违背的铁律（永久记忆）

1. **每次完成修改后，必须本地构建 MSI 并安装到本机**
   - 严禁只编译 `go build` 而不打 MSI 或不安装本机。
   - 每次交付必须递增语义化版本号（否则 Windows Installer `MajorUpgrade` 不会覆盖已装文件），并执行：
     ```powershell
     $env:PATH += ";$env:USERPROFILE\.dotnet\tools"   # WiX Toolset 5
     $env:GOPROXY = "https://proxy.golang.org,direct" # 确保 gpui-go 预构建模块可拉取
     pwsh -NoProfile -File scripts/build-msi.ps1 -Version <新版本>
     pwsh -NoProfile -File install.ps1 -MsiPath .\build\NekoSimulator233-<新版本>-x64.msi -Quiet
     ```
   - 安装位置：`%LOCALAPPDATA%\NekoSimulator233\`（含 `NekoSimulator233.exe`、`neko-mcp.exe`、`configure-jdk.ps1`、`LICENSE-gpui-go.txt`、`NOTICE-gpui-go.txt`）。
   - MSI 默认不打包 Android 运行时（保持安装包轻量）；只有定制离线包才传 `-WithRuntime`。

2. **彻底禁止使用 GitHub Actions**
   - 后续**严禁**新增、启用、修改或触发任何 `.github/workflows/*.yml`。
   - 所有编译、单元测试、打包、MSI 制作、本机安装与真机/GUI 验收全部在本机 Windows 环境完成。

3. **允许完全重构，不需要保留旧版备份**
   - 代码与 UI 架构允许彻底重构升级，不保留冗余备份文件或废弃兼容层（旧 `mygo` UI 已彻底移除）。

4. **UI 入口强制使用 Go + GPUI（`github.com/moriyoshi/gpui-go`）**
   - 主程序采用纯 Go 入口 + `gpui-go`（Rust GPUI GPU 加速渲染框架，`-tags gpui_prebuilt`，`CGO_ENABLED=0`）。
   - 不再使用 `github.com/egoist/mygo` 或 WebView/Electron。
   - 中文字体显式指定 `Microsoft YaHei UI`，保证 Windows 高 DPI 下清晰渲染。

5. **严格双窗口产品形态（对标 MuMu 6.8，100% 去广告）**
   - **入口管理器（图 1）**：独立主窗口（`NekoSimulator233`），采用清爽浅色现代风格（无任何广告、会员横幅、推广红点、商业弹窗）：
     - 左侧导航栏：`设备` / `远控` / `工具` / `设置`
     - 顶部操作栏：`+ 新建设备`（主蓝按钮）、`批量操作`、`排列窗口`、搜索框、排序切换（名称/状态/系统版本）、列表/网格视图切换
     - 设备卡片：左侧缩略图（带 `#0`/`#1`/`#2` 编号角标、实时预览画面或 `未开机`/启动中遮罩）、设备名 + `• 运行中` 绿胶囊、系统徽章（`Android 15`/`Android 14`/`Android 11`）、规格标签（`X 核`、`X GB`）、分辨率与 DPI（`1080 × 1920 | 480 DPI`）、右侧圆形黑底启动/关机按钮 + `⋯` 更多菜单
   - **模拟器窗口（图 2）**：每个运行中的设备拥有独立的 GPUI 窗口（`<设备名> - NekoSimulator233`）：
     - 顶部 Chrome 风格集成栏：左侧多标签页（主页/设备标签 + Android 实时运行应用标签动态切换与关闭）+ 右侧紧凑工具栏（远控/多开同步、键鼠映射、音量/静音、返回、更多 `⋯` 全功能工具抽屉）
     - 视口主体：纯黑背景 + 高帧率 Android 画面渲染（适应/铺满/拉伸与点击坐标 100% 一致）、支持鼠标点击/拖动/滚轮滑动、右键返回、物理键盘直通、键鼠映射与 WASD 摇杆覆盖层、拖拽 APK 安装与文件导入
     - 客户机桌面（`com.neko233.launcher`）：100% 去除 MuMu 桌面广告（无搜索栏推广词、无横幅广告、无“每日新发现”推广流、无推广红点），仅保留纯净壁纸、本地应用搜索、真实已安装应用网格与实用 Dock。

6. **完全实现 MuMu 模拟器全功能去广告 + MCP 生态协同**
   - 功能覆盖 MuMu 6.8 全量核心能力：多实例（新建/克隆/重命名/删除/开机/关机/重启/批量操作/排列窗口）、性能档位与自定义 CPU/内存、分辨率/DPI 预设与自定义、帧率上限/VSync/显存与 GPU 策略/显示帧率、机型伪装（Brand/Manufacturer/Model/Device/IMEI/VDID/MAC/AndroidID + 热门预设 + 一键随机）、Root 模式与 ADB 调试、NAT/端口转发/桥接网卡列表、共享文件夹、文件导入导出、APK/Split APK 安装更新卸载、双向剪贴板同步、一键截图、横竖屏旋转、屏幕亮度、虚拟定位 GPS、重力感应与摇一摇、键鼠映射+WASD 摇杆+多方案+射击模板、操作宏录制与回放、多开窗口同步器、备份恢复 `.nekobak`、桌面快捷方式、最小化自动静音与音量控制。
   - **MCP 工具生态协同**：
     - `neko-mcp.exe` 暴露全部 `neko_*` 工具，并通过黑盒 stdio 透明代理支持协同接入：
       - `agent-desktop`（`NEKO_DESKTOP_PROXY=1`，`desktop_*`）— Windows 宿主桌面控制
       - `rea-mcp`（`NEKO_REA_PROXY=1`，`rea_*`，本机路径 `%LOCALAPPDATA%\Neko233\ReaTools\rea-6.8.0`）— 二进制逆向与静态/动态分析
       - `ghidra-mcp`（`NEKO_GHIDRA_PROXY=1` / `NEKO_GHIDRA_MCP_CMD`，`ghidra_*`，本机 Ghidra `%LOCALAPPDATA%\Neko233\ReaTools\ghidra_12.1.4_PUBLIC`）— 深度反编译分析
       - `craft-anything`（`NEKO_CRAFT_PROXY=1` / `NEKO_CRAFT_BIN`，`craft_*`）— 洁净室软件检查与复刻验收
     - 提供 `neko_mcp_ecosystem` 工具实时检测本机工具链状态。

---

## 二、每次完成修改后的标准验证流程

1. 静态检查与全量测试：
   ```powershell
   $env:GOPROXY = "https://proxy.golang.org,direct"
   go vet ./...
   go test ./... -p 1 -count=1
   ```
2. 本机构建 MSI 并静默安装：
   ```powershell
   $env:PATH += ";$env:USERPROFILE\.dotnet\tools"
   pwsh -NoProfile -File scripts/build-msi.ps1 -Version <递增版本号>
   pwsh -NoProfile -File install.ps1 -MsiPath .\build\NekoSimulator233-<递增版本号>-x64.msi -Quiet
   ```
3. 真机验证安装版：
   - 启动 `%LOCALAPPDATA%\NekoSimulator233\NekoSimulator233.exe`，确认 GPUI 入口管理器窗口与模拟器窗口可正常打开、中文显示清晰、零广告、控件可点击响应（优先用 `agent-desktop` 或截屏验证）。
   - 验证 `%LOCALAPPDATA%\NekoSimulator233\neko-mcp.exe` stdio 握手与工具列表正常。

---

## 三、历史与文档索引

- 完整演进历史与架构处理记录：[docs/HISTORY.md](docs/HISTORY.md)
- MuMu 6.8.x 逆向取证结论：[docs/mumu-reverse-engineering.md](docs/mumu-reverse-engineering.md) & [docs/reverse-engineering/mumu-15-baseline.md](docs/reverse-engineering/mumu-15-baseline.md)
- MuMu 平替功能对等清单：[docs/mumu-parity-assessment.md](docs/mumu-parity-assessment.md)
- 桌面协同控制桥接设计：[docs/superpowers/specs/2026-11-17-agent-desktop-control-bridge-design.md](docs/superpowers/specs/2026-11-17-agent-desktop-control-bridge-design.md)
