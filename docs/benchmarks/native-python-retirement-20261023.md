# Python 工具链退役证据

报告开始日：2026-10-23；最终本地门禁完成日：2026-10-24。R7 correctness 为 16 pass / 2 waived，249.801 秒；performance 为 5 pass，668.849 秒。本报告是源码检查点：当时 release staging、工作站更新和 push/publish 尚 pending；后续完成动作由独立公开 release-completion asset 与 main 的正常文档提交记录，不重写已接受包中的本报告。被拒候选与用户豁免均保留，waived 不计为 pass。

机器可读清单见 [native-python-retirement-20261023.json](native-python-retirement-20261023.json)。该清单逐文件记录旧入口 SHA-256、对应 Go/Rust 模块、退休理由与覆盖边界，只含仓库相对路径、哈希和公开计数。

## 文件清单

以下数字从四份实际退休 JSON 逐条计数并检查路径唯一性，没有用需求中的预计数代替清单。

| 组 | 本批文件 |
| --- | ---: |
| runtime / maintenance / evidence | 24 |
| native build / batch | 19 |
| packaging / release | 17 |
| acceptance / fixtures / benchmarks | 55 |
| **合计** | **115，115 个唯一路径** |

旧回执另报告先前已迁移 6 个主要入口；尚缺完整逐文件清单，因此暂不把它们称为额外 6 个已核验 Python 文件，也不宣称 121 个文件均有逐文件 SHA。Git 历史另外可证实 `check_locales.py`、`publish_docs.py`、`render_docs.py`、`verify_repo_contract.py` 四个 docs/check 工具，JSON 单列其 SHA 与 Go 映射；仍不把旧 reported_count 6 当作完整逐文件清单。

本批包含 30 个历史 CLI suite 的清理，原断言保留于 Git 历史 `3609553`。Rust 是现行 CLI 主门禁；这不表示全部历史断言逐条等量迁入 Rust。当前源码审计记录自有 Python 文件 0、直接 Python 执行入口 0；五平台各自回执绑定的产物严格扫描均为 Python 0，Mac 同一归档也通过；后续重新打包须更新对应审计。

## 架构与能力

Rust `accept-runner` 负责客户端 CLI 行为；Go `tools/devtool` 负责构建、打包、文档、发布、fixture、benchmark 和平台编排；Go `tools/git-p4` 负责产品中的 Perforce helper。必要的 C、PowerShell 和 POSIX 平台代码保留。第三方对应源码归档可保留上游 Python 原文，作为不可变许可证材料，不安装或执行为产品工具。

旧文件到新模块的完整映射位于 JSON 的 `retirement.inventory`。MSI 身份、配置/registry/layout、升级/修复/卸载语义、失败关闭的回执验证、源代码与许可证材料均作为兼容边界保留。30 个 legacy CLI suite 在清单中明确标为归档后的范围化 Rust 门禁，避免误写为逐断言等价。

## 输入与等价验证

| 验证组 | 已成立的证据 | 范围 |
| --- | --- | --- |
| native source prepare/integrate | 4,919 文件，内容差异 0，语义回执相等 | 同一原生源输入 |
| Windows runtime / iconv / libpsl | 两架构 runtime manifest 相等；ABI 导入验证 | COFF/归档只归一化时间戳，不声称原始 import library 字节完全相等 |
| TLS | header 字节与锁结构相等，两平台各 17 provider 文件 | 固定 TLS 输入 |
| LLVM | 9,053 文件，差异 0 | 同一 pin |
| POSIX helpers / updater | Linux x64/ARM64 与 Mac helpers 字节/回执相等，updater 字节相等 | 同一锁与编译器 |
| bridge / MSI worker | 双架构 worker 字节相等、ARM64 bridge 字节相等、freestanding 回执相等 | 编译等价，不代替运行验收 |
| shared assets | 1,398 文件，文件记录差异 0，全部成员字节相等 | 固定 GUI / manuals 输入 |
| corresponding sources | 4 个归档组、459 个上游 opaque 成员逐项 SHA-256 验证 | 不解包或修改上游源码成员 |
| migration authentication / SSH / LF | 13 项断言通过；新 client fixture 无 Python | 旧/新参考 Git 版本不同，已公开记录 |
| native CLI / fixtures / benchmarks | 同旧产物运行的 Go/Python 范围化等价 | 不提供最终 native 包的正确性或性能结论 |

