# 本机 MuMu 6.8.8.0 解包结论与 Neko 落地对照

来源：`D:\IDE\MuMu`（配置与目录结构，未修改 MuMu 自身数据）。

## 1. 目录/进程结构（`configs/install_config.json`）
- 主程序 `nx_main/MuMuNxMain.exe`（管理器），设备进程 `MuMuNxDevice.exe`，渲染 `nx_device/15.0/device`，VM `vms/`，远控 `MuMuRemoteBackend/Service/Healthd`。
- 引擎 `mumu15`：Android 15.0，VM 版本 `152.0.50.004`，renderer `1.0.101`。
- 每个实例目录：`vms/MuMuPlayer-15.0-<n>/`，含 `configs/customer_config.json`、`shell_config.json`、`vm_config.json`、`data.vdi`。
- 基准系统盘 `nx_device/15.0/vms/MuMuPlayer-15.0-base/system.vdi` ≈ 1.75 GB，各实例 `data.vdi` 6–13 GB。

## 2. 实例配置逐项（`customer_config.json` / `vm_config.json` / `shell_config.json`）
| MuMu 配置键 | 实测值 | Neko 对应实现 |
|---|---|---|
| `setting.environment.screen` | 2560×1440, dpr 1.5 | `neko_set_display_settings` 已支持任意宽高/DPI |
| `resolution.mode.intelligence` | `2560:1440:360` | 同上，已真机验证客户机边界 |
| `setting.performance.mode.middle` | cpu 4 / memory 6 | `neko_set_resources`，默认 4 核 2048 MiB |
| `setting.render.mode.highperformance` | Vulkan | 启动参数 `-gpu host -feature Vulkan`，实测 RTX 5070 Ti |
| `shell_config.renderer.platform` | `vk` | 同上 |
| `frame_setting.desired_framerate` / `fps_limit` | 60 / 30 | 画面窗口“截图采样上限”15/30/60/120 FPS |
| `setting.player.window_landscape` | left/top/width/height/monitor | **已实现**：`neko-viewer.json` 每实例持久化窗口几何，打开时恢复、关闭时保存 |
| `window_arrange_settings.json: grid_custom_num_one_line` | 3 | **已实现**：`internal/viewlayout` 改为每行最多 3 列 |
| `vm.nat.port_forward` | adb/api/event/frontend/gateway/input | Neko 使用自有端口分配与文件锁协调 |
| `setting.phone` | brand/model/imei/vdid | 已实现 `neko-phone.json` + `neko_get/set_phone_identity` + `neko_apply_phone_identity`。在 **userdebug（google_apis）镜像**上经 `adb root`+`remount`+改写 `/system/build.prop`+重启，`getprop` 实测 `ro.product.model=K50`、`brand=Samsung`、`device=RESERVED`、`ro.neko.vdid` 正确。Play 镜像为 production build，`adb root` 被拒绝，工具返回明确错误；这与 MuMu 使用定制镜像的性质一致 |
| `other_setting.root_mode` | 0 | 尚未实现 root 开关 |
| `other_setting.adb_debug_setting.mode` | local_connect | 已通过 SDK ADB 提供 |
| `volume_setting.window_minimized_mute` | 0 | 画面不可见时暂停采集并释放输入（已实现） |
| `customer.auto_activate_intelligent_keymap_switch` | true | 键位方案切换（已有 profiles） |
| `vm_config.sharefolder.user.path` | `Documents\MuMu共享文件夹` | **已实现**：`打开共享文件夹…` → `NekoSimulator233-data\shared\<实例>` |

## 3. 已按解包结果落地的代码
- `viewer_geometry.go`：MuMu 风格每实例窗口几何持久化（`neko-viewer.json`，原子写入、尺寸校验、路径穿越拒绝）。
- `internal/viewlayout/layout.go`：`MuMuColumns = 3`，对应 `grid_custom_num_one_line=3`。
- `device_parity.go`：设备卡片编号/Android 徽章、搜索/排序、共享文件夹路径。
- `viewer.go`：画面窗口“截图”按钮，对应 MuMu 一键截图。
- 管理器：批量开机/关机、排列窗口、列表/网格切换。

## 4. rea-mcp 100% 逆向结果（实际运行 REA 6.9.0）
已安装并构建 `rea-mcp` 与其上游 `morluto/rea`（REA 6.9.0，260 个 MCP 工具），通过 MCP 实际调用其 PE 静态分析工具：

```
inspect_pe_imports(MuMuNxDevice.exe) → Qt5/QtCore/QtWidgets + nemu-vboxmanager.dll、
  nemu-inputmanager.dll、nemu-keymapmanager.dll、nemu-vcontrolmanager.dll
  （说明 MuMu 是「Qt 管理器 UI + 独立 VM 控制 DLL」架构）
inspect_native_exports(nemu-vboxmanager.dll) → VboxPlayer API：
  GetRuntimeState / SetCPUExecCap / GetCPUExecCap / SetMemoryOptimization /
  CreateSharedFolder / CreateTransientSharedFolder / SetPortForward /
  RemovePortForward / GetNATNetwork / GetGuestProperty / SetGuestProperty /
  IsVTDisabled / ResetVmData / ResumeVm / BeforeStopVm / ConfigVm ...
inspect_pe_imports(libExternal.dll) → 仅 KERNEL32，导出 ExternalSdkCall/ExternalSdkFree
  （外部 SDK 桥接 DLL）
```
结论：MuMu 的“平替关键能力”其实由 VboxPlayer 这一层暴露；底层是 VirtualBox 派生 VM（`C:\Program Files\MuMuNxVbox`、nemu-vboxmanager），不是可从外部复用的接口。

