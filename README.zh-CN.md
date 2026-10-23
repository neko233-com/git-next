# git-next

开发工具采用 [Rust 验收 + Go 工具链](docs/GO_TOOLING.md)。Go 接替自有 Python 构建、打包和文档工具；native 构建、MSI 和迁移能力保留到替代实现通过等价验证，迁移审计如实报告剩余 Python 文件。

Rust CLI 的合并/tag 门禁统一为 [git-next-accept](docs/GIT_NEXT_ACCEPTANCE.md)：L0/full/network 分层、JSON 报告，Windows/macOS 无需 Python；发布分支必须通过 full。Python CLI 套件已立即归档到 `tests/legacy_cli/` 并禁止执行，聚合 wrapper 已移除。native 产品包、安装器和 GUI 仍按各自范围验收。

当前发布版本是 1.0.2。Linux x64 是 Debian 12 构建，安装后默认 `git` 就是 git-next。Windows x64 与 ARM64 的机器版和用户版 MSI 会装进官方 Git 目录。其中的 git 程序是已验收的 0.3.0 引擎，产品身份改为 1.0.2，旧命令桥接则是重新链接的 CRT-free 程序。1.0.1 已修正 HTTPS、FTP、FTPS；1.0.2 保留这个修正并补上 Windows 包。Windows 终端组件仍可参考 [Git-Terminal 0.2.0](https://github.com/neko233-com/git-terminal/releases/tag/v0.2.0) 和[组件安装说明](docs/TERMINAL.md)。

[English](README.md) · [Git 指南](docs/GIT-GUIDE.md) · [HTML 手册](docs/site/index.html) · [为什么选 git-next](docs/WHY-GIT-NEXT.md) · [交付进度](docs/DELIVERY_STATUS.md) · [文档站](https://neko233-com.github.io/git-next/)

git-next 的客户端用 Rust 实现命令、仓库读写和传输。行为对照 [Git 官方文档](https://git-scm.com/docs)，对象、refs 和 index 仍是 Git 仓库格式。程序不链接、不调用本机的原生 Git。已实现的命令见 [兼容性说明](docs/COMPATIBILITY.md)；`complete_git_parity` 在文档里的命令都对齐之前保持为 false。现有仓库、Git 配置和凭据助手保留原有契约。

0.3.0 已通过最终 18 项本地正确性证据和成对批量性能门禁；本机已卸载原版并接管默认 Git，正式发布及 10/10 完成记录见[交付进度](docs/DELIVERY_STATUS.md)。Windows 默认采用已验证的串行 index/grep 读取，关闭 fscache/fsmonitor/preload；大型仓库全量扫描仍有明确的延迟边界，详见[性能记录](docs/PERFORMANCE.md)。

## 原生安装包

从 [GitHub Releases](https://github.com/neko233-com/git-next/releases) 获取最终验收包。Windows MSI 内含完整自有运行时，无需安装 Rust 编译器。候选构建产物尚不代表获准接管本机。

| 平台 | 安装包 | 环境范围 |
| --- | --- | --- |
| Windows x64 / ARM64 | 机器级或用户级 MSI | 机器级安装需要管理员权限 |
| Linux x64 / ARM64 | 含 install.sh 的完整 tar.gz | Debian 12 / glibc 2.36 或兼容系统 |
| macOS Apple Silicon | 含 install.sh 的完整 tar.gz | macOS 15.5+；按用户要求通过 Dockerfile_mac 检查打包、Mach-O 和 shell |

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

独立正确性验证并行执行，Windows Installer 事务串行执行，报告保留真实耗时和失败结果。性能采用独立成对批量测量，根据实际 median、p95 和主进程内存给出结论，不承诺所有场景都更快。[构建](docs/BUILDING.md) · [性能](docs/PERFORMANCE.md) · [兼容性](docs/COMPATIBILITY.md)。

原生核心遵循 GPL-2.0-only，并提供依赖许可证及对应源码包。[源码与许可](docs/CORRESPONDING_SOURCES.md)。本仓库不使用 GitHub Actions。

## Rust 包

`go -C tools/devtool run . package-rust --platform windows-x64` 打出一个 zip，里面只有 Rust 写的 `git`、LFS 入口和 Git-Terminal。包里没有 MinGW、Bash、Perl、Python，也没有 Git for Windows 的任何文件。解压后运行其中的 `install.ps1`，程序会装到 `%LOCALAPPDATA%\Programs\git-next`，不会改已注册的官方 Git 目录。说明见[安装](docs/INSTALLATION.md)。

## Windows 一键安装

下载正式发布的 `install.ps1` 后执行 `powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\install.ps1`。**默认自动替换已注册的官方 Git for Windows**，保留配置、登录资料和仓库，并记录受保护的回滚事务。`-Plan` 仅查看选择，不作修改。离线 MSI/TLS 材料与校验方式见[安装说明](docs/INSTALLATION.md)。

Windows MSI 同时安装独立 Rust + GPUI Fast [Git-Terminal](docs/TERMINAL.md)，支持标签页、Unicode/IME、复制粘贴、历史滚动及自有 Bash、PowerShell/CMD 配置。它替代 Git Bash 桌面入口，不添加 Git GUI 菜单。[内置 LFS 与游戏项目模板](docs/GAME_PROJECTS.md)覆盖 Unity/Tuanjie、Unreal，无需另外安装 LFS。

## 源码与历史预览

原生 C 核心遵循 GPL-2.0-only，自有打包/扩展及终端遵循各自声明的许可证。发布附带固定依赖、许可证和对应源码包。本项目不使用 GitHub Actions。Gitoxide/Russh Rust 预览保留为历史源码；[固定旧修订中的说明](https://github.com/neko233-com/git-next/blob/ecee4ad91367327957d77ff4bdcb9349d8ee0511/README.zh-CN.md)不代表当前安装入口，安装请使用上面的原生指南。