每组原始回执的相对位置及 SHA-256 位于 JSON。旧 MSI evidence 测试有 18 项成功及 1 个过时的默认数量断言；Go 测试从权威 defaults fragment 渲染期望，未将旧测试写成全绿。旧 repair evidence 的 9 项通过。wide-tree 测试只作诊断：Go 捕获 2 次短暂非空 status、旧入口为 0；这里不宣称稳定性或性能验收成立。

## Rust CLI 门禁

| 平台 | full | 失败 | 跳过 | 边界 |
| --- | ---: | ---: | ---: | --- |
| Windows x64 | 100 | 0 | 0 | 最终 binding 记录客户端与 runner 前后 SHA 一致，实际报告/log/当前二进制 SHA 已回读匹配 |
| macOS ARM64 实机 | 100 | 0 | 0 | source inputs、完整报告及两份 binary SHA 均已记录 |

该结果属于 Rust 客户端及 Rust runner，与 native C 整包发布独立。L0 为 39 个命令，full 是 100 个范围化场景；`complete_git_parity` 保持 false。

## Go git-p4 差分

Linux 同一隔离环境、同一参考 Git 2.39.5、官方 P4/P4D r24.2，分别调用原 Python 与 Go 的上游 `t9800`–`t9836` 37 套测试；36 套实际执行，共 395 项断言。

| 实现 | 成功 | 原有已知缺陷 | 新增差异失败 |
| --- | ---: | ---: | ---: |
| 原 Python | 379 | 16 | 0 |
| Go | 382 | 13 | 0 |

三项改善是 `t9801 #18` 子集分支、`t9804 #3` 同一 changelist 的两个标签、`t9825 #4` 非法 UTF-16 恢复。原测试框架把已知缺陷消失记为 XPASS，导致对应三套原始退出码仍为 1；证据按 TAP 断言对照原入口，保留原退出码，不伪装成原始全绿。

`t9835` 专用于 Python 2，因验证环境未安装 Python 2 跳过。Go 的 `t9836` 只替换解释器 launcher 前置检查，12 个行为断言保持原样。一次性 oracle 中的原 Python 和辅助 Python 不进入产品或正式 Go 工具链；其拥有的容器已清理。

Windows 真实 P4/P4D 与协议测试 26 个顶层测试通过，`go vet` 通过。包内 Git 外部 alias 拒绝的两项 Linux 回归无跳过通过；Windows 对应两项因 symlink 权限跳过，未将跳过计作覆盖。普通两-changelist 历史的一次性对照还验证了两个原始 Git commit 对象 SHA 相等。

Perforce `p4` 客户端仍是可选集成的外部依赖，与原产品相同。Git helper 从包内定位 owned Git，禁止系统 PATH fallback；长任务可配置正 `GIT_P4_TIMEOUT`，Windows 子进程挂起加入 Job 后恢复，POSIX 使用独立进程组。

## 当前最终 native 产品证据

实现阶段提交：`3a8ccb52cf02991de9fc766b7f722ba83ceccf9c`。各结果仅绑定所引回执的准确输入。Windows r4 整合候选已拒；其静态 Python 扫描成立，不代表 MSI 或最终发布门禁通过。

| 平台 | 已完成的范围 | Python 扫描 |
| --- | --- | --- |
| Windows x64 | R7 适用 native lane 通过，MSI user 27 / machine 25 项真实检查精确复用；Rust full 100/100；两 terminal lane 按用户授权豁免，R4 失败保留 | 10,690 文件，0 Python |
| Windows ARM64 | 构建与 PE/静态包检查；未宣称真实 ARM64 运行或安装验收 | 9,005 文件，0 Python |
| Linux x64 | Docker 中四个 native lane 共 52/52，另 P4 两入口通过 | 4,275 文件，0 Python |
| Linux ARM64 | Docker 中四个 native lane 共 52/52，另 P4 两入口通过 | 4,282 文件，0 Python |
| macOS ARM64 实机 | 最新 r5 native 51/51，另 P4 两入口通过；Rust full 100/100 | 目录及同一归档各 9,282 文件，0 Python |

