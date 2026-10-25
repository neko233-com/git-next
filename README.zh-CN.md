# git-next

**v1.0.4 已完成最终本地门禁，仍为候选版本；最终 stage 和正式发布待完成。** 产品使用从固定源码构建的 native C Git 引擎与自有运行时，为 Unity/UE 多项目限制 pack 缓存、线程与 LFS 并发，并关闭自动维护。仓库配置和显式命令参数保留优先级，Windows 串行安全读取与存储格式保持不变。

最终产物正确性为 **14 通过、4 项用户豁免，239.890 秒**；独立配对性能 **5/5 通过，618.615 秒**，每操作 10 对样本。豁免仅限终端两项及用户级/机器级 live MSI 生命周期，不表示 1.0.4 已发布或已在本机安装。[交付进度](docs/DELIVERY_STATUS.md)。

当前已验证 **31 项默认值、24 项真实行为检查**，Go test/vet/race 通过，Rust preview full 100/100；Windows 多项目 33/33（805 个受跟踪路径、489 次 Git 调用）、大资源 LFS 5/5；真实 Mac native 51 场景和两路 P4，Linux 两个 Docker 目标各 52 场景和两路 P4。Windows ARM64 为交叉编译 PE/包静态检查，不声明 ARM 真机或完整宿主生命周期。四个 MSI 均未签名。计划随发布提供[正确性](https://github.com/neko233-com/git-next/releases/download/v1.0.4/native-correctness.json)、[性能](https://github.com/neko233-com/git-next/releases/download/v1.0.4/native-performance.json)、[终端豁免](https://github.com/neko233-com/git-next/releases/download/v1.0.4/native-terminal-waiver.json)和[MSI 包验证策略](https://github.com/neko233-com/git-next/releases/download/v1.0.4/native-package-validation-policy.json)资产；正式发布前不可作为已上线下载。

[English](README.md) · [Git 指南](docs/GIT-GUIDE.md) · [HTML 手册](docs/site/index.html) · [为什么选 git-next](docs/WHY-GIT-NEXT.md) · [交付进度](docs/DELIVERY_STATUS.md) · [文档站](https://neko233-com.github.io/git-next/)

构建、打包、文档、夹具、性能测量和发布工具统一采用 Go，产品中的 `git-p4` helper 也已迁到 Go。本批逐文件核验退役 115 个 Python 文件，自有工具链和产品 helper 无需 Python 解释器；必要的 PowerShell 与 POSIX shell 脚本保留。30 个历史 CLI 套件保留在 Git 历史 `3609553`，当前树不含可运行的 Python CLI wrapper。[工具链说明](docs/GO_TOOLING.md)。

独立 Rust 客户端继续作为 preview，使用 [git-next-accept](docs/GIT_NEXT_ACCEPTANCE.md) 验收。Windows x64 与真实 macOS ARM64 的 full 各通过 100 个范围化场景，L0 覆盖 39 个命令。Rust 客户端与 native C 发布产品分别验收；原生包、运行时和安装器使用各自门禁。`complete_git_parity` 仍为 false，Rust 场景不宣称逐条等量覆盖全部历史断言。[兼容性说明](docs/COMPATIBILITY.md)。

## 历史 v1.0.3 验收与工作站范围

已记录的 native 产品证据包括真实 Apple Silicon macOS 15.5 的 51 场景、Linux 两个 Docker 目标各 52 场景，以及每个 POSIX 目标的两路包内 Git/P4 入口检查。Windows ARM64 已有交叉编译、PE 和包静态检查，尚无真实 ARM64 运行验收。Windows 默认关闭 fscache/fsmonitor/preload，index/grep 使用单线程读取。R8 正确性为 16 lane 通过、2 项用户豁免，耗时 1,229.320 秒；成对性能 5/5 通过，耗时 649.347 秒。Windows x64 用户级/机器级 MSI 实际重跑通过 27/26 项，没有复用旧 MSI 回执。[R8 本地证据](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-python-retirement-20261023.md) · [默认配置](docs/DEFAULTS.md) · [性能边界](docs/PERFORMANCE.md)。

最终工作站 bootstrap 在 1,075.391 秒完成。独立回读核对全部 10,698 个不可变文件、1,362,711,981 字节一致，检查 10,714 个文件且 Python 项为 0，两路包内 P4 入口通过，SYSTEM updater task 已确认，12 个个人配置文件 0 变化。仅 `terminal-rust-contracts` 与 `terminal-native-desktop` 保持豁免；Mac 运行证据不代表完整宿主安装及更新服务生命周期。退役报告保留源码时点的 pending 状态，后续完成事实另见[发布与工作站证据](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-publication-20261024.json)。

R7 工作站流程在 1,127.534 秒后于完成阶段失败。随后从核验过的备份精确恢复 `package.json` 和 `bin/git.exe`，核对全部 10,696 个声明的不可变文件，检查 10,712 个文件且 Python 项为 0，两路包内 P4 入口通过，12 个个人配置文件保持不变。这是 `release_gate=false` 的恢复证据，不表示完整安装成功或已经满足发布条件。[恢复记录](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-workstation-r7-recovery-20261024.json)。

R8 随后通过受保护的恢复控制和正常 MSI 卸载恢复全部 9,625 个原 Git 文件，移除 MSI 注册与迁移指针，12 个个人配置文件 0 变化。这是旧事务恢复，不冒充正常的 MSI 前快照或精确历史逐文件 ACL 恢复。[R8 恢复边界](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-workstation-r8-legacy-recovery-20261024.json)。

## 原生安装包

五平台 **1.0.4 本地候选包**已通过声明范围内的构建、包检查与最终本地门禁；最终 stage、校验和及 [v1.0.4 正式发布](https://github.com/neko233-com/git-next/releases/tag/v1.0.4)仍待完成，不声明当前已可下载 1.0.4。

| 平台 | 1.0.4 候选安装包 | 已验证范围 |
| --- | --- | --- |
| Windows x64 / ARM64 | 机器级或用户级 MSI | PE/导入、四 MSI 载荷/表、隔离迁移及资源探针；live MSI 生命周期豁免；全部未签名 |
| Linux x64 / ARM64 | 含 install.sh 的完整 tar.gz | Debian 12 / glibc 2.36；每个 Docker 目标 52 native 场景及两路 P4，不冒充 ARM 真机 |
| macOS Apple Silicon | 含 install.sh 的完整 tar.gz | 真 Mac 51 native 场景及两路 P4、Mach-O/shell 检查；不声明完整宿主安装/launchd 生命周期 |

参见[安装与卸载](docs/INSTALLATION.md)和[接管说明](docs/TAKEOVER.md)，仅使用与正式 accepted release 匹配的资产。bootstrap 会在 MSI 写入前保存原 Git 快照；本地门禁检查点不等于 1.0.4 已在本机安装。

**历史 v1.0.3 发布及真实 MSI/工作站证据：**从官方 latest [v1.0.3 发布页](https://github.com/neko233-com/git-next/releases/tag/v1.0.3) 获取安装包与校验和。发布时间为 `2026-10-23T20:44:12Z`，发布源码为 `05dac2a938784ce03a2de219349c4d381a637ab5`。28 个远端资产的哈希全部与本地发布一致，27 个资产列入 SHA256SUMS，SHA256SUMS 与验收证明均已实际公开下载核验。四个 MSI 实际为未签名（`NotSigned`），不声明密码学签名验证。Windows MSI 内含完整自有运行时，无需安装 Rust 编译器。

## 命令与默认行为

```sh
git --version
git version --json
git language get
git language set chinese
git language set english
git language set auto
git --language english status
git update check
```

`git --version` 明确显示 git-next 及兼容基线。常规 Git 命令、底层命令、hooks、签名、LFS、凭据助手、submodule 和 linked worktree 使用完整原生引擎；用户配置的 hooks、filters 和 helpers 保留正常行为。

未显式配置的文本统一使用 LF；仓库属性、二进制/LFS 规则和显式配置保留优先级。新仓库 init 后保持空工作区。`git eol --apply` 可主动建立缺失的共享 LF/编辑器策略，不会自动改写所有已有项目。纯换行变化按规范化内容比较，实际内容和 Unity 序列化变化仍会显示。[LF 策略](docs/LINE_ENDINGS.md)。

语言默认跟随系统，可持久设置或单次覆盖，机器格式不随语言变化。自动更新从官方 GitHub 获取可信版本信息，并发探测官方与镜像下载路径、校验实际包内容；镜像不能决定版本或可信身份。[语言](docs/LANGUAGE.md) · [更新](docs/UPDATES.md)。

Unity／UE 支持独立多仓库，也支持在同一仓库内分别放置项目根目录规则。1.0.4 选定的资源默认值限制 pack 缓存、打包线程及 LFS 并发，并关闭自动维护。在同一 1.0.3 原生 Windows 二进制的配置对照中，四个生成的多资产项目首次并行打包时，整进程树峰值提交内存从约 1519 降至 440 MiB，pack 字节数一致。单个超大资产和重复打包的反例保留，不承诺全面加速。[游戏项目](docs/GAME_PROJECTS.md) · [实测范围](docs/PERFORMANCE.md)。

## 验证与源码

Rust CLI 统一使用 [git-next-accept](docs/GIT_NEXT_ACCEPTANCE.md)，提供 L0/full/network 分层和 JSON 报告；Windows/macOS 同一套 Rust 命令，无需 Python，发布分支要求 full。native C 产品行为及平台生命周期由 Go 工具和必要平台脚本另行验收。参考 Git 仅用于隔离夹具，产品检查拒绝回退到本机已安装的 Git。历史 Python CLI 断言保留在 Git 历史中。

独立正确性验证并行执行，Windows Installer 事务串行执行，报告保留真实耗时和失败结果。性能采用独立成对批量测量，根据实际 median、p95 和主进程内存给出结论，不承诺所有场景都更快。[构建](docs/BUILDING.md) · [性能](docs/PERFORMANCE.md) · [兼容性](docs/COMPATIBILITY.md)。

按用户明确选择的[安装包验证发布范围](docs/GO_TOOLING.md#explicit-package-validation-release-scope)，可用真实静态包、隔离迁移与资源行为探针代替两项本机 MSI 事务前置要求。两项生命周期仍标 `waived`，不冒充实际安装通过；发布资产保留授权、探针和精确包哈希。其它 native CLI、传输、Docker、race 与性能门禁继续执行。

原生核心遵循 GPL-2.0-only，并提供依赖许可证及对应源码包。[源码与许可](docs/CORRESPONDING_SOURCES.md)。本仓库不使用 GitHub Actions。

## 独立 Rust preview 包

此包交付独立 Rust 客户端；native C 安装包由 Go 工具另行构建、打包和验收。本批 115 文件退役保留了适用的构建、打包、迁移和维护能力，必要平台脚本继续保留。[工具链与迁移证据](docs/GO_TOOLING.md)。

`go -C tools/devtool run . package-rust --platform windows-x64` 打出一个 zip，里面只有 Rust 写的 `git`、LFS 入口和 Git-Terminal。包里没有 MinGW、Bash、Perl、Python，也没有 Git for Windows 的任何文件。解压后运行其中的 `install.ps1`，程序会装到 `%LOCALAPPDATA%\Programs\git-next`，不会改已注册的官方 Git 目录。说明见[安装](docs/INSTALLATION.md)。

## Windows 一键安装

v1.0.4 正式发布后取得同版本 accepted `install.ps1`，先用 `powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\install.ps1 -Plan` 查看选择；只有请求安装该正式版本时才去掉 `-Plan`。bootstrap 默认选择已注册 Git for Windows，32 位 PowerShell 先自动转发到原生 64 位再请求 UAC，并在失败时保留受保护的 MSI 前快照路径及 SHA。安装无需 Go、Python 或 WiX。**当前 1.0.4 检查点没有执行 live MSI 生命周期，也没有在本工作站安装 1.0.4。**上文真实 MSI/工作站成功记录属于 1.0.3。参见[安装说明](docs/INSTALLATION.md)。

Windows MSI 同时安装独立 Rust + GPUI Fast [Git-Terminal](docs/TERMINAL.md)，支持标签页、Unicode/IME、复制粘贴、历史滚动及自有 Bash、PowerShell/CMD 配置。它替代 Git Bash 桌面入口，不添加 Git GUI 菜单。[内置 LFS 与游戏项目模板](docs/GAME_PROJECTS.md)覆盖 Unity/Tuanjie、Unreal，无需另外安装 LFS。

## 源码与许可

原生 C 核心遵循 GPL-2.0-only；自有扩展、打包工具和 Rust 终端各自遵循声明的许可证。原生发布包提供固定依赖、许可证材料及对应源码。独立 Rust preview 保留自己的实现与范围化验收报告；正式产品安装请使用上方原生安装说明。本仓库不使用 GitHub Actions。
