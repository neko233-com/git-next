# git-next 固定交付清单与进度

## 当前交付范围 · 1.0.3 本地门禁完成（2026-10-24）

本轮完成 Python 工具链退役及声明范围内的本地正确性、成对性能门禁，准备一次整合发布。发布产品继续采用从固定源码构建的 native C Git 引擎；Go 承担构建、打包、文档、夹具、benchmark、维护和发布，并提供 Go `git-p4` helper；Rust 客户端作为独立 preview，由 Rust runner 验收。必要 PowerShell/POSIX shell 平台脚本保留。此源码快照的 release staging、工作站安装/更新、push 和发布仍为 pending；预计的 [v1.0.3 发布页](https://github.com/neko233-com/git-next/releases/tag/v1.0.3) 不表示这些动作已发生。

115 个本批 Python 文件已有逐文件 SHA、能力映射和退休理由，自有 Python 文件与直接执行入口均为 0。30 个历史 CLI suite 保留在 Git 历史 `3609553`；当前不存在 `tests/legacy_cli/`，Rust 的 100 场景也不代表全部历史断言逐条等量迁移。第三方对应源码包内的上游原文仅作许可材料，不作为产品工具安装或执行。[工具链](GO_TOOLING.md)。

| 当前证据 | 已完成范围及边界 |
| --- | --- |
| Go `git-p4` A/B | 相同 Linux 隔离环境调用上游 t9800–t9836 共 37 套，36 套执行、395 项断言；原 Python 379 成功/16 已知缺陷，Go 382 成功/13 已知缺陷，新增差异失败 0。三项改善产生的 XPASS 及原始退出码保留；t9835 因缺少 Python 2 跳过，Go t9836 只适配解释器 launcher 前置检查，12 项行为断言保留。Windows 26 个顶层真实 P4/协议测试通过。 |
| Rust 客户端 | Windows x64 与真实 macOS ARM64 的 full 各 100/100，失败/跳过均为 0；L0 为 39 个命令。Mac 构建及 Clippy 通过。`complete_git_parity=false`，与 native C 产品分别验收。 |
| Linux native | x64、ARM64 Docker 各 52 场景和两路 owned Git/P4 入口通过；产品 Python 扫描为 0。ARM64 为 Windows x64 上 Docker Desktop 执行 ARM64 进程，不宣称真实 ARM64 硬件验收。 |
| macOS native | 真实 Apple Silicon macOS 15.5 上 51 场景及两路 owned Git/P4 入口通过；同一归档通过独立 Mach-O/shell 静态检查及目录/归档 Python 0 扫描。Darwin CPU affinity 约束明确不支持。 |
| Windows ARM64 | native core/bridge/helper 交叉编译、PE/导入和包静态检查；尚无真实 ARM64 运行或安装验收。 |
| Windows 稳定默认值 | 权威片段采用 `core.fscache=false`、`core.fsmonitor=false`、`core.preloadIndex=false`、`index.threads=1`、`grep.threads=1`；同一 core 的隔离 4,000 文件对照完成 30 轮 grep/status 和 6 项契约。最终 R6 的 read-thread lane 通过，工作站安装状态另行绑定。 |

Windows 默认值对照保留原 `true/0` 配置在第 21 轮只返回 3,999/4,000 文件的失败证据，保持完整输出断言。该对照没有证明内部根因或性能优势；用户、仓库和命令行显式配置仍有正常优先级。POSIX 包不消费此 Windows defaults fragment，现有已验收 POSIX 归档字节保持不变。[公开对照回执](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/windows-read-safety-20261023.json)。

**本地门禁完成。** 最终 R6 correctness 为 18 lane 中 **16 通过、2 项用户豁免**，耗时 **246.204 秒**；同一冻结矩阵的 paired performance 为 **5/5 通过、691.251 秒**，覆盖 1k/10k/100k 文件和 loopback HTTPS，各 10 对样本，含冻结输入回读。性能通过表示完整批次与输出校验完成，不表示所有场景更快；两批次均未达到全批次 60 秒目标。实现提交为 `f2e5c1b0f9966f18c08dff1718e759833e9ff819`，冻结矩阵为 `final-matrix-r6.json`，SHA-256 `685686ff842eeb32a5343c51930cf2d983f132f80e47430a14ff70c528ad7260`。[公开总证据](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-python-retirement-20261023.md)及其 [JSON](https://github.com/neko233-com/git-next/blob/main/docs/benchmarks/native-python-retirement-20261023.json)记录原始回执身份和历史失败。

Windows x64 MSI 用户级实际通过 **27 项、622.690 秒**，机器级通过 **25 项、473.217 秒**，包含安装、修复、升级、注入失败回滚、拒绝降级和卸载。R6 只在不可变输入及严格限定的工具变化校验通过后复用这些真实回执；246.204 秒不是把完整 MSI 事务重新执行一遍的总耗时。Windows ARM64 的两个 MSI 只有编译、表及静态检查，不声明硬件生命周期验收。这些隔离生命周期通过也不表示最终工作站安装已发生。

用户豁免只覆盖 `terminal-rust-contracts` 与 `terminal-native-desktop`，结果保留为 `waived`，不计作通过。`terminal-waiver-r6.json` 的 SHA-256 为 `0286de6e4fba224269c28e8f75fe8c4327f30b004b3ac31d23fd80ca2b4d41f7`，绑定同一 R6 矩阵及两个架构的确切终端字节。R4 的 GUI startup/MSI rollback 失败、R5b 因主控缺少 Go PATH 导致的四个 Docker lane 失败和 R5c 的 100k grep 失败均保留。R6 修复 benchmark 超时分类并将 100k 单命令 deadline 调为 120 秒，完整输出、退出码、fsck 和 3 次 warmup + 10 对实测校验保留；原 R5c 失败根因仍未确定，不改写为已证明的超时。

**后续交付动作：pending。** 最终 release staging、暂存产物校验和及配置的签名验证、工作站安装/更新与配置回读、push 和一次整合发布尚未发生或尚未形成最终回执。发布后的动作与远端资产回读由 release 资产和后续文档记录；此源码快照不预先声明本轮 10/10、工作站接管完成或 1.0.3 已发布。[机器可读状态](https://github.com/neko233-com/git-next/blob/main/docs/DELIVERY_STATUS.json) 中 `current_release_evidence` 分开记录本地门禁与交付动作。

## 历史已发布版本 · v0.3.0（2026-10-21）

以下保留 v0.3.0 原始交付、进度及验收记录。其中 10/10、18 项正确性、性能数字、工作站状态和 Mac 仅静态的范围只属于当时产物；它们不表示当前 1.0.3 已完成，也不限制本轮新增的真实 Mac 验收。JSON 原有 `scope_id`、`policy`、`deliverables` 和 `progress` 同属该历史范围，保持原证据。

截至 2026-10-21，本轮客户端固定范围 **10/10（100%），剩余 0 项**。[v0.3.0](https://github.com/neko233-com/git-next/releases/tag/v0.3.0) 已一次性发布，21 个附件的远端字节数和 SHA-256 与本地产物一致。[最终本机与发布回执](benchmarks/native-workstation-release-20261021.json)记录实际完成层级。

Windows 本机已安装正式机器级 MSI，卸载原版 Git for Windows；新 CMD、PowerShell 和旧绝对路径均返回 git-next。旧路径仅保留兼容入口，受保护的原安装恢复备份保留。13 个 Git/SSH 配置文件逐字节不变，默认采用 LF。Git-Terminal 正式窗口已打开，包含原创多尺寸图标、一体化标题栏、原生窗口按钮、多 tab、命令高亮和目录/分支提示。

正确性组合共 18 项；常用命令为 16 组 × SHA-1/SHA-256，共 32 场景，31.482 秒通过；实际最终包桌面 15 项，25.287 秒通过。原 MSI 两种范围的完整生命周期证据保留其明确的安装契约范围；终端二进制和说明更新分别绑定。随后发现并修复机器 TLS 子文件句柄阻止目录重命名，实际机器刷新、首次安装、重复安装、17 文件哈希和受保护权限均通过。继承 ACL 转换修复另通过 44 项更新器检查及 57 项真实配置迁移/恢复。未改动的核心、运行时、GUI 和 MSI 配方不重复运行无关测试。

最终性能批次为每项 10 对样本，覆盖 1k/10k/100k 文件和 HTTPS，596.63 秒全部通过。全批次没有达到 60 秒；HTTPS clone 和大型全量扫描不保证快于参考 Git。旧大样本结果保留原二进制身份。

Windows ARM64 为交叉编译/PE 检查；macOS ARM64 为本机编译及 Dockerfile_mac 包/Mach-O/shell 静态检查，未执行 Mac 产品。任务自有 Docker 资源及 Mac 构建缓存已清理；其他项目资源保留。GitSSS 同仓库同分支 100 push/秒属于服务端未完成要求，不计入此客户端 10 项。

机器可读状态见 [DELIVERY_STATUS.json](DELIVERY_STATUS.json)，验收范围见 [TEST_COVERAGE.md](TEST_COVERAGE.md)。发行 tag 包含运行产物源码及独立 bootstrap 修复；MSI/运行时和 bootstrap 分别记录来源提交。本记录属于交付后文档提交，不改变已发布二进制或 tag。

| 范围 | 状态 |
| --- | --- |
| D01–D07 核心、运行时、LF、身份/语言、MSI接管、自动更新、五平台包 | 完成 |
| D08 批量正确性与声明的平台验收 | 完成 |
| D09 独立成对性能批次 | 完成 |
| D10 本机安装/接管、真实窗口、一次发布 | 完成 |

## 历史构建记录

以下保留此前构建快照；其中百分比、未发布状态和性能数字只属于当时产物，不能覆盖上方最终记录。

## 单一路线与执行顺序

2026-10-21 当前进度：实现项 7/7，完整交付项 7/10。新终端已通过 14 项真实桌面检查，耗时 23.184 秒；标签页、目录/分支、命令高亮和中文文本均有截图与记录。组合验收发现用户 MSI 内 PowerShell 初始化失败，现已实现独立无控制台启动器并通过针对性检查，等待重建后的 MSI 生命周期验收。此前未发布的 MSI 已卸载，本机仍由官方 Git 提供命令；正式发布和本机接管尚未完成。

主路线固定为**从已固定的上游源码构建并维护 git-next 原生兼容核心**，整合本产品的身份、LF、语言和安装更新行为。停止逐个重写 Rust Git 子集来追赶完整 Git，也停止新的目录扫描性能分支。Rust 既有代码只在明确需要的独立配套边界使用；GPL 核心和其他组件的许可、运行时边界、源码交付仍须在 D02/D07 解决。不得调用本机已安装的 Git/OpenSSH 充当运行时兜底。

1. 完成 D01–D04 的核心、运行时、LF 和语言整合。
2. 完成 D05–D07 的 MSI、自动更新、五平台完整包及最终统一回归输入。期间允许必要的编译、静态语法检查和文档生成，不运行功能回归、安装试验或性能测试。
3. D01–D07 全部关闭后才启动 D08。独立临时仓库检查可批量并行，MSI/系统集成串行执行；Docker 使用生成账号并清理本任务资源。禁止再要求个人私有仓库或重复探测真实账号。
4. 收集本批失败后统一修复，再核验受影响检查和最终候选产物。不承诺测试只运行一次就必然无错，也不回到每完成一个小功能就执行多轮测试的方式。
5. 正确性通过后执行 D09，所有性能场景作为同一批处理，不与构建、回归或模拟器并发。慢于参考 Git 的结果必须列为待修复或明确未达标，不能宣称普遍更快。
6. D08/D09 通过后才完成 D10；仅发布一个汇总版本。最终交付前的接管演练必须可回滚，最终默认命令安装以实际 Windows 调用结果为准。

`go -C tools/devtool run . product-batch` 增加实现阶段前置检查；D01–D07 未全部关闭时拒绝进入测试阶段。现有脚本仍是子集脚手架，D07 必须先适配完整核心，不能通过修改状态文件把子集测试冒充完整回归。其他单项脚本也不得绕过本轮执行顺序。

## 每批进展必须报告

- 本批关闭了哪个编号，以及源码、产物或验收证据；没有整项关闭就明确写“本批未关闭整项”。
- 完整交付 `完成数/10`、实现 `完成数/7`、剩余编号；百分比只由整项关闭数计算。
- 当前阶段、下一批的明确退出条件、尚缺的真实运行环境。没有可靠依据不报小时数或“即将完成”。
- 已知阻塞仍留在原编号，不拆出更多完成项抬高进度，不增加与用户目标无关的功能。

本轮状态核对没有启动产品测试或性能测试。历史接受记录保留其原始源码与二进制范围；它们不会被重标为最终产品验收。

## 本批实现证据 · 2026-10-20

[完整主核心实现记录](../native-core/receipts/implementation-complete-core-20261020.json)固定本批十个生产目标、上游源码指纹、补丁、6256 条翻译目录、PSL 导入依赖、完整发行树和 MSI 哈希。D01 按实现完整性关闭；D03/D04 的先前记录保留原始字节范围。首个 Windows x64 完整发行树含 9733 个文件，MSI 含 9735 个文件，大小为 117351292 字节；这些数字仅描述产物，不换算进度或性能。

发行树的七个 Git 入口均使用本项目编译的核心，没有部署上游 Git 核心。配套包含独立 SSH、GCM、Git LFS、shell/脚本解释器、签名工具、模板/文档、软件 LF 默认和证书。默认 Schannel curl 已接入公共后缀校验；显式 OpenSSL 配置走独立获取的固定 provider，发布包不包含该 provider DLL。MSI 编译仅进行输入哈希、架构、语法和打包检查，没有执行产品或安装器。

D08/D09 尚未开始；新核心未执行、安装、接管或发布。本机仍为原 Git 和独立旧预览。剩余编号为 D02、D05、D06、D07、D08、D09、D10。下一阶段退出条件为：四项剩余实现全部关闭，五平台包及完整回归输入已备齐；达成前不进入测试。

[静态依赖收口记录](../native-core/receipts/implementation-static-runtime-20261020.json)保留后续 nghttp2 静态链接修正、48 个自编译入口/依赖 PE 的归属检查和两个 Windows 架构 provider 的 25 个 API 导出检查。最新未安装 MSI 为 117732475 字节，含 9736 个文件。检查只读取 PE/哈希，没有加载 DLL 或执行产品；不计为 D08 验收。生成脚本同时改为内容相同时不重写头文件，避免无效全量重编译。

## Windows 迁移实现批次 · 2026-10-20

[迁移实现记录](../native-core/receipts/implementation-windows-migration-20261020.json)记录自有控制台/GUI 转发入口、版本资源、系统配置承接、备份/访问控制/注册表/快捷方式/Terminal 保留、旧版卸载、自动与显式回滚、MSI 卸载保护及共享更新锁的源码和编译产物。用户级完整 MSI 已编译；没有执行迁移脚本、产品、安装器或测试。随后源码补入明确的安装范围字段与机器级到用户级接管阻断，编译记录保留包内和当前脚本各自哈希，不能把较早字节当成后续改动的验收。

本机安装元数据确认原 Git 位于 LocalMachine，因此 D05 仍须完成机器级 MSI、受保护更新、全机调用路径与恢复范围；不能以用户级安装替代本项。直接引用 Git 安装目录的服务数量为零只说明该条静态查询，没有证明不存在间接调用者。本批未关闭整项：仍为 **3/10（30%）**，实现 **3/7**。下一批明确退出条件是机器级安装/更新/迁移所需源码和完整 MSI 均已生成，原用户级标识保持兼容；功能与性能回归仍等待 D01-D07 全部关闭。

## Windows 双范围安装与更新入口批次 · 2026-10-20

[本批记录](../native-core/receipts/implementation-windows-scopes-20261020.json)关闭 D05 的实现工作，当前为 **4/10（40%）**，实现 **4/7**。机器/用户两个 MSI 各含 9896 个文件，压缩包约 124 MB，均未安装。原生版本依赖、更新 CLI 和后续 PATH 修复的当前源码指纹与较早包内指纹分别保存；不会把旧包当成这些改动的运行验收。

固定剩余编号为 D02、D06、D07、D08、D09、D10。下一批先完成 D02 的既有系统运行时配置承接，再收齐跨平台更新和五平台包、许可证/对应源码与完整回归输入；不增加交付项，不重复按功能打包测试。D01–D07 全部关闭前，D08 功能回归和 D09 性能批次均不启动。原 Git、本机登录和仓库数据未改动。

## 运行时配置承接批次 · 2026-10-20

[源码和编译记录](../native-core/receipts/implementation-runtime-configuration-20261020.json)保存不可变默认配置、活动配置、升级保留、迁移快照和 MSI 初始化/回滚/提交的实现指纹。Windows 原生 TLS 配置选择已编译；后续 POSIX 自有 SSH/shell/路径补丁已整合，尚待目标编译。没有运行产品、安装器、初始化或迁移，也没有为本功能重复打包 MSI。

本批未关闭新的整项：仍为 **4/10（40%）**，实现 **4/7**。D02 还差其余平台运行时构建/装配输入；D06 还差跨平台更新和最终清单；D07 还差五平台最终包、对应源码/许可证及完整回归输入。三项实现全部关闭后才启动 D08，随后批量完成 D09，最终 D10 发布一次并安装接管。

## POSIX 核心构建与主配套输入批次 · 2026-10-20

[本批编译记录](../native-core/receipts/implementation-posix-core-20261020.json)记录 Linux x64 十个生产目标、243 个核心装配文件、62,420,246 字节构建归档及原生镜像/完整 Perl 头文件的静态核对。修复了上游 CMake Perl 安装文件名和头文件换行问题。两个构建的临时容器/卷均已清理；没有执行新核心或功能测试。

已批量固定并准备 Linux x64/ARM64、macOS ARM64 的 Git LFS 3.7.1 和 GCM 2.7.3，逐份记录官方资产摘要及实际原生架构；没有运行或注册这些配套。Windows 系统配置迁移也补入有序 credential.helper 保留逻辑。

本批未关闭新的整项，仍为 **4/10（40%）**、实现 **4/7**。D02 还差 POSIX SSH/shell/签名配套、动态库闭包及完整装配；D06 还差跨平台更新；D07 还差五平台最终包、对应源码/许可证和统一回归输入。macOS 原生构建环境仍待提供。D08/D09 未开始，原 Git 和本机登录/仓库数据未改动。

## POSIX 运行时与语言桥实现批次 · 2026-10-20

[本批记录](../native-core/receipts/implementation-posix-runtime-language-20261020.json)保存 Linux x64 自有运行时材料、共享 GUI/手册/HTML 输入和 Windows 十个语言桥生产目标的编译指纹。临时 Docker 容器和卷已清理。通用 POSIX 装配器要求真实更新器和源码匹配的核心；没有执行装配、产品、安装、接管或测试。后续 IDN/GSS/HTTP2 和许可证采集改动仍是源码，等待 D07 最终重建。

本批未关闭新的整项，仍为 **4/10（40%）**、实现 **4/7**。剩余编号固定为 D02、D06、D07、D08、D09、D10。下一批退出条件是跨平台更新器与安装事务实现完备，再收齐五平台最终构建/包和完整回归输入；D01–D07 全部关闭前不运行 D08/D09。

## 跨平台自动更新实现批次 · 2026-10-20

[本批实现记录](../native-core/receipts/implementation-cross-platform-updates-20261020.json)关闭 D06：Windows 更新入口和三个 POSIX 原生更新器已实现；Go 1.27.2 编译 Linux x64/ARM64、macOS ARM64，Windows x64 十个原生核心目标也已消费新的 IDN/HTTP2 依赖重建。POSIX 使用受保护状态/独占锁、独立版本目录、原子 current 链接及事务恢复，Linux systemd 和 macOS launchd 承接后台更新。没有执行更新器、服务、安装器或产品。

当前 **9/10（90%）**，实现 **5/7**；剩余 D02、D07、D08、D09、D10。D02 还差其余目标运行时闭包/装配；D07 还差五平台最终包、对应源码/许可证和完整批次输入。Windows ARM64 构建工具正在补齐，Mac 构建连接已可用。按最新用户要求，Mac 宿主不运行产品测试；`tests/containers/Dockerfile_mac` 负责最终包、Mach-O 与 shell 语法隔离检查，不冒充 macOS 内核/Keychain 验收。该容器尚未构建或执行。

本机默认 Git 尚未接管，也没有发布。旧开发构建缓存通过 Cargo 的限定 dev profile 清理命令释放 23.6 GiB；本批临时 Docker 容器/卷已清理，其他项目镜像/数据卷保留。


## 五平台运行时实现收口 · 2026-10-20

[实现记录](../native-core/receipts/implementation-five-platform-runtimes-20261020.json)关闭 D02。五平台核心均消费同一集成源码；Mac ARM64 已完成原生编译、11,693 文件的运行时装配和产物取回，没有在 Mac 上运行产品测试。Windows ARM64 使用隔离的 LLVM-MinGW 工具链，未终止用户已有 MSBuild 进程。Linux x64/ARM64 配套分别收集 2,905 / 2,912 文件，所有临时构建容器/卷已按本任务标签清理。

当前 **6/10（60%）**、实现 **6/7**；只剩 D07 实现收口，之后统一进行 D08、D09、D10。原生候选版本由 `packaging/product.json` 独立声明为 0.3.0；这不是已发布或已安装的版本。`complete_git_parity` 仍为 false，本机默认 Git 还未接管。


## 完整候选包与并行回归输入

[本批记录](../native-core/receipts/implementation-final-packages-20261020.json)关闭 D07：当前为 **7/10（70%），实现 7/7**，剩余 D08、D09、D10。默认 8 路并行执行独立检查，两个 MSI 范围保留一个串行安装通道。报告记录整批实际耗时及是否低于 60 秒；不删减检查，不将超时改写为通过。性能采样单独成批执行，避免并行负载污染结果。源码材料详情见 [对应源码](CORRESPONDING_SOURCES.md)。

## 最终统一候选回归（2026-10-20）

[统一回归记录](benchmarks/native-correctness-consolidated-20261020.json)的 12 项报告全部通过。独立功能验证耗时 **45.725 秒**，达到 `<1min`；完整批次含用户/机器两套 MSI 安装、修复、失败注入回滚、升级、降级拒绝和卸载，耗时 **652.943 秒**，完整批次未达到一分钟目标。没有为达标跳过检查。Mac 只进行了包、Mach-O 和 shell 静态检查，未在 Mac 执行产品。

进度为 **7/10**：D08 完成，D09 同一候选的成对性能批次正在执行，D10 发布和实际接管尚未完成。接管前只读预检发现三处旧 cmd 启动器映射和原更新器通知工具退役规则需要补齐；原 Git 尚未移除。

## 修复后验收与发布前进度

当前为 **9/10**。更新后的[正确性记录](benchmarks/native-correctness-consolidated-20261020.json)全通过：并行功能 **52.177 秒**，含串行 MSI 生命周期的完整批次 **641.437 秒**。旧 cmd 包装器映射和原更新器通知工具退役规则已整合。

[性能批次](benchmarks/native-performance-consolidated-20261020.json)全部通过，耗时 **371.473 秒**，完整性能批次未达到一分钟目标。千/万/十万文件 status 中位数分别为 19.206/61.075/86.173ms，参考命令为 27.154/69.390/95.485ms。启动约 12ms，对照约 20ms。网络表现接近参考；主进程内存不等于包含参考包装器子进程的总内存。D10 安装、实际接管与唯一发布尚待完成。

## git-default-v2 布局重构（2026-10-22）

本节记录在 v0.3.0 发布之后发生的安装布局变更，不改写上面各批次的历史结论。

MSI 目录树已合并到官方 Git 默认目录：机器级 `%ProgramFiles%\Git`，用户级
`%LOCALAPPDATA%\Programs\Git`。主 IDE 入口 `cmd\git.exe` 由 git-next 载荷直接承担，
不再是指向旧安装的桥接转发器，因此 IDE 的 `Cannot Run Git` 报错从根因上消失。
`bin\git.exe` 与 Git-Terminal 属于同一受管安装。

接管语义同步改为同址覆盖：`Get-GnTakeoverPlan` 不再要求新旧目录分离（仍拒绝嵌套），
新增 `same_directory` 与 `stale` 状态；同址时不铺桥接转发器，而是按 sha256 精确删除
官方卸载器遗留物，并把迁移后的 `system.gitconfig` include 改指 `runtime\etc\gitconfig`。
回滚在 MSI 仍占用目录时会被拒绝，并提示先卸载 MSI。

两处历史缺陷已修正。其一，`REPLACEGIT` 授权属性此前被标记为 `Secure="yes"`；Secure 属性在
`InstallExecuteSequence` 期间不可由命令行改写，也不会从客户端传递给服务端，因此受管安装下
该值会静默保持 `0`，官方 Git 已注册时安装会被 Launch Condition 恒定阻断。现已移除 `Secure`，
并在 `install.ps1` 中从 verbose MSI 日志核验安装器确实记录了 `REPLACEGIT=1`，未记录则拒绝继续。
其二，`Remove-GnBootstrapLegacyInstall` 此前在 32 位进程下使用空的 `$env:ProgramW6432`，
使期望路径退化为 `\git-next`、前缀校验过宽；现改为从 64 位注册表视图解析原生 Program Files。

MSI 已按新配方重建（134,516,157 字节，10,601 载荷文件，ProductCode 与 UpgradeCode 不变，
升级链成立）。载荷结构直读验证 13/13 关键入口落位正确，缺失 0。`REPLACEGIT` 属性的 Secure 列
已确认为空。实际安装验收仍受本机环境限制（`msiexec` 对任何包返回 1619），尚未执行。

## 多语言与文档站（2026-10-22）

本节记录在 v0.3.0 之后的接口语言与文档发布变更，不改写上面各批次的历史结论。

接口语言由硬编码的英/中二选一改为数据驱动。`locales/` 下每个 JSON 文件即一种语言，
`build.rs` 自行发现并内嵌全部文件，因此新增语言只需增加一个文件，不需要改动 Rust 代码、
注册表、别名表或帮助文本。英文为源语言：代码中的消息模板是英文，语言文件只列出它覆盖的条目，
未翻译的消息回退英文而不是消失，部分翻译可以正常降级。

设备语言改为以系统原生 preferred-UI-language API 为准（Windows 与 Apple 平台），环境变量
`LC_ALL`/`LC_MESSAGES`/`LANGUAGE`/`LANG` 仅在无原生 API 时参考；区域会先解析为书写系统再退化，
因此 `zh-CN` 命中 `zh-Hans`，`zh-TW` 在提供 `zh-Hant` 时命中繁体。早前版本接受的
`chinese`/`english`/`auto` 等名称保持可用，既有配置文件不会被破坏。

文档站已发布为 GitHub Pages（`gh-pages` 分支根目录，`.nojekyll` 已随分支发布），
`https://neko233-com.github.io/git-next/` 线上可访问，共 24 页。本仓库的工程约定禁止
`.github/workflows/`，因此发布由 `go -C tools/devtool run . publish-docs` 在本地完成：用 Git 底层命令在临时
索引中构建仅含渲染结果的分支提交，不触碰工作区与当前分支；推送为快进，不强制覆盖。

Git-Terminal 同步采用同一套语言文件与自动检测，`git-terminal language list` 可列出内置语言，
`set` 在写入前校验语言名称。

## 语言扩容、配置检查与用户文档（2026-10-22）

本节记录与接口语言和文档相关的后续变更。

语言文档由 2 种（英、简中）扩展到 10 种：新增 `zh-Hant`、`ja`、`ko`、`de`、`fr`、`es`、`pt-BR`、`ru`。
每种语言均提供全部 32 条命令摘要与 60 条核心消息（status/add/commit/merge/分支状态/冲突/帮助标题），
其余消息按设计逐条回退英文。简体中文为完整文档（140 条）。实测 `zh-CN`→`zh-Hans`、
`zh-TW`→`zh-Hant`、`ja-JP`→`ja`、`pt_BR`→`pt-BR`，显式 `--language` 与设备自动检测使用同一套匹配链。

新增 `go -C tools/devtool run . check-locales` 作为配置检查：文档不可读、重复键、源码已不再产生的键、命名占位符
丢失、行尾换行不一致、键未排序均判为失败；`--fix` 排序，`--coverage` 输出各语言覆盖率。
该检查随本地检查流程执行，不进入跨语言回归矩阵——按工程约定，语言只在其文档加入时验收一次，
措辞问题属于该文档自身的缺陷，通过修改 JSON 修正。

新增两篇面向用户的文档：`docs/GIT-GUIDE.md` 为任务导向的 Git 手册（替代官方文档的阅读入口，
所有示例命令均对照 `--help` 核实），`docs/WHY-GIT-NEXT.md` 记录提速原理与实测数据、
以及 Git 命令集之外的额外能力，并明确写出尚未达到的部分与测量局限。

文档站扩至 26 页。

## HTTPS 克隆修复、克隆基准与生产就绪清单（2026-10-22）

本节记录网络传输缺陷的修复、clone 实测方法与结论，以及生产就绪的差距清单。

**传输缺陷（已修）**：`http.sslcaInfo` 指向 rustls 无法解析的证书捆绑（Windows 上常见的
curl `ca-bundle.trust.crt`）时，之前会让**所有** HTTP 与 HTTPS 远端直接失败，连纯 `http://`
也被牵连——因为 HTTP 客户端在判断 URL scheme 之前就构建了 TLS 配置。现在 TLS 设置只作用于
`https://`，捆绑解析不出证书时回退默认信任库而不是中止连接。本机修复前 `http://127.0.0.1`
克隆直接报 `Configured TLS CA bundle has no certificates`，修复后正常。

**克隆优化**：fetch 之后原本重新 `open` 一次刚克隆的仓库再做检出，该次重开会重读配置并重扫
pack 目录；现直接复用 fetch 返回的仓库句柄。另新增 `GIT_NEXT_TRACE_TIMING=1` 阶段计时，
把克隆拆成「传输+索引」与「检出」两段。

**实测（gitsss 本地服务端，HTTP，3000 文件 / ~100MB / 12.7MiB pack，9 轮交替采样）**：
Git 2.55.0 中位 3379ms，git-next 中位 2726ms，快 **1.24×（-19.3%）**。
服务端访问日志显示每次克隆有 2.0–2.9s 花在服务端（info/refs 370–720ms、协商 370–640ms、
打包 1000–2000ms），客户端实际工作约 0.5–1.0s。Windows 上 `git upload-pack` 单次启动约 360ms，
因此该结论**低估**了客户端贡献；该占比在 Linux 服务端会大幅下降。

**GitHub 对照**：本沙箱经拦截代理访问 GitHub 的 TLS 握手不稳定（`unexpected eof` /
`tls handshake eof`），无法给出可靠的克隆数字，已在文档中标注为未测并给出复现命令。
可测得的是单请求路径：`ls-remote` 约 1020ms 对 1990ms（探索性，3 次）。

**新增文档**：`docs/CLONE-BENCHMARK.md` 记录方法、数字与时间去向；
`docs/PRODUCTION-READINESS.md` 列出到生产就绪仍缺的五项阻断、平台与安装证据缺口、
以及一份可勾选的就绪清单。
