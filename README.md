# NekoSimulator233

Windows Android 模拟器原型，使用 Go 1.27 和 MyGo 原生 UI。独立模式调用本机 Android SDK Emulator、系统镜像和 Windows 虚拟化；兼容模式连接本机 MuMu，不包含或复制其引擎。

独立模式需要 Android SDK 的 `emulator`、`platform-tools`、可用的硬件 Vulkan GPU 和虚拟化。新实例优先使用已安装的 Android 34 或 35 `google_apis_playstore/x86_64` 系统镜像；没有时使用 `google_apis/x86_64` 镜像。可在新建时选择已安装的 Android 30 镜像，作为 32 位应用兼容路径。Google Play 镜像需事先通过 SDK Manager 安装，例如 `system-images;android-34;google_apis_playstore;x86_64`；普通 Google APIs 镜像默认不包含 Play 商店。已创建实例不会自动切换镜像。此路径尚未完成真实 Google Play 登录、应用安装或 Vulkan 验证。程序通过 `ANDROID_HOME`、`ANDROID_SDK_ROOT` 或当前用户默认 SDK 目录查找工具；自己的实例数据放在 `%LOCALAPPDATA%/NekoSimulator233-data/avd`，不修改现有 AVD。启动时使用宿主 GPU 并启用 Vulkan，检查 Android 内枚举到的硬件 Vulkan 设备；没有硬件设备时会报告错误。窗口支持鼠标单指点击、拖动和物理键盘直通，并提供返回、主页、最近任务、音量和全屏按钮；目标画面采集频率为 30 帧/秒，画面窗口均不可见时暂停采集，失焦时释放按住的输入。

画面窗口的“截图采样上限”可在 15、30、60、120 FPS 之间实时切换，重新打开窗口后恢复 30 FPS。该设置控制截图请求频率，也限制实验画面流的呈现频率；实际显示帧率取决于模拟器、画面传输、宿主性能及显示器，较高设置可能增加资源占用。

可通过启动前设置 `NEKO_EXPERIMENTAL_STREAM=1` 试用独立实例的事件驱动画面流；画面不可见时停止流，恢复时重新连接，流失败时回退到截图轮询。流持续接收并丢弃无法及时呈现的旧帧，复用最多两个解码画面缓冲区；上游仍可能按更高频率传输和解码。此路径目前只通过模拟服务测试，尚未完成真实模拟器、游戏和资源占用验证。

主窗口的“资源设置…”可调整独立实例的虚拟 CPU 核数和 Android 客户机内存；关闭独立实例后保存，下次启动生效。新实例默认 4 核、2048 MiB；已有实例沿用当前配置，直到主动保存新设置。设置保存在实例目录中，不修改其 Android 数据盘；项目的 `.nekobak` 备份会保留这些值。客户机内存设置不等于宿主实际内存占用，过低可能导致应用运行不稳定。

独立实例启动后，可在主窗口选择“安装 APK…”安装本地 APK；已有同名应用时安装会拒绝覆盖。需要升级已有应用时使用“更新 APK…”，Android 仍会检查包签名和版本。安装结果显示在主窗口，不影响 MuMu 实例。

微信和抖音不默认安装。独立实例启动后，可通过主窗口中的对应官方下载按钮在 Android 浏览器打开官方页面，再由用户完成下载和安装；也可使用 Agent 工具 `neko_open_optional_app_download`，参数 `app` 为 `wechat` 或 `douyin`。打开页面不代表下载、安装或应用兼容性验证成功。

新建独立实例首次启动后，会在后台尝试从 TapTap 官方下载入口安装 Android 客户端；画面可以继续使用。实例停止、网络失败或缺少 Android SDK Build Tools 时保留待重试状态，下次启动重试，也可使用“安装 TapTap…”或 Agent 工具 `neko_install_taptap` 手动重试。既有实例和从备份恢复的实例不会自动安装。程序限制下载来源和大小，使用 `aapt` 检查包名，不覆盖已安装应用；下载计算的 SHA-256 用于标识本次文件，并非发布者签名认证。点击“刷新实例”或调用 `neko_taptap_setup_status` 可查看首次准备状态，该状态不是当前安装状态。自动安装流程尚未完成真实应用验收。

