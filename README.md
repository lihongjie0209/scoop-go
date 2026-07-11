# Scoop Go

> **A pure Go rewrite of [Scoop](https://scoop.sh) — the Windows package manager.**
>
> **用纯 Go 重写的 [Scoop](https://scoop.sh) Windows 包管理器。**

[![Go Report Card](https://goreportcard.com/badge/github.com/lihongjie0209/scoop-go)](https://goreportcard.com/report/github.com/lihongjie0209/scoop-go)
[![Build & Test](https://github.com/lihongjie0209/scoop-go/actions/workflows/ci.yml/badge.svg)](https://github.com/lihongjie0209/scoop-go/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/lihongjie0209/scoop-go)](https://github.com/lihongjie0209/scoop-go/releases)

---

## English

### What is Scoop Go?

**Scoop Go** is a from-scratch rewrite of the classic [Scoop](https://scoop.sh) package manager for Windows, written entirely in **Go**. It produces a single, self-contained binary with no runtime dependencies — no PowerShell required (except for user-defined manifest hooks).

### Why Scoop Go?

| Feature | PowerShell Scoop | Scoop Go |
|---------|----------------|----------|
| Runtime | Requires PowerShell 5+ | **Single binary** (~25MB) |
| Git ops | Requires `git.exe` | **Built-in** via `go-git` |
| Download | Requires `aria2` for multi-connection | **Built-in** multi-connection downloader |
| Extract | Requires `7z.exe`, `innounp`, `dark` | **Built-in** for zip/7z/tar/gz/xz/bz2 |
| Search cache | Requires .NET SQLite | **Built-in** via pure-Go SQLite |
| Cross-platform | Windows only | **Windows + Unix** (same binary) |

### Quick Start

```powershell
# Download the latest release from GitHub Releases
# Or build from source:
go build -o scoop-go.exe ./cmd/scoop/

# Add a bucket and install an app
.\scoop-go.exe bucket add main
.\scoop-go.exe install 7zip
.\scoop-go.exe install fd
```

### Commands

`scoop install`, `search`, `list`, `uninstall`, `info`, `update`, `status`, `bucket`, `config`, `shim`, `checkup`, `cache`, `cleanup`, `hold`, `reset`, `export`, `import`, `cat`, `which`, `prefix`, `depends`, `home`, `alias`, `create`, `version`, `help`

`scoop update` checks `lihongjie0209/scoop-go` GitHub Releases for a newer Windows binary. It requires a matching entry in `checksums.txt`, validates the staged executable, and retains the previous executable as `.old`. Set `scoop_go_repo` (or `SCOOP_GO_REPO=owner/repo`) to use another compatible release repository.

### Build

```bash
# Build scoop binary
go build -ldflags="-X 'github.com/scoopinstaller/scoop-go/cmd/scoop/cmd.BuildDate=$(date +%Y-%m-%d)' -X 'github.com/scoopinstaller/scoop-go/cmd/scoop/cmd.Version=$(git describe --tags 2>/dev/null || echo "0.1.0")'" -o scoop-go.exe ./cmd/scoop/
```

### Architecture

```
scoop-go/
├── cmd/scoop/         # CLI commands (Cobra)
├── pkg/               # Core packages (17 modules)
│   ├── app/           # Global state, paths, logging
│   ├── config/        # Configuration (~34 keys)
│   ├── manifest/      # Manifest parsing
│   ├── bucket/        # Git bucket management
│   ├── download/      # Multi-connection download engine
│   ├── extract/       # Archive extraction (zip/7z/tar/msi/...)
│   ├── install/       # Full install pipeline
│   ├── shim/          # Shim creation + embedded shim.exe
│   ├── env/           # PATH + registry persistence
│   ├── shortcut/      # Start menu shortcuts
│   ├── dependency/    # Dependency resolution
│   ├── update/        # Self-update + app update
│   ├── version/       # SemVer comparison
│   ├── status/        # Status checking
│   ├── diagnostic/    # Health checks
│   ├── db/            # SQLite search cache
│   └── gitutil/       # go-git wrapper
└── internal/shimbinary/  # Shim binary source (C#-compatible)
```

---

## 中文

### 什么是 Scoop Go？

**Scoop Go** 是用 **Go** 从零重写的经典 [Scoop](https://scoop.sh) Windows 包管理器。它生成单个自包含的二进制文件，无运行时依赖——无需 PowerShell（用户自定义的 manifest hook 除外）。

### 为什么选择 Scoop Go？

| 特性 | 原版 PowerShell Scoop | Scoop Go |
|------|----------------------|----------|
| 运行时 | 需要 PowerShell 5+ | **单个二进制** (~25MB) |
| Git 操作 | 需要 `git.exe` | **内置** `go-git` |
| 下载 | 需要 `aria2` 多连接下载 | **内置**多连接下载 |
| 解压 | 需要 `7z.exe`、`innounp`、`dark` | **内置** zip/7z/tar/gz/xz/bz2 |
| 搜索缓存 | 需要 .NET SQLite | **内置**纯 Go SQLite |
| 跨平台 | 仅 Windows | **Windows + Unix** |

### 快速开始

```powershell
# 从 GitHub Releases 下载最新版本
# 或从源码构建：
go build -o scoop-go.exe ./cmd/scoop/

# 添加仓库并安装应用
.\scoop-go.exe bucket add main
.\scoop-go.exe install 7zip
.\scoop-go.exe install fd
```

### 架构

```
scoop-go/
├── cmd/scoop/         # CLI 命令 (Cobra 框架)
├── pkg/               # 核心包 (17 个模块)
│   ├── app/           # 全局状态、路径、日志
│   ├── config/        # 配置系统 (~34个配置项)
│   ├── manifest/      # Manifest 解析
│   ├── bucket/        # Git Bucket 管理
│   ├── download/      # 多连接下载引擎
│   ├── extract/       # 归档解压 (zip/7z/tar/msi/...)
│   ├── install/       # 完整安装流水线
│   ├── shim/          # Shim 创建 + 嵌入 shim.exe
│   ├── env/           # PATH + 注册表持久化
│   ├── shortcut/      # 开始菜单快捷方式
│   ├── dependency/    # 依赖解析
│   ├── update/        # 自更新 + 应用更新
│   ├── version/       # 版本比较
│   ├── status/        # 状态检查
│   ├── diagnostic/    # 健康检查
│   ├── db/            # SQLite 搜索缓存
│   └── gitutil/       # go-git 封装
└── internal/shimbinary/  # Shim 二进制源码 (兼容 C#)
```

---

## License

MIT
