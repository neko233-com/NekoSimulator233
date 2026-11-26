# NekoSimulator233 完整开发历史与架构演进整理

本文档完整梳理项目从初始原型到 **Go + GPUI 全功能去广告重构（v0.2.0）** 的所有历史阶段、逆向取证、关键架构决策与处理结论。

---

## 阶段一：初始原型与独立运行内核确立（2026-11-13 ~ 2026-11-16）

### 里程碑提交
- `f0ef016` — 初始 MuMu 实例管理器原型（早期使用 `mygo` 原生 UI 框架）。
- `a00da76` ~ `b658ba8` — 引入独立的 Android SDK Emulator + 硬件 Vulkan 后端（`internal/avd`），通过 gRPC `EmulatorController` 与 ADB 实现独立实例启动、画面采集、触摸/键盘输入、APK 安装及本机 MSI 安装包（WiX 5）。
- `33f10ce` ~ `0c1e4d9` — 建立 MCP 服务端 `neko-mcp.exe`：支持无障碍 UI 层级查询与点击、多实例隔离管理（`%LOCALAPPDATA%\NekoSimulator233-data\avd`）、虚拟 CPU/内存资源配置、`.nekobak` 备份恢复、Android 11（API 30）32 位兼容镜像支持。
- `62f5ac4` ~ `cab9891` — 画面与渲染优化：分片帧零拷贝解码、可切换游戏键位方案与 WASD 摇杆、事件驱动画面流（`NEKO_EXPERIMENTAL_STREAM`）、截图采样率动态调节（15/30/60/120 FPS）、宿主独立 Vulkan 交换链/上传/黑边/GPU 缩放探针（`internal/vulkan`）。
- `4899b24` ~ `a67f45f` — 应用生态与 SDK 运维：Google Play 镜像自动准备与进度取消、TapTap 官方下载与 `aapt` 包验签安装、分包 Split APK 原子安装、单文件导入导出、专用 JDK 配置脚本（`scripts/configure-jdk.ps1`）。
- `7ced9ae` — **关键边界决策（Remove competing emulator runtime integration）**：彻底移除对 MuMu 闭源引擎的直接耦合与注入，确立 NekoSimulator233 为独立管理实例数据的纯净开源平替，绝不修改或破坏用户本机 MuMu 数据。
- `666c70b` — 初版设备管理器界面与离线运行时打包脚本。

---

## 阶段二：MuMu 6.8 逆向取证与功能对等推进（2026-11-16 ~ 2026-11-19）

### 逆向取证（只读静态分析）
- **基线取证（`docs/reverse-engineering/mumu-15-baseline.md`）**：
  - 使用本地 `REA 6.8.0` + `Ghidra 12.1.4` 对 `nx_device/15.0` 核心 DLL 及 `system.vdi` 中 guest Vulkan 驱动（`vulkan.kona.so`）与内核模块（`vaddress.ko`，PCI ID `1c87:1074`）进行只读静态分析，厘清了 `external_renderer_ipc.dll`（`ncalrpc` + 共享内存）、`libRenderer.dll`、`VaddressDevice.dll`（MMIO2 控制区）的边界。
- **架构解包（`docs/mumu-reverse-engineering.md`）**：
  - 解包 `D:\App\MuMu`（6.8.14.0）目录结构、`customer_config.json` / `vm_config.json` / `shell_config.json` 与 `MuMuManager.exe` 全量 CLI 协议。
  - 使用 REA 分析 `MuMuNxMain.exe`、`MuMuNxDevice.exe`、`nemu-vboxmanager.dll`、`nemu-inputmanager.dll`（74 导出：操作录制/回放/合并、多开同步、相对鼠标）、`nemu-keymapmanager.dll`（37 导出：键位方案与宏脚本）、`nemu-vcontrolmanager.dll`（90 导出：运行中 App 标签追踪、清理数据/磁盘、Root 状态、剪贴板、音量静音）。
- **核心架构结论**：
  - MuMu 本质上是 **「双窗口外壳（管理器 + 设备播放器）+ 控制管理层（VboxPlayer / InputManager / KeymapManager / VcontrolManager）+ 私有 VirtualBox 派生 VM」**。
  - 底层闭源 hypervisor 为厂商私有二进制，无源码；开源等效路径统一采用 **Android SDK Emulator（QEMU + 硬件 Vulkan）**，并在控制层、UI 层与客户机桌面层实现 100% 去广告全功能对等。