主窗口可新建和选择多个独立实例。名称使用英文、数字、点、下划线或连字符；新建时不会覆盖已有实例或不完整目录。启动、关闭、资源设置与 APK 安装作用于当前选中的实例；多个实例各有独立的 Android 数据和资源设置。运行状态是上次检查时的进程状态，可以通过“刷新实例”重新检查。多开会增加宿主资源占用。

Android 11（API 30）x86_64 系统镜像可供部分 32 位 ARM 游戏使用；Google 也记录了部分 ARMv7 程序可能不兼容，实际运行和 Vulkan 支持仍取决于镜像、驱动和应用。若 SDK 缺少该镜像，可在 SDK Manager 安装 `system-images;android-30;google_apis;x86_64`。新系统选择只用于创建新实例，不会升级、降级或修改已有实例的系统镜像。参考 [Android Emulator 官方版本说明](https://developer.android.com/studio/releases/emulator)。

实例运行后，可选择“系统兼容信息”查看 Android API、系统报告的 ABI 列表和 Native Bridge 属性。属性信息并不证明具体游戏能够运行；安装 APK 时若系统报告 ABI 不匹配，程序会给出相应提示。

“备份选中实例…”可将已关闭的独立实例导出为 `.nekobak`；恢复时先填写尚不存在的新实例名称，再选择“恢复备份到新实例…”。备份包含 Android 用户磁盘、实例资源设置以及默认键位和已保存的键位方案，文件**未加密**，请按私人数据保存；旧版备份不包含键位。恢复会检查备份校验值、可用磁盘空间及 Android 系统镜像的类型和版本，已有目标和原有键位文件不会被覆盖。Google Play 镜像备份使用格式 4，旧版程序不能恢复；普通 Google APIs 镜像继续使用格式 3。只支持本项目的备份格式，不支持直接导入 MuMu 的实例或备份。

独立实例窗口的“编辑键位”可将画面位置绑定到键盘：点击画面位置，再按对应按键；“删除键位”选择后按要删除的键。编辑界面也可选择“设置 WASD 摇杆”，点击游戏摇杆中心，再用缩小或放大按钮调整半径；编辑时画面显示键位和摇杆范围，“完成键位”退出编辑。WASD 已有单点绑定时需先删除这些绑定。原有默认实例继续使用 `%LOCALAPPDATA%/NekoSimulator233-data/keymap.json`，其他原有实例分别保存到 `keymaps` 目录；从备份恢复的实例将键位保存在自身目录。其他未绑定按键仍直通 Android。摇杆占用一个触点，可与鼠标和其他绑定键同时使用；失焦或画面尺寸变化时释放输入。尚无按键组合。GUI 与 Agent 对同一实例的触屏手势互斥，忙时会返回具体原因。

在键位编辑界面输入名称并点击“保存为新方案”，可复制当前单点键位和摇杆配置，再分别修改和手动切换；不同实例的方案彼此隔离。原有键位文件继续作为“默认”方案，方案名称重复时不会覆盖。重新打开画面时选择默认方案，当前尚不支持按游戏自动切换。

兼容模式：启动 MuMu 后运行程序，或将 `NEKO_MUMU_ROOT` 指向 MuMu 安装目录。已启动的 MuMu 实例可在自己的画面窗口打开；停止的实例先由 MuMu 启动，待 Android 启动后刷新。兼容模式的图形后端由 MuMu 配置决定。

MuMu 实例旁的“检查迁移条件”会查询厂商报告的实例状态和 Android 版本，并检查对应 Android SDK 的 `system.img` 文件是否存在；本程序不直接读取用户数据盘，也不请求导出、启动或关闭实例。此检查不验证镜像完整性或数据兼容性，完整 MuMu 数据迁移仍未实现。

## 构建与安装

需要 Go 1.27.3 或更新的 1.27 版本，以及 WiX Toolset 5。

```powershell
pwsh -File scripts/build-msi.ps1 -Version 0.1.28
pwsh -File install.ps1 -MsiPath ./build/NekoSimulator233-0.1.28-x64.msi
```

MSI 按当前用户安装，首次安装和升级使用完整 MSI。可选的本地更新源尚未预配置：确认 MSI 来源可信后，运行以下命令会把本地文件路径、版本及 SHA-256 写入 `%LOCALAPPDATA%/NekoSimulator233-data/update.json`：

```powershell
pwsh -File scripts/configure-local-update.ps1 -MsiPath D:/updates/NekoSimulator233-0.1.29-x64.msi -Version 0.1.29
```

安装版在启动时检查清单；运行期间每 10 分钟尝试检查，只有当前登录会话至少 2 分钟没有输入、没有打开画面或资源设置窗口、没有未完成的实例名称或其他操作时才会启动更新。发现更新后校验文件哈希、MSI 产品标识及版本，准备完成时再次检查会话空闲状态和其他本程序进程，然后启动独立更新进程，退出主程序并静默安装，最后重新打开程序。运行中的其他本程序或 `neko-mcp.exe` 会使更新推迟；安装失败重开后，相同清单在本次运行中不自动重复安装，可修改清单或重新启动程序重试。需要 Windows 重启时不会自动重启电脑。暂存目录建在 MSI 同级目录，要求该目录可写且可由 Windows Installer 访问，当前版本会保留暂存文件。此功能只检查本地文件，不下载更新；本地清单及所在目录必须可信，哈希和 MSI 标识不能证明发布者身份。MSI 不包含、修改或迁移 MuMu 引擎和实例数据。

## Agent 控制

安装后的 `neko-mcp.exe` 是通过标准输入输出通信的 MCP 服务，客户端应以进程方式启动它。无需配置网络监听或填写模拟器令牌。可用工具包括 `neko_host_vulkan`、`neko_test_vulkan_guest_frame`、`neko_mumu_migration_preflight`、`neko_list_instances`、`neko_create_instance`、`neko_select_instance`、`neko_export_backup`、`neko_import_backup`、`neko_status`、`neko_device_info`、`neko_start`、`neko_stop`、`neko_get_resources`、`neko_set_resources`、`neko_screenshot`、`neko_tap`、`neko_swipe`、`neko_multi_swipe`、`neko_joystick`、`neko_key`、`neko_hold_key`、`neko_text`、`neko_apps`、`neko_install_apk`、`neko_launch_app`、`neko_ui_hierarchy`、`neko_find_ui` 和 `neko_tap_ui`。`neko_host_vulkan` 查询 Windows 宿主，`neko_mumu_migration_preflight` 只读查询指定 MuMu 实例及本机 SDK 镜像文件；其余实例操作面向项目自己的独立实例。屏幕截图和 UI 层级来自 Android 客户端，应当视为不可信数据。

`neko_test_vulkan_guest_frame` 对已运行的所选独立实例截图，通过内存管道交给隔离子进程，在隐藏 Vulkan 窗口上传并回读比对；仅返回设备和验证结果，不返回或保存截图。可同时传入目标 `width` 和 `height`，测试保持比例、黑边填充的 CPU 最近邻缩放；未指定时使用原始尺寸。它不会启动实例。该诊断会分配画面和 GPU 资源，尚未完成真实实例端到端验证，不代表可见窗口已经接入 Vulkan 或证明游戏性能。

`neko_host_vulkan` 会短暂创建并销毁宿主 Vulkan 图形设备，报告设备、图形队列及驱动声明的交换链和 Win32 外部资源扩展；传入 `{"test_surface":true}` 时还会创建并销毁 64×64 隐藏测试窗口和交换链。传入 `{"test_present":true}` 时会在独立子进程向隐藏交换链提交纯色画面；传入 `{"test_pixels":true}` 还会上传图案并从 GPU 图像回读比对。呈现测试需要驱动支持交换链维护扩展，像素测试还需要交换链支持回读。查询不启动 Android 实例，也不证明屏幕上实际显示了像素或外部资源互操作可用。实验像素路径复用成功帧的资源，但每帧仍同步等待，尚未完成真实多帧和游戏性能验证。源码目录中可用 `go run ./cmd/vulkan-host-probe -pixels` 独立执行像素测试。

实验 Vulkan 窗口呈现器能在窗口尺寸变化或交换链失效时重建资源；GPU 仍在使用资源时会保留资源并报告忙碌。该恢复路径已通过模拟驱动测试，尚未完成真实窗口缩放、最小化和 GPU 故障测试。

可通过启动前设置 `NEKO_EXPERIMENTAL_VULKAN_VIEWER=1` 试用独立进程中的可见 Vulkan 画面窗口。打开模拟器画面后，它通过内存管道接收画面，按窗口大小缩放；实验窗口支持鼠标单指点击、拖动和物理键盘，并使用原窗口已配置的键位及摇杆；键位编辑仍在原窗口操作。失焦或实验进程退出时释放其触点和按键，鼠标捕获丢失时释放触点。关闭原画面窗口会终止实验进程；实验进程失败时原窗口继续运行。此模式会增加进程、像素传输和 GPU 资源占用；CPU 最近邻缩放会复用采样位置和黑边行，不额外保存完整缩放后画面。尚未完成真实 GPU、窗口交互或游戏性能验证，不作为默认渲染路径。

事件驱动画面流报告画面不可见时，实验 Vulkan 窗口会清空旧画面并暂停输入，恢复画面后重新显示；窗口最小化期间收到的清屏会在恢复后处理。该状态切换尚未完成真实 GPU 与模拟器联合验证。

主画面隐藏或最小化后，若实验 Vulkan 窗口仍可见，画面采集继续且可使用该窗口操作。两个窗口都不可见时暂停采集、释放输入并清空旧画面；任一窗口恢复后等待新画面，暂停前正在读取的画面不会作为恢复画面。该流程尚未完成真实窗口和模拟器联合验证。

MCP 默认选择 `NekoSimulator233` 实例；使用 `neko_create_instance` 创建，参数 `api` 可选 30、34 或 35，省略时按默认策略选择 35 或 34。使用 `neko_select_instance` 选择后，其他操作针对所选实例。选择状态属于 MCP 服务进程，连接同一进程的客户端会共享该选择；可为需要隔离选择状态的客户端分别启动服务进程。

为减少连续截图和触控的检查开销，这些操作最多在两秒内复用一次已验证的 Android/Vulkan 状态，每次仍检查实例发现信息和控制端点；显式状态查询及 ADB 操作每次完整检查。若 Android 在此期间重启，短时内仍可能尝试访问尚未就绪的画面，实际调用失败会返回错误。

`neko_find_ui` 和 `neko_tap_ui` 按 Android 无障碍层级查找元素，例如 `{"resource_id":"com.example:id/play"}` 或 `{"text":"开始","package":"com.example"}`。至少指定 `text`、`resource_id`、`description` 之一；多个条件按区分大小写的精确匹配组合。`neko_find_ui` 返回匹配元素及画面范围；`neko_tap_ui` 仅在恰好匹配一个启用的元素时点击其画面内部分的中心。层级读取与点击之间界面可能发生变化；自绘游戏画面也可能不暴露无障碍节点，这些场景仍可使用截图和坐标操作。

`neko_text` 支持可打印 ASCII 和 UTF-8；非 ASCII 输入会临时替换 Android 实例的剪贴板，若其间没有其他修改则恢复之前的纯文本。非文本剪贴板内容无法通过当前接口恢复。`neko_multi_swipe` 支持 2 至 10 指沿直线同步滑动；`neko_joystick` 允许按顺序保持多个方向，单次总时长不超过 20 秒。取消请求时会释放触点；尚未覆盖所有复杂游戏输入。启动和关闭独立实例通过文件锁协调 GUI 与 MCP 进程。

## 当前限制

这是早期原型：独立模式使用 Android SDK Emulator，而非自主实现的虚拟化内核；其 Android Vulkan 命令已在测试实例上执行并回读验证，MyGo 窗口的宿主绘制仍使用 MyGo 的 Windows 渲染后端。兼容模式的启动器和 Android 内部仍由 MuMu 控制，并未全面去除 MuMu 广告；兼容模式的键盘转发尚未在真实 MuMu 游戏中验证。尚未实现 MuMu 使用习惯与功能对等、复杂游戏键位、音频验证、MuMu 数据迁移、远程更新或游戏性能验收。请勿用于替代现有实例的备份。
