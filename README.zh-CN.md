# git-next

[English](README.md) · [HTML 手册](docs/site/index.html) · [交付进度](docs/DELIVERY_STATUS.md)

git-next 是从固定源码构建的完整 Git 客户端，提供自身产品身份、跨平台 LF 默认和原生中英文。主要引擎采用完整的 Git 2.54.0 原生命令、仓库和协议实现，不依赖本机原 Git 或 OpenSSH 兜底。现有仓库、Git 配置和凭据助手保留原有契约。

0.3.0 已通过统一本地正确性与批量性能门禁；正式发布和本机实际接管单独跟踪。[交付记录](docs/DELIVERY_STATUS.md)分别记录实现、验证、发布和安装。

## 原生安装包

从 [GitHub Releases](https://github.com/neko233-com/git-next/releases) 获取最终验收包。Windows MSI 内含完整自有运行时，无需安装 Rust 编译器。候选构建产物尚不代表获准接管本机。

| 平台 | 安装包 | 环境范围 |
| --- | --- | --- |
| Windows x64 / ARM64 | 机器级或用户级 MSI | 机器级安装需要管理员权限 |
| Linux x64 / ARM64 | 含 install.sh 的完整 tar.gz | Debian 12 / glibc 2.36 或兼容系统 |
| macOS Apple Silicon | 含 install.sh 的完整 tar.gz | macOS 15.5+；按用户要求通过 Dockerfile_mac 检查打包、Mach-O 和 shell |

参见[安装与卸载](docs/INSTALLATION.md)。Windows 默认 Git 接管在 MSI 验收后通过独立受保护迁移执行，保留仓库、配置和登录资料，并提供验证过的恢复备份。[接管说明](docs/TAKEOVER.md)。

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

## 旧版 Rust 实验预览（历史说明）

已安装的旧预览基于 Gitoxide 与 Russh，是独立 Rust Git 客户端，目标安装命令为 **git**。新仓库默认 main，运行时不调用外部 Git/OpenSSH 兜底。

**当前仍是实验预览，不能完整替代 Git。** 已实现原生本地/HTTPS/SSH push、干净工作区的 `switch` / `checkout`、分支创建、detached HEAD，以及快进 pull 和[双亲三方 merge 子集](docs/MERGE.md)。Push 支持分支与 tag、上游设置、dry-run、原子批次、删除和 force-with-lease；服务器拒绝、并发 tracking 更新与凭据读取均有隔离对照验收。已加入[原生 LFS 指针、缓存、过滤器及 HTTPS 上传下载](docs/LFS.md)，LFS 上传失败会阻止 Git 引用发布。完整 merge/recovery、rebase、LFS/CLI/IDE 和真实既有登录迁移仍未完成，因此只安装独立的 git-next，git 同名预览放在 PATH 外。完整接管目标见 [接管验收清单](docs/TAKEOVER.md)；未逐项通过前不卸载原 Git。

平台目标：Windows x64/ARM64、Linux x64/ARM64、macOS ARM64（不提供 Intel Mac）。编译通过与真实平台运行验收分别记录，不承诺所有操作都更快。

查看 [兼容矩阵](docs/COMPATIBILITY.md)、[安装卸载](docs/INSTALLATION.md)、[构建验证](docs/BUILDING.md) 和 [性能测量](docs/PERFORMANCE.md)。支持安装脚本自动尝试固定官方提交 SHA 的 GitHub 镜像传输；不会修改现有账号、凭据、Git 配置或仓库。安装需要 Rust 1.96+ 和本机编译工具链。

PowerShell：下载并检查 install.ps1 后执行 `./install.ps1`。Unix：下载并检查 install.sh 后执行 `sh install.sh`。新终端运行 `git-next compatibility --json` 查看真实兼容状态。

`git-next --version`、`git-next -v` 和 `git-next version --build-options` 明确显示 git-next 身份；`git-next version --json` 输出产品与引擎信息，`git-next doctor --json` 列出阻止全局接管的具体缺项。版本查询使用直接启动路径，不扫描仓库、不初始化 SSH。

状态扫描根据 index 大小选择 1–8 个工作线程，避免小仓库按全部逻辑 CPU 启动线程。默认 `status` 使用长格式，支持 `status.short`、`status.branch` 和 `advice.statusHints`；短格式及 porcelain 仍可显式选择。长格式可读取 merge、cherry-pick、revert、bisect、rebase 与 am 进行中的常见状态；读取这些状态不代表已实现相应写命令。性能以优化二进制、本机临时仓库和重复测量为准，见 [性能测量](docs/PERFORMANCE.md)。

## 文件与跨平台 LF

原生 `diff` / `show` / `log` 已补充补丁、文件名/统计、路径筛选、重命名、提交格式与日期等能力；对照测试同时检查读取不会修改 `.git`。完整选项和历史遍历兼容仍未完成，见 [读取命令范围](docs/READ_COMMANDS.md)。

状态源码已补充 porcelain v1/v2、分支及 upstream 头、NUL 分隔、重命名原路径、文件模式/对象 ID、七种冲突状态、未跟踪模式与路径筛选。`--ignored[=traditional|matching|no]` 支持查询忽略文件和目录，兼容全局忽略、`.git/info/exclude`、目录折叠与输出分组；`matching` 与不显示未跟踪文件的组合会在写入前拒绝。普通子模块状态区分提交变化、内部修改和未跟踪文件，支持忽略模式。普通短格式使用当前目录相对路径，porcelain/NUL 使用仓库根目录相对路径。当前安装与各平台实际通过的范围见 [验收记录](docs/ACCEPTANCE.md)；完整 IDE/子模块/重命名兼容和系统接管仍待完成。

已加入普通文件的 `restore`、soft/mixed/hard/path `reset` 及引用/reflog 保护，见 [恢复范围](docs/RESTORATION.md)。新仓库与克隆默认 LF；Git 属性与 EditorConfig 共享规范，二进制和明确的 CRLF 例外保留。纯 LF/CRLF 保存通过规范化内容核验，不再单凭文件长度判定修改；真实内容修改仍显示。旧仓库通过 `eol --apply` 配置策略，`add --renormalize` 显式迁移，不自动暂存业务改动。见 [LF 策略与验证](docs/LINE_ENDINGS.md)。

## 原生合并

暂存源码已补充同名文件/目录冲突处理和提交前索引路径检查。`--renormalize` 遇到缺失文件或类型替换会拒绝且保留索引；请先审查并完成这些独立改动，再做换行迁移。实际安装和平台验收仍以 [验收记录](docs/ACCEPTANCE.md) 为准。

普通双亲合并已支持快进选择、无冲突三方合并、简单文本/二进制冲突索引与手动解决、继续及原生中止。提交消息清理遵循已验证的范围；`reftable` 和未知必需仓库扩展在写入前拒绝。新实现的准确二进制与安装验收层级见 [验收记录](docs/ACCEPTANCE.md)，不能据源码能力推断系统默认 Git 已被替换。

## 仓库终端

`git-terminal --cwd 路径` 打开仓库终端。Windows 优先 PowerShell 7，回退 Windows PowerShell，支持开始菜单和可选目录右键入口；Unix 使用本机 Shell。预览客户端独立提供，原 Git 与 Git Bash 保留。它尚不提供完整 Bash/POSIX 模拟，不能把此终端入口等同于完整 Git Bash 替代。

系统配置使用原生文件路径探测，Windows 独立 GCM 支持包保留现有命名助手并使用 git-next 回调。详见 [配置探测](docs/NATIVE_DISCOVERY.md) 和 [凭据支持](docs/CREDENTIALS.md)；真实账号迁移及完整接管仍待验收。

Linux x64 及 ARM64 模拟容器会运行 LF、状态、恢复、读取、工作区、提交、格式保护和 Homebrew 路径等本地验收；每版实际通过的组数及源码/产物对应关系见[最新验收](docs/ACCEPTANCE.md)。macOS 实机、物理 ARM64、真实网络登录和全面性能仍待验收。
