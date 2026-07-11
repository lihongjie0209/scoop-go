# Scoop Go 替代原版 Scoop 审计报告

审计日期：2026-07-11  
审计对象：`D:\code\scoop-go`（Go 版）、`D:\code\Scoop`（PowerShell 原版，commit `b588a06`）、`D:\code\shim-source`（参考 shim，commit `24c107b`）  
Go 版基线：commit `0121aec`

> 修复进展（2026-07-11）：P0-04 已修复并增加恶意 tar 回归测试；P1-01 已修复，重建后的 Go shim 通过参考套件 28/28；P0-02 已加入第一阶段更新回滚（恢复 `current`、shim、快捷方式和环境变量）；P0-03 已增加失败清理并让安装元数据错误中止安装；P0-01 已实现 GitHub Release 查询、平台资产选择、`checksums.txt` SHA-256 校验、启动验证、独立 helper 替换及 `.old` 回滚；P1-03 已实现常见 autoupdate 字段合并、标准版本变量替换、目标文件预下载与 SHA-256 固化，并增加 HTTP 端到端测试。报告主体保留首次审计结论，便于追踪剩余准入项。

## 1. 最终结论

**结论：NO-GO，当前 Go 版不能安全、完整地替代 PowerShell 原版。**

Go 版已经具备相当完整的命令骨架，能够构建，普通单元测试和 `go vet` 均通过；下载、常见压缩格式、manifest 基础解析、环境变量、shim 生成等局部能力已有实现。但“替代原版”所需的升级连续性、失败回滚、shim 格式兼容、安全解压、命令/参数兼容和真实 Windows 端到端覆盖尚未达到发布门槛。

建议把当前状态定义为 **技术预览（alpha）**，只允许在隔离的测试用户和可重建环境试用；不应覆盖现有 `scoop` 命令，也不应迁移生产用户目录。

## 2. 审计范围与验证方法

- 仅使用 `D:\code\Scoop` Git 已跟踪的 PowerShell 文件作为原版基线，排除了该目录内未跟踪的 Go 文件。
- 对比 28 个原版命令、参数、配置项、manifest 字段和安装/更新生命周期。
- 检查 Go 版下载、解压、持久化、PATH、shim、快捷方式、依赖、bucket、缓存和自更新实现。
- 运行 `go build ./...`、`go test ./...`、`go vet ./...`、覆盖率统计。
- 使用独立 `shim-source/test/run-tests.ps1` 对嵌入的 `pkg/shim/shim.exe` 进行黑盒兼容测试。

## 3. 阻断性问题

### P0-01：自更新目标仍是 PowerShell Scoop，无法维持 Go 产品升级链

`pkg/update/update.go:38-96` 的 `SyncScoop` 默认读取 `https://github.com/ScoopInstaller/Scoop`；`cloneScoop` 又以存在 `bin/scoop.ps1` 作为下载成功条件（`pkg/update/update.go:267-281`）。更新完成后只是把当前正在运行的 Go 可执行文件重新做 shim，并没有下载、校验、原子替换新的 Go 二进制。

影响：

- `scoop update` 的“更新 Scoop”语义没有更新 Go 程序本身。
- 新用户安装链、版本回退、签名/哈希验证和二进制原子替换均缺失。
- 继续使用默认仓库会把 Go 版和 PowerShell 版的状态混合在同一个 Scoop 目录中。

替换前必须实现独立的 Go release metadata、架构选择、校验、原子替换、失败回滚和可恢复启动器。

### P0-02：应用更新先破坏旧版本，后安装新版本，失败无回滚

`pkg/update/update.go:207-259` 在新版本安装前执行旧版卸载 hook、删除 shim、快捷方式、环境变量和 `current`；随后才调用 `engine.Install`。下载、解压、hook、installer、持久化或 shim 创建任一步失败，都不会恢复旧 `current` 和相关系统集成。

影响：网络波动、坏 manifest 或 installer 失败即可把原本可用的应用变成不可用状态。这不满足包管理器升级的基本事务性要求。

应改为 staging 安装和验证成功后原子切换 `current`，系统集成采用提交/补偿日志；任何失败都恢复旧版本。

