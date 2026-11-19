# MuMu 平替差距评估（本机实测）

日期：2026-11-17。参考对象：本机 MuMu 模拟器 6.8.8.0（`D:\IDE\MuMu`）。

## 本机 MuMu 参考值
- 设备卡片：`Android 15 · 4 核 · 6 GB · 2560 × 1440 · 360 DPI`（多实例 `#0/#1/#2`）。
- 基础系统盘 `nx_device/15.0/vms/MuMuPlayer-15.0-base/system.vdi` ≈ **1.75 GB**；各实例 `data.vdi` 6–13 GB。
- 渲染配置：`platform=vk`，`fps_limit=30/60`，`Adreno (TM) 640`，`joystick_function=true`，键位目录 `keymap_config` 存在但**为空**。
- 形态：多实例管理器（批量操作、排列窗口）+ 定制横屏桌面（游戏中心、TapTap、应用图标、Dock）。

## Neko 已对齐（本机验证）
| 维度 | MuMu | Neko 实测 |
|---|---|---|
| 分辨率/DPI | 2560×1440 @360 | `neko_set_display_settings` 设置后客户机层级 `[0,0][2560,1440]`，DPI 360 |
| 资源 | 4 核 / 6 GB | 默认 4 核 / 2048 MiB，可经 `neko_set_resources` 调整 |
| 渲染 | Vulkan | `neko_start` 报告 `NVIDIA GeForce RTX 5070 Ti` 硬件 Vulkan |
| 多实例 | 多开 + 批量 | 两个实例批量开机同时 running，排列窗口平铺（`x=0` 与 `x=1920`） |
| 桌面形态 | 定制桌面 | 自研 launcher APK：系统壁纸、真实应用图标、游戏中心/每日发现/系统设置卡片、Dock |
| 缩放 | 等比适配 | 画面缩放 适应/铺满/拉伸；agent-desktop 点击切换并回读验证 |
| 键位 | 键位 + 摇杆 | `neko_keymap_*` 工具，含 `neko_keymap_shooter`；真机驱动游戏产生截图变化 |
| 桌面协同 | 宿主控制 | agent-desktop v1.3.1：`status/list-windows`、75 个 `desktop_*` 代理工具；`snapshot/find/click` 实测控制记事本（新增标签页、标题变化）与画面窗口（缩放模式切换） |
| 管理器形态 | 设备卡片 #编号、Android 版本徽章、搜索、排序、列表/网格切换、批量操作、排列窗口 | 已对齐：`#0/#1` 徽章、`Android 14` 徽章、CPU/内存/分辨率/DPI 一行、搜索框、排序下拉、列表/网格切换；agent-desktop snapshot 可见全部控件 |
| 共享文件夹 | MuMu共享文件夹 | 已实现“打开共享文件夹…”按钮，真机点击后创建并打开 `%LOCALAPPDATA%\NekoSimulator233-data\shared\<实例名>` |
| 一键截图 | MuMu 画面工具栏截图 | 已实现画面窗口“截图”按钮，agent-desktop 点击后在 `Pictures\NekoSimulator233\neko-<时间戳>.png` 生成文件（实测 0→1） |

## 性能实测（本机）
- 命令：`mcpctl <neko-mcp> -- bench-screenshot 20`（`cmd/mcpctl`，复用单个 MCP 会话连续截图）。
- 结果：20 帧，平均 **65.81 ms**、最小 47.98 ms、最大 203.74 ms，平均 PNG 110,868 B，等效 **15.20 FPS**。
- 进程：QEMU 常驻工作集约 **4.7 GB**，CPU 累计 1435 s。
- 说明：这是“截图请求→PNG 返回”的端到端吞吐，不等同于客户机游戏帧率；它说明默认 30 FPS 采样上限在当前链路上偏高，实测有效吞吐约 15 FPS。

## 仍未对齐 / 需要用户输入
0. **手机身份伪装（已消除）**：新实例默认优先 `android-35/google_apis`（userdebug）镜像；启动时若配置了 `neko-phone.json`，`StartWithProgress` 会自动完成 `adb root → remount → 首次重启使 overlayfs 生效 → 再次 remount → 写 /system/build.prop → 重启`。真机验证 `neko_start` 后 `getprop` 为 `ro.product.model=K50`、`ro.product.brand=Samsung`、`ro.neko.vdid=parity-vdid-001`。`NEKO_PREFER_USERDEBUG_IMAGE=0` 可切回 Play 镜像，此时注入工具会明确报 ADB root 不可用。
1. **商业射击游戏路径**：未安装和平精英/PUBG。已用 F-Droid 开源射击游戏 Astro Loop 验证键位与摇杆输入链路，但不能外推到商业游戏的反作弊、帧率与压枪手感。需要用户提供合法游戏安装包与账号（或明确授权从官方商店下载）。
2. **音频**：当前画面链路只传输图像，没有 Android→Windows 音频转发与延迟测量。需要决定是否引入音频采集/转发（额外依赖与许可）。
3. **MuMu 键位/数据迁移**：本机 `keymap_config` 目录为空，没有可解析的 MuMu 键位样例；且 README 已声明备份格式不兼容。需要用户提供 MuMu 键位文件样本或导出备份，才能实现并验证迁移工具。
4. **虚拟化内核**：Neko 使用 Android SDK Emulator，不是 MuMu 自研虚拟化；无法在功能对等层面完全替代其驱动/性能优化。
5. **音频（宿主输出、静音策略与链路诊断已对齐）**：QEMU 默认把客户机音频输出到宿主声卡，和 MuMu 一样。已实现 `neko_audio_status`（STREAM_MUSIC 静音/音量/输出设备/活跃轨道）、`neko_set_mute`、`neko_set_volume`、`neko_audio_latency`（AudioFlinger 输出线程 + `getLatency()` 统计），并按 MuMu `window_minimized_mute` 实现画面不可见自动静音。真机回读：`music_index=9`、`music_max=15`、`music_muted` 可切换、`output_device=speaker(2)`；`output_threads=[AudioOut_D/MIXER, AudioOut_15/MIXER]`、`getLatency n=14 ave=0.296935`。该 getLatency 是 AudioFlinger binder 调用统计，不是扬声器到麦克风的端到端音频延迟。