Mac 最新实机回执的仓库相对位置为 target/go-native-release-1.0.3/macos/runtime-refresh-receipts-r5/native-runtime.json，准确 SHA-256 见 JSON 的 current_runtime_evidence。它绑定同一归档 SHA-256 `ff65a4bbe9d7c38dedcb0c6235649c15044ed5445c4f866f38c477348000bad2`，127031662 字节；22 项 harness 哈希在运行前后相等。此前 Darwin executable-path 编译探测修复及原 r4 回执均保留。C source tree 身份保持 ec6983578eb17d399d7f734e57af9651f194e4202ffc12e598c1b2589a7baaad；Darwin CPU affinity 约束仍明确不支持，没有计作通过。

## 整合门禁及用户授权边界

首轮 r3 为 18 lane 中 12 pass / 6 fail（5 个执行 lane 加 1 个 frozen-input-readback 失败），候选已拒。后续 R4 为 16 pass / 2 fail，frozen-input-readback 为 pass；失败项是 terminal-native-desktop 的 owned GUI startup 断言，以及 msi-user 注入失败后未恢复旧注册并出现 PATH 清理断言失败。原 R4 报告的相对位置和 SHA 保留于 JSON，不将失败改成通过。

用户明确授权“不用再测试git-terminal了，其他没问题全部push和发布，维护.gitignore”。后续仅 terminal-rust-contracts 与 terminal-native-desktop 可以记为 waived；须绑定新 matrix SHA、两 Windows 平台的 terminal commit 和四个二进制 SHA。该授权不覆盖 MSI rollback、其它 CLI/transport、Mac/Linux、包/hash 或性能门禁，也不再执行 terminal Rust、GUI 或 startup 专项测试。授权已绑定 JSON 中 accepted_correctness 对应的新 matrix 和 terminal 字节。

最终 correctness 已绑定 18 行、16 pass / 2 waived，状态 pass-with-user-waiver；五条 paired performance 实际 pass。准确引用及 SHA 见 JSON 的 accepted_correctness / accepted_performance。waived 不能称为 pass。release_eligible、published、workstation_installed_and_accepted 的 false 仅描述此源码检查点尚未执行 release staging、工作站安装/更新和发布，不否定本地门禁已完成。后续动作由独立公开 release-completion asset 与 main 的正常文档提交记录，保留本检查点及原始回执。

完整 R7 correctness 用时 249.801 秒，performance 用时 668.849 秒，均未达到 60 秒编排目标；没有为此跳过检查。性能每操作为 10 对样本，p95 属于小样本探索值，主要进程峰值工作集不含子进程。100,000-file status 中位数为 git-next 1576.423 ms / reference 1547.554 ms，grep 为 8092.692 / 8524.933 ms；loopback HTTPS clone 为 1051.060 / 728.930 ms。不能宣称普遍更快或更省内存，也没有 WAN/provider/GitSSS 吞吐量结论。

R5b 原批次保留为 12 pass / 4 fail / 2 waived，readback pass；四个 Docker 相关 lane 均因编排进程 PATH 缺少 Go 而在预定 fixture 执行前失败。真实 MSI user 生命周期为 27 项检查、622.69 秒，machine 为 25 项检查、473.217 秒；后续完全相同冻结输入可精确复用这些原始通过证据。

原 R5c performance 失败报告仍保留：100,000-file native grep 退出 1 且无 stderr，该失败操作没有有效延迟报告。较小规模的通过不代替失败门禁；后续通过须绑定新报告，不覆盖原失败。

R6 本地 correctness 16 pass / 2 waived（246.204 秒）、performance 5 pass（691.251 秒）作为历史检查点保留。后续 release-stage-r6 的硬链接源别名错误污染了 Windows ARM64 的 25 个文档/metadata 文件，stage 已停止且未发布；严格 r6b immutable 校验拒绝继续。25 个文件仅从已冻结 r5b MSI 的原始 CAB 字节恢复并先脱离旧 inode，9005 个 ARM 冻结文件 SHA 全相符，896 个 exe/dll 无漂移，runtime binary 写入为 0。原污染 stage 保留，恢复没有运行 MSI 安装或 terminal。

release-only 修复提交 e849f9c2fce0b99ee85b8ee52f44a803af12f607 改为新 inode 替换 repair/manual/manifest，成功和错误路径均复查源输入；共享复制器、runtime、benchmark 与 MSI recipe 没有修改。R7 使用新冻结矩阵及新精确豁免，重新完成非 terminal correctness 和全部五条 paired performance，不把 R6 性能重新绑定到新 harness。准确恢复回执与源码 SHA 见 JSON 的 release_staging_repair。