### 提交 `1b527b0` 落地内容
- 新增批量开机/关机与 3 列平铺排列窗口（`batch.go`、`internal/viewlayout`）。
- 新增窗口几何每实例持久化（`viewer_geometry.go`）、共享文件夹、一键截图、设备缩略图、搜索/排序/列表网格切换。
- 新增手机身份伪装（`internal/avd/phone_identity_windows.go`）并默认启用 userdebug 镜像以支持 `build.prop` 注入。
- 新增 VboxPlayer 对等引擎控制（`internal/avd/engine_config_windows.go` + `internal/agent/engine_control.go` / `engine_status.go`）。
- 新增音频状态/延迟诊断、最小化自动静音与 PCM 中继测试（`internal/avd/audio_windows.go`、`internal/audioforward`）。
- 新增 `agent-desktop` 桌面协同桥接与 `rea-mcp` 风格透明 stdio 代理（`internal/desktopctl/proxy.go`）。
- 新增自研 Android 桌面 APK 源码（`launcher/`）与轻量分离运行时下载器（`internal/runtimeinstall`）。

---

## 阶段三：Go + GPUI 彻底重构与全功能去广告落地（2026-11-26，v0.2.0）

### 用户确立的永久规范
1. **每次完成后必须本地构建并静默安装最新 MSI 到本机**。
2. **彻底禁止使用 GitHub Actions**，全部流程本地化。
3. **允许完全重构不留备份**，彻底淘汰 `github.com/egoist/mygo`，以 **Go + GPUI (`github.com/moriyoshi/gpui-go`)** 作为唯一原生 GPU 加速 UI 入口。
4. **严格区分双窗口形态并 100% 去广告**：
   - **图 1：入口管理器**（清爽浅色风、左导栏、顶部工具栏、带 `#0/#1/#2` 实时缩略图与规格胶囊的设备卡片、创建/批量/排列/完整多页签设备设置）。
   - **图 2：模拟器窗口**（Chrome 风格顶部多应用标签栏 + 紧凑集成工具栏 + 100% 去广告纯净 Android 视口 + 完整键位/宏录制/多开同步/定位/摇一摇/旋转/拖拽安装/剪贴板同步/置顶全屏）。
5. **MCP 工具链完整协同**：支持 `neko-*` 全量控制工具以及 `agent-desktop`、`rea-mcp`、`ghidra-mcp`、`craft-anything` 生态检测与透明代理。

### 重构核心变更
- **UI 层完全重写为 Go + GPUI**：
  - 移除 `github.com/egoist/mygo` 与 `third_party/mygo`，引入 `github.com/moriyoshi/gpui-go v0.0.5`（Apache-2.0，`CGO_ENABLED=0`，`-tags gpui_prebuilt`）。
  - 实现高性能零压缩 BMP 帧编码通道将 Android 实时画面直接推入 GPUI 纹理，结合 Win32 窗口管理（排列平铺、几何记忆、窗口置顶、工作区查询）与嵌入式矢量 SVG 图标库。
- **补齐 MuMu 6.8 全部缺失功能**：
  - 实例完整生命周期：新建、克隆（copy）、重命名、删除、开机、关机、重启、重置数据、磁盘占用统计与清理、桌面快捷方式。
  - 全量 MuMu 设备设置：性能档位（低/中/高/自定义 CPU+内存）、显示预设（手机/平板/宽屏/自定义分辨率+DPI+帧率上限+VSync+显示帧率+自动旋转+固定窗口）、机型伪装（热门机型预设+一键随机+IMEI/VDID/MAC/AndroidID）、网络（NAT/端口转发/桥接网卡）、系统高级（Root 开关、ADB 调试、系统盘只读、内存优化、独显优先、右键返回、最小化静音、退出确认）。
  - 模拟器高级工具：顶部实时运行应用多标签页（切换/关闭应用）、操作宏录制/回放/倍速/循环（`internal/macro`）、多开窗口同步器（`internal/syncctl`）、虚拟定位 GPS、重力感应/摇一摇、横竖屏旋转、亮度调节、双向剪贴板同步、拖拽 APK 安装与文件传输。
- **客户机纯净无广告桌面升级**：
  - 重构 `launcher/src/com/neko233/launcher/LauncherActivity.java`，彻底删除“游戏中心/每日新发现”等推广卡片，提供纯粹暗色壁纸、本地应用过滤搜索、自适应应用网格与无广告 Dock。
- **通用 MCP 生态代理（`internal/mcpproxy`）**：
  - 在 `neko-mcp.exe` 统一支持 `agent-desktop`、`rea-mcp`、`ghidra-mcp`、`craft-anything` 透明代理，并新增 `neko_mcp_ecosystem` 工具报告本机逆向与自动化工具链状态。