**远程音频转发已用真实 guest PCM 量化**：`internal/audioforward` 提供带序列号/发送时间戳的 PCM 分帧、TCP 中继与 ACK 计时；`cmd/audiolatency -wav` 直接读取 QEMU WAV 后端捕获的客户机 PCM。

真实捕获方法：启动 emulator 时设 `QEMU_AUDIO_DRV=wav`、`QEMU_WAV_PATH=<file>`，在客户机播放 `/sdcard/Music/tone.wav`，QEMU 输出标准 RIFF/WAVE（data 块大小为 0 的流式写法，解析器已兼容）。实测：
```
guest WAV = 1,384,448 bytes
1082 chunks / 1,384,404 bytes (real guest PCM)
avg one-way 0.001527 ms, p95 0 ms, max 1.0039 ms, avg RTT 0.002128 ms
```
这是**真实客户机音频**经过转发链路的延迟测量。仍未做的是声学端到端（扬声器→麦克风）延迟：本机 DirectShow 只有麦克风、无 Stereo Mix 回环设备，且 MuMu 本机场景也只输出到宿主声卡。
6. **自研虚拟化内核（最终接受的范围约束）**：本机 MuMu 的引擎只以二进制形式提供（`nx_device/15.0/device/libExternal.dll` 17 MB、`libRenderer.dll` 14.5 MB、`libGLESv2_nemu.dll` 10 MB、`libgallium_wgl.dll` 45 MB 等），没有对应源码，只有 `licenses.txt`。复刻这类专有 hypervisor/渲染栈需要其源码或同等研发规模，超出本项目可交付范围；Neko 继续使用 Android SDK Emulator 作为底层。这不是凭据阻塞，而是范围判定。
7. **性能目标**：15 FPS 截图吞吐与 4.7 GB 内存是本机基线；未做多开压力测试、GPU 帧时间分解或长时间稳定性验收。

## 最终决策（用户确认：以 MuMu 的实现方式为参考）
- **底层引擎**：MuMu 使用专有自研 VM（本机只有二进制，无源码）。按“参考 MuMu 实现方式”的结论，Neko 采用**同为 QEMU 系的 Android SDK Emulator + 硬件 Vulkan** 作为开源等效路径，并已对齐 MuMu 的外围管理器、批量/排列、键位、分辨率/DPI、资源、身份与共享文件夹。自研 hypervisor 属于 MuMu 的私有研发资产，不是可复用的外部接口，因此不纳入本项目范围。
- **音频**：MuMu 的 `shell_config.json` 只有 `audio_out_hardware_off=0` 与音量/静音设置，音频直接输出宿主声卡，**本身不包含声学回环或端到端延迟测量功能**。因此 Neko 按同一模型实现：宿主输出 + 画面隐藏静音 + `dumpsys` 状态诊断 + QEMU WAV 后端捕获真实 guest PCM，再用 `internal/audioforward` 测转发链路延迟。声学回环不是 MuMu 的功能，故不作为 parity 项。
- 这两条已由用户明确确认“按 MuMu 的实现方式”处理，不再是待办差距。

## API35 Play 镜像修复（已完成）
sdkmanager 的 `x86_64-35_r09.zip` 在解压阶段反复失败。改为直接从 `dl.google.com` 下载同一包（1,762,061,559 B，与 HTTP Content-Length 一致），tar 校验 44 条目后手工解压到 `system-images/android-35/google_apis_playstore/x86_64`，再由 sdkmanager 补齐 `package.xml`。真机创建 `play35test` 成功，实例报告 `image_tag=google_apis_playstore`。默认镜像仍为 userdebug（`NEKO_PREFER_USERDEBUG_IMAGE=1`），可用 `=0` 显式使用 Play 镜像。

## 可复现实验
- `pwsh -File scripts/verify-game-control.ps1`：应用 shooter 键位 → 启动 Astro Loop → 摇杆+点击 → 前后截图 SHA-256 与像素差异比例。
- `mcpctl build/neko-mcp.exe -- bench-screenshot 20`：截图吞吐基准。
- `agent-desktop snapshot/find/click`：桌面 UI 状态变化验证（记事本新增标签页、画面窗口缩放模式切换）。