### P0-03：安装流程本身不是事务，错误会留下半安装状态

`pkg/install/install.go:54-166` 创建版本目录后没有统一 rollback。创建 `current` 后的 shim、快捷方式、PS module、PATH、环境变量、persist 和 post-install 任一步失败，之前的副作用都会残留。`saveInstallInfo` 和 `saveManifest` 还会吞掉写入错误（`pkg/install/install.go:740-779`），可能报告成功但缺少状态文件。

此外，被判断为“简单命令”的 hook 执行失败只记录警告并继续（`pkg/install/install.go:342-367`），与安装脚本失败应中止的语义不一致。

### P0-04：tar 符号链接可绕过目标目录边界

`pkg/extract/tar.go:176-202` 只对条目名称做词法前缀检查，却直接创建 archive 指定的符号链接；后续常规文件使用 `os.Create(target)`。恶意 tar 可先创建指向目标目录外的链接，再通过该链接写文件，形成 symlink traversal。

Scoop bucket 和下载归档属于供应链输入；即使正常 manifest 有哈希，第三方 bucket、被接管的上游或显式跳过哈希仍使此问题具有实际风险。应拒绝绝对/越界链接，解析每一级父目录的 reparse point/symlink，并增加恶意 tar 回归测试。

## 4. 高风险兼容性问题

### P1-01：Go shim 未通过参考实现兼容套件

黑盒结果：**26/28 通过，2/28 失败**。

失败项：

1. `%~VAR% expansion in path`：`%SystemRoot%\System32\cmd.exe` 未展开，进程无法创建。
2. `Environment variables from .shim`：`%USERNAME%_suffix` 保持原样。

根因位于 `internal/shimbinary/main.go:269-270`：使用 `os.ExpandEnv`，它不实现 Windows `%VAR%` 语法。当前 shim 因此不能视为 `shim-source` 的 1:1 替代品。实际进程执行逻辑也没有 Go 单元测试；`pkg/shim` 的测试主要覆盖文件生成和解析辅助函数。

### P1-02：命令存在性接近，但 CLI 兼容不完整

原版含 28 个 libexec 命令；Go 版缺少 `virustotal`。多个命令参数缺失或语义变化，例如：

- `download`：原版支持多个 app、`--force`、`--no-update-scoop`；Go 版只接受一个 app，且把 `-k` 定义为 `--no-cache`。
- `install`：缺少原版 `--force`、`--no-update-scoop`、`--no-depends` 等兼容入口（仅提供 `--independent`）。
- `depends`：缺少 `--arch`。
- `info`：缺少 `--verbose`。
- `search`、`cache`、`hold/unhold` 等多 app、通配符、输出格式和退出码尚无系统性契约测试。

脚本和 CI 经常依赖参数名、输出、JSON 结构和退出码；“同名命令可运行”不等于可替换。

### P1-03：版本指定安装没有生成目标版本 manifest

Go CLI 能解析 `app@version`，但只把版本字符串覆盖到 `Engine.Version`，仍使用当前 bucket manifest 的 URL/hash。原版会通过 autoupdate 规则生成指定版本 manifest。结果可能把“当前版本文件”安装到“旧版本目录”，或者因哈希/URL不匹配失败。

### P1-04：仍有多处 PowerShell 和外部命令依赖，README 声明过强

实际代码会调用：

- manifest hooks、installer script、运行进程检查、快捷方式：`powershell.exe`；
- Inno/WiX/MSI：`innounp`、`dark`、`msiexec`/`lessmsi`；
- junction 属性、诊断：`attrib`、`sc`；
- `pkg/gitutil/gitutil.go:72,105` 的部分路径直接调用 `git` CLI。

因此“单文件、无运行时依赖”“Git 完全内置”“除用户 hook 外无需 PowerShell”的表述目前不准确。尤其快捷方式和更新运行进程检查不是用户自定义 hook，却仍要求 PowerShell。

### P1-05：关键包没有任何测试覆盖