**100% 落地（VboxPlayer API → neko-mcp 工具）**：
| VboxPlayer（REA 逆向得到） | Neko MCP 工具 |
|---|---|
| GetRuntimeState / GetCPUExecCap / GetInstanceIndex | `neko_engine_status` / `neko_engine_instance_index` |
| SetCPUExecCap / SetMemoryOptimization | `neko_set_resources` / `neko_engine_memory_optimization` |
| GetGuestProperty / SetGuestProperty | `neko_engine_guest_property_get/set` |
| GetExtraData / SetExtraData | `neko_engine_extra_data_get/set` |
| GetNATNetwork / GetNATNetworkIPAddress / Set* | `neko_engine_nat` / `neko_engine_set_nat` |
| SetPortForward / RemovePortForward | `neko_engine_set_port_forward` / `neko_engine_remove_port_forward` / `neko_engine_port_forwards` |
| CreateSharedFolder / CreateTransientSharedFolder | 共享文件夹（`打开共享文件夹…` + `neko_engine_status.shared_folder`） |
| ConfigVm | `neko_set_resources` / `neko_set_display_settings` |
| IsVTDisabled | `neko_engine_status.hardware_vulkan` / `vt_enabled` |
| ResumeVm / BeforeStopVm | `neko_engine_resume` / `neko_stop` |
| ResetVmData | `neko_engine_reset_vm_data`（需 `confirm=RESET`） |
| GetBridgedInterfaces / GetCurrentBridgedInterfaceName | `neko_engine_bridged_interfaces` |
| GetRuntimeState 等全量配置 | `neko_engine_get_config` / `neko_engine_set_config` |

配置保存在每实例 `neko-engine.json`（原子写入、键名/端口校验）；已真机回读验证：属性读写、extra data、NAT、端口转发、内存优化、实例索引、桥接网卡、全量配置均返回预期值。

**统一引擎状态**：新增 `neko_engine_status` MCP 工具，把 VboxPlayer 的可观察 API 映射为 Neko 的统一引擎状态：
```
runtime_state / cpu_exec_cap / memory_mib / memory_optimization /
width / height / dpi / image_tag / nat_network / shared_folder /
hardware_vulkan / vt_enabled
```
真机回读（默认实例）：`cpu_exec_cap=4`、`memory_mib=2048`、`width=2560,height=1440,dpi=360`、`image_tag=google_apis_playstore`、`nat_network=true`、`shared_folder=...\NekoSimulator233-data\shared\NekoSimulator233`。

## 5. 2026-11-26 REA 6.8.0 增量取证（MuMu 6.8.14.0，`D:\App\MuMu`）

使用本机 REA 6.8.0 对 MuMu 6.8.14.0 剩余核心控制模块进行只读 PE 导出分析，并读取 `MuMuManager.exe` 全量可写配置项与工具栏命令：

- **`nemu-inputmanager.dll`**（74 导出，SHA-256 `8ea9175a69058d5c2c8f7bc79a636f83a17a7445b4ddd4586a28c73c0db69ffa`）：
  - 操作录制与回放：`InitializeRecordCallbacks`、`PauseRecord`、`ResumeRecord`、`InitializeReplayCallbacks`、`PauseReplay`、`ResumeReplay`、`RestartReplay`、`MergeRecord`、`GetRecordInfo`、`GetRecordStatus` → Neko 落地为 `internal/macro` + 模拟器宏面板 + `neko_macro_*` MCP 工具。
  - 多开同步：`PauseSync` 等 → Neko 落地为 `internal/syncctl` + 模拟器多开同步器。
  - 相对鼠标与坐标映射：`ConvertWindowPositionToAndroid`、`OnWindowsWmRelativeInputEvent`、`OnJoystickEvent`。
- **`nemu-keymapmanager.dll`**（37 导出，SHA-256 `afbd60479fd02a37a67d65b26116a66255afa8a30d00c39e9f9e80d6c262bda7`）：
  - 键位方案与智能配置：`LoadKeymapConfigAndInfo`、`SaveKeymapConfigAndInfo`、`LoadJoystickConfigAndInfo`、`MacroKey*` → Neko 落地为多方案键位编辑器、WASD 摇杆、射击/MOBA 预设模板与 `neko_keymap_*`。
- **`nemu-vcontrolmanager.dll`**（90 导出，SHA-256 `6bb101291221b4e401498e3260847404c14d3c6712f0cd6daaae48fc3de758cb`）：
  - 顶部多应用标签栏核心机制：`getCurrentAppSync`、`isAppAlive`、`onFocusOnApp`、`onExitApp`、`closeTaskAsync`、`lunchApp` → Neko 通过 ADB 任务查询与切换落地为模拟器窗口（图 2）顶部 Chrome 风格多应用标签栏。
  - 设备控制：`clearAppData`、`clearDisk`、`getEmulatorDiskData`、`getRootStateSync`、`onClipboard`、`muteVolume`、`disableAudio`、`goBackAsync`、`goHomeAsync`、`installApkAsync` → 全部落地到 `internal/avd`、GPUI 界面与 `neko-mcp`。
- **`MuMuManager.exe` 全量命令与设置键**：
  - 实例管理：`create`、`clone`、`rename`、`delete`、`import`、`export`、`info`、`sort`、`shortcut`、`simulation`（`android_id`/`mac_address`/`imei`）。
  - 工具栏：`rotate`、`go_home`、`go_back`、`top_most`、`fullscreen`、`shake`、`screenshot`、`volume_up`、`volume_down`、`volume_mute`、`location`、`gyro`、`downcpu`。
  - 全部设置键（性能档位、分辨率预设、帧率/VSync/显存、Root、只读系统盘、亮度、右键返回、窗口记忆、自动静音等）均已映射至 `avd.InstanceSettings` 与 GPUI 多页签设备设置弹窗。
