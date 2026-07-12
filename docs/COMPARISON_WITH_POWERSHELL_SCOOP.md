# Scoop Go vs PowerShell Scoop 对比分析与修复建议

| 项 | 值 |
|---|---|
| 文档日期 | 2026-07-12 |
| Go 版仓库 | `D:\code\scoop-go`（commit `ca985c4`） |
| PowerShell 原版 | `D:\code\Scoop`（commit `b588a06`，Scoop 0.5.3） |
| 对比范围 | 命令面、安装/更新/下载/解压/依赖/配置/shim、测试与发布就绪度 |
| 结论 | **技术预览可用；尚不能安全、完整地替代原版 Scoop** |

> 相关历史文档：仓库根目录 `AUDIT_REPORT.md`（2026-07-11）。本文基于当前源码重新比对，覆盖其后已修复项与仍存缺口。

---

## 1. 项目概览

### 1.1 Scoop Go 定位

Scoop Go 是对 [Scoop](https://scoop.sh) 的 **纯 Go 重写**，目标是：

- 单文件二进制，弱化 PowerShell / 外部工具依赖
- 用 Go 原生能力替换：`git`（go-git）、多连接下载（替代 Aria2）、7z 解压、SQLite 搜索缓存
- 尽量兼容原版 manifest、数据目录与 CLI 习惯

### 1.2 架构对照

| 层次 | PowerShell Scoop | Scoop Go |
|------|------------------|----------|
| 入口 | `bin/scoop.ps1` → `libexec/scoop-*.ps1` | `cmd/scoop/main.go` → Cobra `cmd/*.go` |
| 核心库 | `lib/*.ps1`（~19 个模块） | `pkg/*`（~17 个包） |
| Shim | 预编译 `supporting/shims/*` | `internal/shimbinary` + `//go:embed` |
| 配置 | `~/.config/scoop/config.json` | 同路径，字段大体对齐 |
| 数据目录 | `~/scoop`（apps/buckets/cache/shims/persist） | 同布局，可复用 |
| 自更新 | Git pull Scoop 源码仓库 | GitHub Release 下载 Go 二进制 |

### 1.3 体量粗算

| 侧 | 规模 |
|----|------|
| PS 库 + 命令 | `lib` ~5.4k 行 + `libexec` ~2.7k 行（约 80 个 `.ps1`） |
| Go 核心包 | `pkg` ~10k+ 行（含下载/安装/解压等加厚实现） |
| Go 有测试的包 | `app/bucket/config/download/env/extract/install/manifest/shim/update/version` + `internal/shimbinary` + `cmd/config` |
| Go 无测试的包 | `db`、`dependency`、`diagnostic`、`gitutil`、`shortcut`、`status`、大部分命令层 |

---

## 2. 设计差异（有意为之）

这些差异是产品方向选择，不一定算“缺陷”，但会影响“能否无替原版”。

| 主题 | PowerShell | Go | 影响 |
|------|------------|-----|------|
| 运行时 | 依赖 PowerShell 5+ | 主逻辑为 Go 二进制 | 启动快；hook 仍可能调 PS |
| 多连接下载 | 外部 `aria2c` | 内置 multipart HTTP | 少一个依赖；配置语义不同 |
| Git | 系统 `git.exe` | 优先 `go-git`（部分路径仍可能调 git） | 无 git 环境可 clone/pull bucket |
| 7z | 常依赖 `7zip` 应用 | 原生 `bodgit/sevenzip` | 多数场景不装 7zip |
| 自更新 | 更新 Scoop **源码树** | 更新 **scoop-go 可执行文件** | 与原版共存时路径/语义冲突 |
| 输出 | PowerShell 对象 + Format | 人类可读文本 / 日志 | 脚本/管道兼容差 |
| 跨平台 | 仅 Windows | 代码有 Unix 退化路径 | 产品语义仍是 Windows 包管理 |

---

## 3. 命令面对比

### 3.1 命令清单

| 命令 | PS | Go | 说明 |
|------|----|----|------|
| install / update / uninstall | ✓ | ✓ | 核心生命周期 |
| download / search / list / info | ✓ | ✓ | 查询与预下载 |
| depends / cache / cleanup | ✓ | ✓ | |
| hold / unhold | ✓（分离） | ✓（分离） | 均独立子命令 |
| reset / status / bucket / config | ✓ | ✓ | |
| shim | ✓ | ✓（命令名 `shim`） | 子命令语义有差 |
| export / import / alias | ✓ | ✓ | JSON 形状不同 |
| which / cat / prefix / home / create | ✓ | ✓ | |
| checkup | ✓ | ✓ | 检查项集合略不同 |
| **virustotal** | ✓ | **✗** | Go 仅有配置键，无命令 |
| help | ✓ | ✓ | Cobra 帮助 |
| **init** | ✗ | ✓ | Go 扩展：预加 main/extras 等 bucket |
| **version** | 间接 | ✓ | 显式版本命令 |
| `__self-update-replace` | ✗ | ✓（隐藏） | 二进制替换辅助 |

### 3.2 高频命令参数差异

| 命令 | PS 参数 | Go 参数 | 主要缺口 |
|------|---------|---------|----------|
| **install** | `g i k s u a` | `g i k s a` | 缺 `-u/--no-update-scoop`；**依赖安装未真正接入** |
| **update** | `g f i k s q a` | `g f i k s q a` | 表面齐全，但 **`-i` 未传入 `UpdateApp`**；无参时语义为 Go 自更新 |
| **uninstall** | `g p` | `g p f` | Go 多 `--force`；**只接受 1 个 app** |
| **download** | `f s u a` | `a k s` | 缺 `--force`/`--no-update-scoop`；**单 app**；`-k` 与 PS 语义不同 |
| **info** | `v` | — | 缺 `--verbose` |
| **depends** | `a` | — | 缺 `--arch`，硬编码 `64bit` |
| **shim** | `g` | — | 缺 `--global`；`alter` 语义不同 |
| **alias** | list 时 `v` | — | 缺 verbose |
| **export** | `c` | `c` | JSON schema 不互通 |
| **virustotal** | `a s n u p` | N/A | 整命令缺失 |

### 3.3 多 app / 脚本兼容性

| 命令 | PS 多 app | Go 多 app |
|------|-----------|-----------|
| install / update | ✓ | ✓ |
| uninstall / download / hold / unhold | ✓ | **✗（单 app）** |
| cleanup / reset | `*` / `-a` | 同类支持 |

**脚本替换风险最高的点：**

1. 缺少 `virustotal`
2. `uninstall` / `download` / `hold` / `unhold` 不能批量
3. `download -f` vs Go `--no-cache`（字母与语义均不同）
4. `export`/`import` JSON 不能原样互用
5. `list`/`status`/`search`/`cache` 输出非 PS 对象
6. 无参 `scoop update` 更新的是 Go 二进制，不是 PowerShell Scoop 仓库

---

## 4. 核心子系统差异

### 4.1 安装流水线

两边主干步骤基本一致：

```
校验版本/架构 → 下载 → 解压 → pre_install → installer
→ link current → shims → shortcuts → psmodule → PATH/env
→ persist → post_install → 写 install.json + manifest.json
```

| 能力 | PS | Go | 状态 |
|------|----|----|------|
| 版本目录创建 | ✓ | ✓ | 对齐 |
| 下载 + 哈希校验 | ✓ | ✓ | 基本对齐 |
| 解压 `extract_dir` / `extract_to` | ✓（按 URL 数组配对） | Partial（实现存在，多文件配对需再核） | 需补测试 |
| pre/post install hooks | 完整 PS 会话变量 | PS 脚本 + 简单命令双路径 | Partial |
| installer / keep | ✓ | ✓ | 基本对齐 |
| shim / shortcut / PATH / env_set | ✓ | ✓ | 基本对齐 |
| persist junction/hardlink | ✓ | ✓（含 no_junction、跨盘 copy 回退） | 较好 |
| persist ACL（`persist_permission`） | ✓ | **未见等价实现** | 缺口 |
| show_manifest 交互确认 | ✓ | 配置项有，安装流未完整接入 | 缺口 |
| notes 中 `$dir`/`$persist_dir` 替换 | ✓ | Partial | 需核对 |
| 安装事务 / 失败清理 | 弱 | 有 `createdVersionDir` 清理 + integration rollback | 好于早期版，仍非完整事务 |
| **依赖自动安装** | `Get-Dependency` 后整表安装 | **命令层未调用 `dependency.Resolve`** | **严重缺口** |
| `--independent` | 跳过依赖 | 写入 `Engine.Independent` 但 **未使用** | **无效 flag** |
| admin 校验（`-g`） | 强制 | 仅日志/弱校验 | 缺口 |
| `app@version` | `generate_user_manifest` + autoupdate hash 抓取 | `GenerateUserManifest` + **本地下载后算 SHA-256** | Partial |

**关键缺陷：依赖未接入安装路径**

- 原版：`scoop-install.ps1` 在非 independent 时 `Get-Dependency` 展开依赖再安装。
- Go：`cmd/scoop/cmd/install.go` 直接 `engine.Install`，**从不解析 `depends`**。
- `pkg/dependency/resolver.go` 与 `depends` 命令存在，但与 install 脱节。
- 结果：`scoop install app-with-depends` 可能装出缺依赖的应用。

### 4.2 更新

| 能力 | PS | Go | 状态 |
|------|----|----|------|
| 无参更新 Scoop 自身 | Git pull Scoop 仓库 | GitHub Release 自更新 Go 二进制 | **产品语义不同**（Go 方向正确） |
| bucket 同步 | git pull（PS7 并行） | go-git / 同步 | 基本对齐 |
| SQLite 增量更新 | 基于 git diff 增量 | 常全量 `RebuildAll` | 性能/一致性差 |
| 应用更新顺序 | 先预下载新版本 → 再卸旧装新 | 先挪走 current / 清集成 → 再 Install | 有差异 |
| 更新失败回滚 | 弱 | 有 `*.scoop-go-rollback` + 恢复 shim/快捷方式/env | 已改进 |
| hold 应用跳过 | ✓ | status/hold 字段有，更新路径需确认全覆盖 | 需补测试 |
| `--independent` | 影响依赖更新 | **flag 存在但未传到 UpdateApp** | 缺口 |
| 运行中进程检测 | PS `test_running_process` | 仍调 `powershell.exe` Get-Process | 功能有，依赖 PS |

### 4.3 下载

| 能力 | PS | Go | 状态 |
|------|----|----|------|
| 缓存命名 `app#ver#hash` | ✓ | ✓ | 对齐 |
| 哈希 md5/sha1/sha256/sha512 | ✓ | ✓ | 对齐 |
| Cookie / proxy / gh_token | ✓ | ✓ | 对齐 |
| private_hosts 自定义头 | ✓ | ✓ | 对齐 |
| SourceForge / 特殊 URL | ✓ | ✓ | 有测试 |
| 断点续传 | 有限 | ✓ | Go 较好 |
| 多连接 | aria2 | 内置 multipart | 有意替换 |
| aria2-* 配置项 | 控制 aria2 | **多数配置不驱动内置下载器** | 兼容假象 |
| 哈希失败删缓存 | ✓ | 解压失败会删缓存；哈希路径需保持一致 | 基本有 |

### 4.4 解压

| 格式 | PS | Go |
|------|----|----|
| zip | ✓（.NET 或 7z） | ✓ 原生 |
| 7z / split 001 | ✓ 外部 7z | ✓ 原生 |
| tar / gz / xz / bz2 | 经 7z | ✓ 原生 |
| msi | msiexec / lessmsi | ✓ 外部 |
| innosetup | innounp | ✓ 外部 |
| wix / dark | dark | ✓ 外部 |
| zstd / rar / nupkg 等 | 经 7z 覆盖较多 | **原生覆盖有限** |
| zip-slip 防护 | 有限 | 有单元测试 |
| tar symlink 逃逸 | 风险存在 | 审计后已有修复方向（需回归保持） |

### 4.5 Manifest / Autoupdate

| 能力 | PS | Go | 状态 |
|------|----|----|------|
| 字段结构（url/hash/bin/…） | schema.json | `manifest.Manifest` 结构体 | 大体对齐 |
| 架构覆盖 32/64/arm64 | ✓ | ✓ | 对齐 |
| Flexible string/array | PS 动态 | `FlexibleStrings` | 对齐 |
| `app@version` 生成 | `Invoke-AutoUpdate` 全套 hash 模式 | 合并 autoupdate + 版本变量替换 | Partial |
| 版本变量 `$version`/`$majorVersion`/… | ✓ | ✓ | 基本对齐 |
| autoupdate **hash 抓取**（rdf/jsonpath/xpath/text/github…） | 完整 | **未实现；改为下载后本地 SHA-256** | **行为差异大** |
| 用户 manifest 落盘 `workspace` | ✓ | 内存生成后安装 | 可接受，路径不同 |
| URL / 本地路径 / UNC 安装 | ✓ | Partial（需确认 FindManifest 全路径） | 需补 |

> 影响：`scoop install foo@旧版本` 在 Go 下会下载 autoupdate 模板替换后的 URL，并用**下载内容的 SHA-256** 作为 hash。若上游 URL 不存在或内容与期望不符，行为与原版“按 autoupdate.hash 规则取官方校验和”不同。

### 4.6 依赖解析

| 能力 | PS | Go | 状态 |
|------|----|----|------|
| 拓扑排序 + 环检测 | ✓ | ✓（`pkg/dependency`） | 算法有 |
| installation helpers（7zip/lessmsi/innounp/dark） | ✓ 且排除已安装 | Partial（7z 常跳过；dark 不全；**不检查已安装**） | 缺口 |
| 接入 install | ✓ | **✗** | 严重 |
| 接入 update | ✓ | **✗** | 严重 |
| `bucket/app` 名称处理 | 保留 bucket 前缀 | `Split("/")[0]` **取反风险**（应用名变 bucket 名） | Bug |
| depends 命令 | 完整树 | 本地 DFS，arch 固定 64bit | Partial |

### 4.7 卸载

| 能力 | PS | Go | 状态 |
|------|----|----|------|
| pre/post uninstall | ✓ | ✓ | 对齐 |
| uninstaller | ✓ | ✓ | 对齐 |
| 去 shim/shortcut/PATH/env | ✓ | ✓ | 对齐 |
| purge persist | ✓ | ✓ | 对齐 |
| 多 app | ✓ | ✗ | 缺口 |
| `uninstall scoop` | 调用 `bin/uninstall.ps1` | 提示用脚本 | 可接受 |
| 运行中进程 | 阻止 | `--force` 可跳过 | Go 更灵活 |

`pkg/uninstall` 目录为空，逻辑落在 `cmd/scoop/cmd/uninstall.go`，后续宜下沉到 package 以便测试。

### 4.8 配置

Go `pkg/config` 字段与原版文档大体对齐，包括：

`root_path`、`global_path`、`cache_path`、`proxy`、`scoop_repo`/`scoop_branch`、`gh_token`、`aria2-*`、`use_external_7zip`、`use_lessmsi`、`use_sqlite_cache`、`no_junction`、`default_architecture`、`debug`、`force_update`、`show_update_log`、`show_manifest`、`shim`、`cat_style`、`ignore_running_processes`、`private_hosts`、`hold_update_until`、`update_nightly`、`use_isolated_path`、`virustotal_api_key`、`alias` 等。

Go 额外：`scoop_go_repo`（自更新仓库）。

| 问题 | 说明 |
|------|------|
| 配置项存在 ≠ 被消费 | 如 `virustotal_api_key`、部分 `aria2-*`、`show_manifest`、`cat_style` |
| `hold scoop` 时长 | PS：+1 天；Go：`2100-01-01` 近似永久 |
| 配置 dump | Go “get all” 展示子集，不如 PS 完整 |

### 4.9 搜索 / SQLite

| 能力 | PS | Go |
|------|----|----|
| `use_sqlite_cache` | ✓ | ✓ |
| 缓存字段（name/bin/shortcut…） | ✓ | ✓ |
| 无缓存搜索 | **正则**匹配 name **与 binary** | **子串**匹配 **name only** |
| bucket 更新后缓存 | 增量 | 多为全量重建 |

### 4.10 Shim

| 能力 | PS / shim-source | Go |
|------|------------------|-----|
| `.shim` + `shim.exe` | C# 参考实现 | Go 重写 + embed |
| `%VAR%` 环境变量展开 | ✓ | 已有 `expandWindowsEnv`（相对审计日有改进） |
| 生成 .exe/.cmd/.ps1/.jar/.py shim | ✓ | ✓ 大体 |
| `shim alter` | 交互切换多目标 | 编辑 `.shim` 键值 |
| `--global` | ✓ | ✗ |
| CI 对参考套件门禁 | 原版随发布 | 建议强制 28/28 |

### 4.11 仍依赖外部运行时的场景

README 宣称“除用户 hook 外几乎无 PowerShell/外部工具”，实际仍可能调用：

| 场景 | 依赖 |
|------|------|
| manifest hooks / installer.script | `powershell.exe` |
| 运行进程检测（update/uninstall） | `powershell.exe` |
| Start Menu 快捷方式 | PowerShell COM |
| MSI / Inno / WiX | `msiexec`/`lessmsi`/`innounp`/`dark` |
| 部分诊断 | `sc`、`attrib` 等 |
| 部分 git 路径 | 可能回退 `git` CLI |

建议在 README **如实披露**。

---

## 5. 差异优先级总表

### P0 — 正确性 / 数据安全（应优先修）

| ID | 问题 | 建议修复 |
|----|------|----------|
| **P0-1** | **install 不解析、不安转 depends** | 在 `install` 命令中调用 `dependency.Resolve`；尊重 `--independent`；按拓扑顺序安装；跳过已安装 |
| **P0-2** | **update 的 `--independent` 为死参数** | 将 flag 传入 `UpdateApp`，并实现依赖更新策略与 PS 对齐或文档声明差异 |
| **P0-3** | **依赖解析 `bucket/app` 截取错误** | `strings.Split(dep, "/")` 应取 **最后一段为 app 名**，或完整保留 `bucket/app` 再解析 |
| **P0-4** | 更新/安装事务仍可能留下半状态 | 采用 staging 目录 → 校验 → 原子切换 `current`；journal 记录已做副作用；失败按 journal 补偿 |
| **P0-5** | 解压安全边界 | 保持 zip-slip/tar symlink 拒绝越界；CI 回归恶意归档用例 |

### P1 — 兼容性（替换原版必需）

| ID | 问题 | 建议修复 |
|----|------|----------|
| **P1-1** | 缺 `virustotal` 命令 | 实现或在兼容矩阵中明确“不支持”并给迁移说明 |
| **P1-2** | 多 app 支持不全 | `uninstall`/`download`/`hold`/`unhold` 支持 `cobra.MinimumNArgs(1)` 批量 |
| **P1-3** | `download` 参数与 PS 不一致 | 增加 `--force`（重下）；评估 `-k` 兼容别名；文档说明差异 |
| **P1-4** | 缺 `--no-update-scoop` / 安装前自动自更新策略 | 明确产品策略：要么实现，要么文档写死“Go 不自动自更新” |
| **P1-5** | `app@version` hash 语义不同 | 至少实现常见 autoupdate.hash 模式（url + regex / jsonpath / github）；本地 sha256 作 fallback |
| **P1-6** | `export`/`import` schema 不互通 | 对齐 PS 字段（Name/Version/Source/Info/hold/arch）或提供 `--format=ps|go` |
| **P1-7** | `shim alter` / `--global` | 对齐交互切换语义；支持 global shim 目录 |
| **P1-8** | 搜索无 SQLite 时行为 | 支持 binary/shortcut 搜索；可选 regex 兼容 |
| **P1-9** | 全局安装缺 admin 硬校验 | 与 PS 一致：非 admin 时 `-g` 直接失败 |
| **P1-10** | installation helpers 不全 | 扫描 hook 脚本中的 `Expand-*` 调用；dark/innounp/lessmsi 与已安装检测 |

### P2 — 质量与体验

| ID | 问题 | 建议修复 |
|----|------|----------|
| **P2-1** | 0% 覆盖包过多 | 优先 `dependency`/`db`/`status`/`shortcut`/`diagnostic`/`gitutil` + 命令层契约测试 |
| **P2-2** | 输出非机器可读 | 增加 `--json` 到 list/status/search/info/depends |
| **P2-3** | SQLite 全量重建 | 对齐 PS 的 git diff 增量更新 |
| **P2-4** | 运行进程检测依赖 PS | 用 Windows API / `golang.org/x/sys/windows` 枚举进程路径 |
| **P2-5** | 快捷方式依赖 PS COM | 评估纯 Go 写 `.lnk` 或 COM 封装 |
| **P2-6** | `hold scoop` 永久化 | 改为 +1 天或配置策略 |
| **P2-7** | `show_manifest` / `cat_style` 未完整生效 | 安装前展示 + 确认；`cat` 可选 bat |
| **P2-8** | README 过度承诺 | 修正依赖声明、架构支持、Unix 表述 |
| **P2-9** | persist ACL | 评估是否需要 `persist_permission` 等价 |
| **P2-10** | arm64 发布 | 主程序与 embed shim 的多架构矩阵 |

---

## 6. 建议修复路线图

### 阶段 A — 让“能装对”（1–2 周）

1. **接线依赖安装**（P0-1 / P0-3）  
   - `install`：`Resolve` → 过滤已安装 → 顺序 `Install`  
   - 修复 `bucket/app` 解析  
   - 让 `--independent` 真正短路  
2. **update 依赖与 hold**（P0-2）  
3. **全局 admin 检查 + 多 app uninstall/download**（P1-2 / P1-9）  
4. **补充 dependency 单测与集成测**（装带 depends 的假 manifest）

验收标准：

- `scoop install` 带 `depends` 的 app 会先装依赖  
- `--independent` 不安转依赖  
- 循环依赖有清晰错误  

### 阶段 B — 更新与事务（1–2 周）

1. Staging 安装 + 成功后原子切换 `current`  
2. 更新路径统一 journal 回滚  
3. 进程检测去掉 PowerShell 依赖（或明确 optional）  
4. 失败注入测试（下载失败、解压失败、hook 失败）

验收标准：

- 更新中途杀进程/断网后，旧版仍可运行  

### 阶段 C — CLI / Manifest 兼容（2–3 周）

1. 参数兼容矩阵（download force、info verbose、depends arch、shim global…）  
2. `app@version` hash 模式补齐  
3. export/import 兼容格式  
4. search 行为对齐  
5. 可选实现 virustotal 或文档剔除  

验收标准：

- 用原版脚本中常见参数跑一遍不报“unknown flag”  
- Main bucket 中带 autoupdate 的代表性 app 可装指定版本  

### 阶段 D — 发布就绪（持续）

1. 参考 shim 套件纳入 CI  
2. Main/Extras 抽样端到端矩阵  
3. 与现有 PS Scoop 目录的只读扫描 + 迁移/回退手册  
4. 修正 README；发布 notes 列出已知不兼容  

---

## 7. 替换就绪门槛（建议）

在以下条件全部满足前，建议定位为 **并行客户端 / 技术预览**，不要覆盖系统里的 `scoop` 命令，也不要默认写入生产用户的 Scoop 根目录：

1. P0 全部关闭，依赖安装与更新事务可证明  
2. CLI 兼容矩阵逐项标注：兼容 / 有意不兼容 / 迁移路径  
3. shim 参考套件 CI 全绿  
4. 代表性真实 app：纯二进制、zip、7z、msi、innosetup、带 hook、带 persist、带 depends、指定版本  
5. 在 **复制的** 现有 Scoop 数据目录上完成安装/更新/卸载一周期  
6. README 准确披露外部依赖与不兼容点  

---

## 8. 模块对照速查

| 原版 | Go 对应 | 完成度（主观） |
|------|---------|----------------|
| `lib/core.ps1` | `pkg/app` + `pkg/env` + 散落工具 | 中高 |
| `lib/config`（scoop-config + core） | `pkg/config` | 高（消费不全） |
| `lib/manifest.ps1` + `autoupdate.ps1` | `pkg/manifest` + install 内生成 | 中（hash 抓取弱） |
| `lib/download.ps1` | `pkg/download` | 高 |
| `lib/decompress.ps1` | `pkg/extract` | 中高 |
| `lib/install.ps1` | `pkg/install` | 中（依赖未接、事务不完整） |
| `lib/depends.ps1` | `pkg/dependency` + `cmd/depends` | **低（未接入 install）** |
| `lib/buckets.ps1` | `pkg/bucket` + `gitutil` | 中高 |
| `lib/database.ps1` | `pkg/db` | 中 |
| `lib/versions.ps1` | `pkg/version` | 高 |
| `lib/shortcuts.ps1` | `pkg/shortcut` | 中（PS 依赖） |
| `lib/psmodules.ps1` | install 内逻辑 | 中 |
| `lib/diagnostic.ps1` | `pkg/diagnostic` | 中 |
| `libexec/scoop-update.ps1` | `pkg/update` + selfupdate | 中高（语义变化） |
| `libexec/scoop-virustotal.ps1` | — | 无 |
| shim C# | `internal/shimbinary` | 中高 |

---

## 9. 结论

Scoop Go 已经具备：

- 完整命令骨架与大量核心实现（下载、解压、安装步骤、配置、bucket、shim 生成、自更新链）
- 相对原版在“无 aria2 / 弱化 git / 单文件分发”上的真实优势
- 对早期审计问题的部分修复（自更新、更新回滚、版本 manifest 初版、shim env 展开等）

但仍存在 **替代原版的阻断项**，其中最突出的是：

1. **依赖系统没有接入安装/更新主路径**（实现了库却没用）  
2. **CLI/输出/多 app/部分参数** 与脚本生态不兼容  
3. **`app@version` 与 autoupdate hash 行为** 与原版不同  
4. **测试与端到端覆盖** 仍不足以支撑生产替换  
5. **外部依赖披露** 与 README 宣传不完全一致  

**推荐定位：** 继续作为实验性/并行客户端发展；按第 6 节路线图先打通依赖安装与事务安全，再做 CLI 兼容与迁移，最后才考虑“可替换原版”。

---

## 附录 A：快速复现对比的源码入口

| 主题 | PowerShell | Go |
|------|------------|-----|
| 安装命令 | `libexec/scoop-install.ps1` | `cmd/scoop/cmd/install.go` |
| 安装核心 | `lib/install.ps1` | `pkg/install/install.go` |
| 依赖 | `lib/depends.ps1` | `pkg/dependency/resolver.go` |
| 更新 | `libexec/scoop-update.ps1` | `pkg/update/update.go` |
| 自更新 | 同文件 Sync-Scoop | `pkg/update/selfupdate.go` |
| 下载 | `lib/download.ps1` | `pkg/download/download.go` |
| 解压 | `lib/decompress.ps1` | `pkg/extract/*` |
| Manifest | `lib/manifest.ps1` | `pkg/manifest/manifest.go` |
| Autoupdate | `lib/autoupdate.ps1` | `GenerateUserManifest` + `GenerateVersionManifest` |
| 配置 | `libexec/scoop-config.ps1` | `pkg/config/config.go` |
| VirusTotal | `libexec/scoop-virustotal.ps1` | （无） |

## 附录 B：建议的第一批补丁清单（可直接开 issue）

1. `install`: 调用依赖解析并安装；修复 `Independent`  
2. `dependency`: 修复 `bucket/app` 解析；helpers 与已安装过滤  
3. `update`: 传递 `independent`；确认 hold 跳过  
4. `uninstall`/`download`/`hold`/`unhold`: 多 app  
5. `download`: `--force` 兼容  
6. `install -g`: admin 检查  
7. `GenerateVersionManifest`: 实现至少 1–2 种常见 hash 提取  
8. 测试：`dependency` + install 依赖集成 + update 回滚  
9. 文档：README 依赖披露 + 本对比文档链到发布说明  

---

*本文基于本地源码静态对比生成，未替代完整 Windows 端到端安装矩阵测试。*
