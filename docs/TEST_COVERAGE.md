# 常用 Git 功能与交付验收范围

当前原生产品采用 SHA-1、SHA-256 双格式仓库验收。测试只创建临时仓库并隔离 HOME、USERPROFILE、SSH_ROOT_PATH 和配置目录；参考 Git 仅用于独立读取对象、索引和输出，不作为 git-next 的运行时回退。

## 常用命令与恢复

`go -C tools/devtool run . native-common-cli-acceptance` 将以下 16 组场景分别在两种对象格式运行，共 32 个独立场景。默认 4 路并发；收集全部失败，失败命令还必须是正常 Git 错误码，不能以崩溃冒充拒绝。每组结束用独立 Git 比较索引和历史，并运行双方 `fsck --full`。

| 场景 | 实际校验 |
| --- | --- |
| 日常提交与读取 | init、add、commit、status、diff、show、log/rev-list；中文路径、对象与索引 |
| 分支与检出 | branch 创建/列出/改名/删除，checkout 分支/路径/-b/分离 HEAD，返回 main |
| 合并与撤销 | merge --no-ff 两父提交，cherry-pick，revert 后再 revert 的树恢复 |
| 变基 | rebase 成功后共同祖先与双方文件内容保留 |
| 恢复与重置 | restore --staged/--worktree，reset --soft/--mixed/--hard 的索引和内容 |
| 暂存现场 | stash push -u、pop --index，暂存内容及未跟踪文件恢复 |
| 覆盖保护 | 脏文件/未跟踪文件拒绝切换分支，保持当前分支、索引和用户内容 |
| 冲突继续与撤销 | cherry-pick/rebase 解决后 --continue，revert 冲突后 --abort |
| 推送保护 | 非快进、过期 force-with-lease 拒绝后远端 refs 不变；fetch/rebase 后正常恢复 push |
| 无效操作 | 不存在的 checkout/revert，删除当前分支；HEAD 和索引逐字节不变 |
| 多工作树 | worktree add/list、独立索引提交、lock/unlock、move、脏目录拒绝、强制删除/prune |
| 本地传输 | bare clone、clone --no-local、remote、upstream、push/fetch/pull --ff-only、跟踪分支 |
| 冲突恢复 | merge/cherry-pick 冲突索引阶段，--abort；rebase --abort 恢复原 HEAD |
| 导出与标签 | 轻量/附注 tag、解引用/删除、archive ZIP 内容、bundle verify/clone、reflog |
| 忽略与换行 | check-ignore、clean 预览/执行/-x、attributes、LF/CRLF 无效变更、二进制原样 |
| 锁与文件操作 | 外部 index.lock 拒绝写入并保留锁/索引，中文 mv/rm 后对象内容与提交 |

SHA-256 的 stash 存储曾因十六进制缓冲区缺少 NUL 结束符空间触发 Windows 栈保护。补丁 `0056-stash-sha256-hex-terminator.patch` 分配 `GIT_MAX_HEXSZ + 1`，保留编译器栈保护；双格式 stash 往返为直接回归。

## 配套验收

| 层级 | 用例/工具与边界 |
| --- | --- |
| 核心契约 | go -C tools/devtool run . native-contract-acceptance：对象/pack/gc、子模块递归、hooks、稀疏检出、split index、错误参数与锁保护 |
| Windows 读取稳定性 | go -C tools/devtool run . native-read-threads-acceptance：索引/grep 默认与覆盖、宽目录多轮完整输出、仓库文件不变 |
| 游戏资产/LFS | go -C tools/devtool run . native-game-project-acceptance 与 go -C tools/devtool run . native-integrations-acceptance：自带过滤器、模板、游戏配置、失败路径与签名 |
| 登录迁移 | 隔离 DPAPI fixture；Docker 生成 HTTPS/SSH 私有仓库账号，校验旧数据/认证失败保护；不读取个人密码 |
| 安装与更新 | Windows x64 两种 MSI 范围实际完成安装/修复/升级/注入失败回滚/拒绝降级/卸载：用户级 27 项、机器级 25 项；复用须绑定相同不可变输入。ARM64 两种 MSI 仅编译、表和静态检查，未作硬件生命周期验收 |
| Bootstrap | bootstrap_acceptance.ps1 对真实 MSI 的版本读取只允许一个字符串；特殊进程名 `[`、`?` 必须按字面量检测 |
| 原生终端 | 历史证据保留原 Rust/ConPTY 与桌面范围及失败；本轮用户明确豁免后续 `terminal-rust-contracts`、`terminal-native-desktop` 两个专项 lane，单列 waived，不计作通过 |
| 平台 | Windows x64 执行运行契约；Linux x64 与模拟 ARM64 各 52 场景及两路 owned Git/P4 入口；真实 macOS ARM64 51 场景及两路入口，另有独立包/Mach-O/shell 检查。Windows ARM64 仅交叉编译/PE/包静态检查，Darwin CPU affinity 约束不支持 |
| 性能 | 独立批次覆盖 1k/10k/100k 文件和本地 HTTPS；配对次数、实际耗时和每轮输出校验写入报告。不得并发基准造成资源争抢后宣称性能提升 |
| 实际接管 | 新进程 PATH 默认 git、所有入口身份与哈希、原配置/SSH 文件指纹、安装快捷方式和真实新版窗口 |

通过上述范围不表示验证 Git 所有参数组合、所有 IDE/服务商、物理 Linux/Windows ARM64 或全部 Mac 安装/登录/服务生命周期。真实 Mac CLI/runtime 证据不能推定完整 Keychain/服务商登录或 launchd 更新验收。每份证据绑定实际二进制、源码和测试指纹；修改后只复验受影响范围，保留旧证据原身份。功能测试并行，Windows Installer 事务串行。完整 MSI 和大型性能验收不承诺一分钟完成。

## 复现

```powershell
go -C tools/devtool run . native-common-cli-acceptance <git-next/bin/git.exe> --reference <reference-git.exe> --output <new-result.json>
# 定位修复时只运行受影响场景；完整发布仍使用组合验收清单。
go -C tools/devtool run . native-common-cli-acceptance <git-next/bin/git.exe> --reference <reference-git.exe> --case stash_untracked --output <new-result.json>
```

当前状态见 [交付清单](DELIVERY_STATUS.md)，历史大样本性能见 [性能说明](PERFORMANCE.md)。正式发布必须所有未豁免的本地门禁通过，明确授权的豁免与失败历史单列，且只有一次综合版本发布。