本次覆盖率结果中以下关键包为 0%：`internal/shimbinary`、`pkg/db`、`pkg/dependency`、`pkg/diagnostic`、`pkg/gitutil`、`pkg/shortcut`、`pkg/status`、`pkg/update`。`pkg/install` 仅 11.9%，命令层仅 5.5%。

现有 integration workflow 主要循环执行“安装后卸载”，没有验证：更新失败回滚、现有 PowerShell Scoop 目录原位迁移、全局安装、hold、persist 跨版本、PATH/环境变量恢复、快捷方式、bucket 冲突、代理/私有 host、指定旧版本、SQLite 一致性和 self-update。

## 5. 中风险问题与工程缺口

- `go test -race ./...` 在当前环境因 `CGO_ENABLED=0` 未运行；CI 也未配置 race 或其他并发压力测试。
- 嵌入的 `pkg/shim/shim.exe` 与本次从同一源码新构建的二进制哈希不同。Go 构建本身未必可复现，但发布流程应记录源码 commit、Go 版本、构建参数和产物哈希，并在 CI 中对嵌入产物执行参考 shim 套件。
- 当前仅发布 `windows/amd64`，manifest 却支持 arm64；需明确主程序和 shim 的架构策略。
- 原版工作区本身有大量未跟踪 Go 文件。后续自动差异工具必须继续使用 `git ls-files`/`git show HEAD:`，否则会误把这些文件当作 PowerShell 基线。
- README 的“Windows + Unix（same binary）”不成立：Go 二进制不能跨 OS 复用，且核心产品语义依赖 Windows registry、junction、shortcut 和 Windows installer。

## 6. 已通过的验证

| 验证 | 结果 |
|---|---:|
| `go build ./...` | 通过 |
| `go test ./...` | 通过 |
| `go vet ./...` | 通过 |
| `go test -race ./...` | 未执行成功：需要 CGO |
| 参考 shim 黑盒套件 | 26 通过 / 2 失败 |
| 常规 zip-slip 单元测试 | 已存在并通过 |
| 版本比较单元覆盖率 | 84.7% |

这些结果证明代码可编译且部分组件可用，但不能抵消 P0/P1 的替换阻断项。

## 7. 替换就绪门槛

只有同时满足以下条件，结论才可从 NO-GO 调整为灰度 GO：

1. 修复全部 P0，安装和更新具备可证明的原子切换与回滚。
2. Go 自更新完全脱离 PowerShell Scoop 仓库，具备签名/哈希、原子替换、回退和渠道策略。
3. 参考 shim 套件 28/28 通过，并纳入每次 CI；补充 Unicode、超长路径、Ctrl-C/job、GUI/UAC 和 arm64 测试。
4. 建立 CLI 兼容清单，所有原版命令/参数/退出码/机器可读输出逐项决定“兼容、明确弃用或迁移适配”。
5. 用 Main/Extras 的全量 manifest 做静态解析和代表性动态安装矩阵；覆盖指定版本、架构覆盖、persist、installer/uninstaller、hooks、URL/hash 数组和第三方 bucket。
6. 在现有 PowerShell Scoop 数据目录的副本上完成原位迁移、双向回退和至少一个完整升级周期。
7. `update`、`install`、`dependency`、`db`、`gitutil`、`shortcut`、`status`、实际 shim 进程均有失败注入和端到端测试。
8. README 和发行说明准确披露 PowerShell/外部工具依赖、支持架构和不兼容项。

## 8. 建议实施顺序

1. **安全与事务层**：修复 tar traversal；引入 install/update transaction、staging、journal 和 rollback。
2. **产品升级链**：设计 Go 二进制 self-update，不再 clone PowerShell Scoop 作为自身更新。
3. **shim 合规**：以 `shim-source` 套件为规范，修复 `%VAR%` 展开并强制 CI 门禁。
4. **manifest/CLI 兼容层**：实现指定版本 manifest 生成，补齐参数与输出契约。
5. **迁移与灰度**：只读扫描旧安装、生成迁移计划、备份、canary 用户验证和一键回退。

在上述工作完成前，最稳妥的定位是：Go 版作为并行实验性客户端，使用独立的 root/cache/shims 目录，不接管现有 Scoop 安装。
