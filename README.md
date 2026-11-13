# NekoSimulator233

Windows 上的 MuMu 实例管理原型，使用 Go 1.27 和 MyGo 原生 UI。当前依赖本机已安装的 MuMu 引擎，不包含模拟器内核。

启动 MuMu 后运行程序，或将 `NEKO_MUMU_ROOT` 指向 MuMu 安装目录。界面可查询实例并启动或显示实例。显示实例时仍会使用 MuMu 自身窗口，该窗口的广告尚未移除。

## 构建与安装

需要 Go 1.27.3 或更新的 1.27 版本，以及 WiX Toolset 5。

```powershell
pwsh -File scripts/build-msi.ps1 -Version 0.1.0
pwsh -File install.ps1 -MsiPath ./build/NekoSimulator233-0.1.0-x64.msi
```

MSI 按当前用户安装。首次安装和升级都使用完整 MSI；目前自动检查、下载和安装更新尚未实现，也没有配置更新源。MSI 不包含、修改或迁移 MuMu 引擎和实例数据。

## 当前限制

这是早期原型，尚未实现 MuMu 功能对等、广告移除、模拟器升级迁移、自动更新和性能目标。请勿用于替代现有实例的备份。
