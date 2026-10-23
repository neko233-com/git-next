# git-next

1.0.3 distribution 已完成声明范围内的本地正确性与成对性能门禁。原生安装包采用从固定源码构建的 native C Git 引擎，整合 git-next 身份、LF 策略、语言和安装更新能力，并提供自有运行时；现有仓库、Git 配置、hooks 和凭据助手保留正常契约。Release staging、工作站安装/更新、push 和发布状态单独记录于[交付进度](docs/DELIVERY_STATUS.md)。

[English](README.md) · [Git 指南](docs/GIT-GUIDE.md) · [HTML 手册](docs/site/index.html) · [为什么选 git-next](docs/WHY-GIT-NEXT.md) · [交付进度](docs/DELIVERY_STATUS.md) · [文档站](https://neko233-com.github.io/git-next/)

构建、打包、文档、夹具、性能测量和发布工具统一采用 Go，产品中的 `git-p4` helper 也已迁到 Go。本批逐文件核验退役 115 个 Python 文件，自有工具链和产品 helper 无需 Python 解释器；必要的 PowerShell 与 POSIX shell 脚本保留。30 个历史 CLI 套件保留在 Git 历史 `3609553`，当前树不含可运行的 Python CLI wrapper。[工具链说明](docs/GO_TOOLING.md)。

独立 Rust 客户端继续作为 preview，使用 [git-next-accept](docs/GIT_NEXT_ACCEPTANCE.md) 验收。Windows x64 与真实 macOS ARM64 的 full 各通过 100 个范围化场景，L0 覆盖 39 个命令。Rust 客户端与 native C 发布产品分别验收；原生包、运行时和安装器使用各自门禁。`complete_git_parity` 仍为 false，Rust 场景不宣称逐条等量覆盖全部历史断言。[兼容性说明](docs/COMPATIBILITY.md)。

当前 native 产品证据包括真实 Apple Silicon macOS 15.5 的 51 场景、Linux 两个 Docker 目标各 52 场景，以及每个 POSIX 目标的两路包内 Git/P4 入口检查。Windows ARM64 已有交叉编译、PE 和包静态检查，尚无真实 ARM64 运行验收。Windows 默认关闭 fscache/fsmonitor/preload，index/grep 使用单线程读取。R7 正确性为 16 lane 通过、2 项用户豁免，耗时 249.801 秒；成对性能 5/5 通过，耗时 668.849 秒。Windows x64 用户级/机器级 MSI 实际生命周期各通过 27/25 项，最终批次绑定相同输入复用这些回执；终端豁免与此前失败均明确保留。[验收证据](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-python-retirement-20261023.md) · [默认配置](docs/DEFAULTS.md) · [性能边界](docs/PERFORMANCE.md)。

## 原生安装包

从 [GitHub Releases](https://github.com/neko233-com/git-next/releases) 获取已发布的安装包与校验和。预计的 [v1.0.3 发布页](https://github.com/neko233-com/git-next/releases/tag/v1.0.3) 不表示发布已经发生，源码快照中的后续交付动作仍单独追踪。Windows MSI 内含完整自有运行时，无需安装 Rust 编译器。

| 平台 | 安装包 | 环境范围 |
| --- | --- | --- |
| Windows x64 / ARM64 | 机器级或用户级 MSI | 机器级安装需要管理员权限；x64 有实际生命周期证据，ARM64 为构建/表/静态检查 |
| Linux x64 / ARM64 | 含 install.sh 的完整 tar.gz | Debian 12 / glibc 2.36 或兼容系统 |
| macOS Apple Silicon | 含 install.sh 的完整 tar.gz | macOS 15.5+；真实 ARM64 运行验收及独立包/Mach-O/shell 检查；CPU affinity 约束不支持 |

参见[安装与卸载](docs/INSTALLATION.md)。根目录 `install.ps1` 安装正式原生 MSI 后自动接管已注册的 Git for Windows，机器级安装会请求系统 UAC；保留仓库、配置和登录资料，并提供验证过的恢复备份。[接管说明](docs/TAKEOVER.md)。

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

## 验证与源码

Rust CLI 统一使用 [git-next-accept](docs/GIT_NEXT_ACCEPTANCE.md)，提供 L0/full/network 分层和 JSON 报告；Windows/macOS 同一套 Rust 命令，无需 Python，发布分支要求 full。native C 产品行为及平台生命周期由 Go 工具和必要平台脚本另行验收。参考 Git 仅用于隔离夹具，产品检查拒绝回退到本机已安装的 Git。历史 Python CLI 断言保留在 Git 历史中。

独立正确性验证并行执行，Windows Installer 事务串行执行，报告保留真实耗时和失败结果。性能采用独立成对批量测量，根据实际 median、p95 和主进程内存给出结论，不承诺所有场景都更快。[构建](docs/BUILDING.md) · [性能](docs/PERFORMANCE.md) · [兼容性](docs/COMPATIBILITY.md)。

原生核心遵循 GPL-2.0-only，并提供依赖许可证及对应源码包。[源码与许可](docs/CORRESPONDING_SOURCES.md)。本仓库不使用 GitHub Actions。

## 独立 Rust preview 包

此包交付独立 Rust 客户端；native C 安装包由 Go 工具另行构建、打包和验收。本批 115 文件退役保留了适用的构建、打包、迁移和维护能力，必要平台脚本继续保留。[工具链与迁移证据](docs/GO_TOOLING.md)。

`go -C tools/devtool run . package-rust --platform windows-x64` 打出一个 zip，里面只有 Rust 写的 `git`、LFS 入口和 Git-Terminal。包里没有 MinGW、Bash、Perl、Python，也没有 Git for Windows 的任何文件。解压后运行其中的 `install.ps1`，程序会装到 `%LOCALAPPDATA%\Programs\git-next`，不会改已注册的官方 Git 目录。说明见[安装](docs/INSTALLATION.md)。

## Windows 一键安装

下载正式发布的 `install.ps1` 后执行 `powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\install.ps1`。**默认自动替换已注册的官方 Git for Windows**，保留配置、登录资料和仓库，并记录受保护的回滚事务。`-Plan` 仅查看选择，不作修改。离线 MSI/TLS 材料与校验方式见[安装说明](docs/INSTALLATION.md)。

Windows MSI 同时安装独立 Rust + GPUI Fast [Git-Terminal](docs/TERMINAL.md)，支持标签页、Unicode/IME、复制粘贴、历史滚动及自有 Bash、PowerShell/CMD 配置。它替代 Git Bash 桌面入口，不添加 Git GUI 菜单。[内置 LFS 与游戏项目模板](docs/GAME_PROJECTS.md)覆盖 Unity/Tuanjie、Unreal，无需另外安装 LFS。

## 源码与许可

原生 C 核心遵循 GPL-2.0-only；自有扩展、打包工具和 Rust 终端各自遵循声明的许可证。原生发布包提供固定依赖、许可证材料及对应源码。独立 Rust preview 保留自己的实现与范围化验收报告；正式产品安装请使用上方原生安装说明。本仓库不使用 GitHub Actions。
