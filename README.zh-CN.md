# git-next

**[v1.0.4](https://github.com/neko233-com/git-next/releases/tag/v1.0.4) 已正式发布，为官方 latest。** 产品使用从固定源码构建的 native C Git 引擎与自有运行时，为 Unity/UE 多项目限制 pack 缓存、线程与 LFS 并发，并关闭自动维护。仓库配置和显式命令参数保留优先级，Windows 串行安全读取与存储格式保持不变。

最终产物正确性为 **14 通过、4 项用户豁免，243.565 秒**；独立配对性能 **5/5 通过，640.668 秒**，每操作 10 对样本。豁免仅限终端两项及用户级/机器级 live MSI 生命周期，正式发布已核验；1.0.4 安装运行验收仍为 false。[交付进度](docs/DELIVERY_STATUS.md)。

当前已验证 **31 项默认值、24 项真实行为检查**，Go test/vet/race 通过；Windows 多项目 33/33（805 个受跟踪路径、489 次 Git 调用）、大资源 LFS 5/5；真实 Mac native 51 场景和两路 P4，Linux 两个 Docker 目标各 52 场景和两路 P4。独立的 Go 客户端当前 CLI 验收为 L0 39/39、full 57/100（主要缺口为 SHA-256，见[Go 实现说明](docs/GO_IMPLEMENTATION.md)）。Windows ARM64 为交叉编译 PE/包静态检查，不声明 ARM 真机或完整宿主生命周期。四个 MSI 均未签名。已发布[正确性](https://github.com/neko233-com/git-next/releases/download/v1.0.4/native-correctness.json)、[性能](https://github.com/neko233-com/git-next/releases/download/v1.0.4/native-performance.json)、[终端豁免](https://github.com/neko233-com/git-next/releases/download/v1.0.4/native-terminal-waiver.json)和[MSI 包验证策略](https://github.com/neko233-com/git-next/releases/download/v1.0.4/native-package-validation-policy.json)资产；全部远端摘要及五个匿名实际下载均已核验。

[English](README.md) · [Git 指南](docs/GIT-GUIDE.md) · [HTML 手册](docs/site/index.html) · [为什么选 git-next](docs/WHY-GIT-NEXT.md) · [交付进度](docs/DELIVERY_STATUS.md) · [文档站](https://neko233-com.github.io/git-next/)

Go 1.27.3 是独立客户端、验收器、构建、打包、文档、夹具、性能测量和发布工具（含产品 `git-p4` helper）的唯一实现语言。`Taskfile.yml` 是本地唯一入口：`task verify` 聚合格式、vet、随机顺序测试、语言与契约检查、构建与 L0 验收，`task test-race` 是独立生产门禁。当前树不含 Rust 源码、Cargo 文件或 vendored Rust crate；native C/MSI 运行时作为独立组件保留。必要的 PowerShell 与 POSIX shell 脚本继续保留。[工具链说明](docs/GO_TOOLING.md)。

**Go-only 客户端：**独立客户端（`cmd/git-next`、根 `go.mod`）及其 [CLI 验收器](docs/GIT_NEXT_ACCEPTANCE.md)全部用 Go 实现；Rust 源码、Cargo 文件、Rust 验收器与 vendored Rust crate 已删除。CLI 验收是唯一门禁：L0 39/39、full 57/100，二进制约 11.3 MB（历史 Rust CLI 基线约 22 MiB）。Go 客户端与 native C 发布产品分别验收；原生包、运行时和安装器使用各自门禁。`complete_git_parity` 仍为 false，Go 场景不宣称逐条等量覆盖全部历史断言；历史 Rust 验收报告仅作为旧发布产物的证据保留，不代表当前实现。[兼容性说明](docs/COMPATIBILITY.md)。

发布源码：`06b073593a5765ff4db204f7e454584990868881`；构建代码检查点：`3be06fa5bac1912ec7e77459830de4ebff055355`；native 源树：`845e391da2d52fb9cfb580606507e1e9d791fffd591987a2b64abd9ab06d9155`。全部 **29** 个远端资产的名称、字节数和 SHA-256 一致，**28** 个资产列入 SHA256SUMS。已匿名 HTTPS 实际下载并核对五个资产，包括 Windows x64 用户级 MSI。四个 MSI 实际状态均为 **`NotSigned`**，摘要一致不代表发布者签名验证。[发布完成记录](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-publication-1.0.4.json)。宿主 MSI 生命周期和 1.0.4 工作站安装仍未执行，两项终端门禁保持豁免。

**以下段落保留发布前检查点的原文；其中 pending 仅表示当时状态。**

**保留 r1 检查点与归档布局纠正。** 先前正确性 14 通过/4 豁免（239.890 秒）、性能 5/5（618.615 秒）仍保留在 [Git 历史](https://github.com/neko233-com/git-next/blob/4d61669ddf4276c52fd977f048ccb72218cd7805/docs/DELIVERY_STATUS.json)及原始本地报告中。r1 stage 随后因 Mac 候选包将 package.json 放在产品目录前缀下而拒绝，未发布。r3 在归档根放置相同载荷，保留相同 native core 源身份，并重新实际执行 Mac 51/P4/Python 0；新 r2 矩阵绑定该归档，14 项非豁免正确性及全部 5 项性能均完整重跑。此处纠正包布局，不改变 Git 存储，也不削弱 parser。最终 stage/发布仍待完成，1.0.4 安装运行验收仍为 false。

## 历史 v1.0.3 验收与工作站范围

已记录的 native 产品证据包括真实 Apple Silicon macOS 15.5 的 51 场景、Linux 两个 Docker 目标各 52 场景，以及每个 POSIX 目标的两路包内 Git/P4 入口检查。Windows ARM64 已有交叉编译、PE 和包静态检查，尚无真实 ARM64 运行验收。Windows 默认关闭 fscache/fsmonitor/preload，index/grep 使用单线程读取。R8 正确性为 16 lane 通过、2 项用户豁免，耗时 1,229.320 秒；成对性能 5/5 通过，耗时 649.347 秒。Windows x64 用户级/机器级 MSI 实际重跑通过 27/26 项，没有复用旧 MSI 回执。[R8 本地证据](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-python-retirement-20261023.md) · [默认配置](docs/DEFAULTS.md) · [性能边界](docs/PERFORMANCE.md)。

最终工作站 bootstrap 在 1,075.391 秒完成。独立回读核对全部 10,698 个不可变文件、1,362,711,981 字节一致，检查 10,714 个文件且 Python 项为 0，两路包内 P4 入口通过，SYSTEM updater task 已确认，12 个个人配置文件 0 变化。仅 `terminal-rust-contracts` 与 `terminal-native-desktop` 保持豁免；Mac 运行证据不代表完整宿主安装及更新服务生命周期。退役报告保留源码时点的 pending 状态，后续完成事实另见[发布与工作站证据](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-publication-20261024.json)。

R7 工作站流程在 1,127.534 秒后于完成阶段失败。随后从核验过的备份精确恢复 `package.json` 和 `bin/git.exe`，核对全部 10,696 个声明的不可变文件，检查 10,712 个文件且 Python 项为 0，两路包内 P4 入口通过，12 个个人配置文件保持不变。这是 `release_gate=false` 的恢复证据，不表示完整安装成功或已经满足发布条件。[恢复记录](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-workstation-r7-recovery-20261024.json)。

R8 随后通过受保护的恢复控制和正常 MSI 卸载恢复全部 9,625 个原 Git 文件，移除 MSI 注册与迁移指针，12 个个人配置文件 0 变化。这是旧事务恢复，不冒充正常的 MSI 前快照或精确历史逐文件 ACL 恢复。[R8 恢复边界](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-workstation-r8-legacy-recovery-20261024.json)。

## 原生安装包

五平台 **1.0.4 正式安装包**已通过声明范围内的构建、包检查与最终本地门禁；最终 stage、校验和及 [v1.0.4 正式发布](https://github.com/neko233-com/git-next/releases/tag/v1.0.4)均已核验，可从发布资产选择同版本安装包。

| 平台 | 1.0.4 安装包 | 已验证范围 |
| --- | --- | --- |
| Windows x64 / ARM64 | 机器级或用户级 MSI | PE/导入、四 MSI 载荷/表、隔离迁移及资源探针；live MSI 生命周期豁免；全部未签名 |
| Linux x64 / ARM64 | 含 install.sh 的完整 tar.gz | Debian 12 / glibc 2.36；每个 Docker 目标 52 native 场景及两路 P4，不冒充 ARM 真机 |
| macOS Apple Silicon | 含 install.sh 的完整 tar.gz | 真 Mac 51 native 场景及两路 P4、Mach-O/shell 检查；不声明完整宿主安装/launchd 生命周期 |

参见[安装与卸载](docs/INSTALLATION.md)和[接管说明](docs/TAKEOVER.md)，仅使用与正式 accepted release 匹配的资产。bootstrap 会在 MSI 写入前保存原 Git 快照；本地门禁检查点不等于 1.0.4 已在本机安装。

**历史 v1.0.3 发布及真实 MSI/工作站证据：**从历史 [v1.0.3 发布页](https://github.com/neko233-com/git-next/releases/tag/v1.0.3) 获取安装包与校验和。发布时间为 `2026-10-23T20:44:12Z`，发布源码为 `05dac2a938784ce03a2de219349c4d381a637ab5`。28 个远端资产的哈希全部与本地发布一致，27 个资产列入 SHA256SUMS，SHA256SUMS 与验收证明均已实际公开下载核验。四个 MSI 实际为未签名（`NotSigned`），不声明密码学签名验证。Windows MSI 内含完整自有运行时，无需额外编译器。

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

Go CLI 的唯一门禁是 `go -C tools/devtool run . cli-accept --tier l0|full`，L0 是合并/tag 门禁，发布分支必须通过 full。验收器输出 JSON 报告，使用隔离 harness（空 PATH/GIT_EXEC_PATH、隔离 HOME/USERPROFILE/XDG/SSH 配置、子进程超时与 Windows 64 进程作业上限），参考 Git 仅用于隔离夹具，产品检查拒绝回退到本机已安装的 Git。详见 [CLI 验收](docs/GIT_NEXT_ACCEPTANCE.md)。native C 产品行为及平台生命周期由各自的 Go/平台门禁另行验收，不由 Go CLI 报告推定。历史 Python/Rust CLI 断言保留在 Git 历史中。

独立正确性验证并行执行，Windows Installer 事务串行执行，报告保留真实耗时和失败结果。性能采用独立成对批量测量，根据实际 median、p95 和主进程内存给出结论，不承诺所有场景都更快。[构建](docs/BUILDING.md) · [性能](docs/PERFORMANCE.md) · [兼容性](docs/COMPATIBILITY.md)。

按用户明确选择的[安装包验证发布范围](docs/GO_TOOLING.md#explicit-package-validation-release-scope)，可用真实静态包、隔离迁移与资源行为探针代替两项本机 MSI 事务前置要求。两项生命周期仍标 `waived`，不冒充实际安装通过；发布资产保留授权、探针和精确包哈希。其它 native CLI、传输、Docker、race 与性能门禁继续执行。

原生核心遵循 GPL-2.0-only，并提供依赖许可证及对应源码包。[源码与许可](docs/CORRESPONDING_SOURCES.md)。本仓库不使用 GitHub Actions。

## 独立 Go 客户端产物

独立 Go 客户端由 `task build`（`go -C tools/devtool run . build-go-client`）构建到 `target/release/git`，产物用 `-mod=mod -trimpath -ldflags "-s -w"` 且 `CGO_ENABLED=0` 生成。native C/MSI 安装包由独立的 Go 工具和平台脚本另行构建、打包和验收。历史 Rust preview 的独立 zip 与 `package-rust` 命令已从当前树删除。[工具链与迁移证据](docs/GO_TOOLING.md) · [构建说明](docs/BUILDING.md)。

## Windows 一键安装

取得已正式发布的 v1.0.4 同版本 accepted `install.ps1`，先用 `powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\install.ps1 -Plan` 查看选择；只有请求安装该正式版本时才去掉 `-Plan`。bootstrap 默认选择已注册 Git for Windows，32 位 PowerShell 先自动转发到原生 64 位再请求 UAC，并在失败时保留受保护的 MSI 前快照路径及 SHA。安装无需 Go、Python 或 WiX。**当前 1.0.4 检查点没有执行 live MSI 生命周期，也没有在本工作站安装 1.0.4。**上文真实 MSI/工作站成功记录属于 1.0.3。参见[安装说明](docs/INSTALLATION.md)。

[Git-Terminal](docs/TERMINAL.md) 当前由 Go + MyGo 实现（renderer=mygo），支持标签页、Unicode/IME、复制粘贴、历史滚动及自有 Bash、PowerShell/CMD 配置；SSH、SFTP、主机监控、askpass 对话框尚未迁移，仅存在于历史 Rust + GPUI Fast 发布二进制中。它替代 Git Bash 桌面入口，不添加 Git GUI 菜单。[内置 LFS 与游戏项目模板](docs/GAME_PROJECTS.md)覆盖 Unity/Tuanjie、Unreal，无需另外安装 LFS。

## 源码与许可

原生 C 核心遵循 GPL-2.0-only；自有扩展与打包工具遵循声明的许可证。原生发布包提供固定依赖、许可证材料及对应源码。历史 Rust preview 的验收报告仅作为旧发布产物的证据保留。正式产品安装请使用上方原生安装说明。本仓库不使用 GitHub Actions。
