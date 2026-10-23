# git-next agent contract

这是替换用户 Windows Git 的产品仓库，不是只改文档或只加测试的仓库。所有 Agent 修改必须保持以下硬约束。

## 强制进程安全

一次调用导致本机产生 54,914 个 `git.exe`，内存和 CPU 被耗尽。原因是入口转发器把注册表里指向自身的路径当成"另一个文件"。本节的每一条都是那次事故的直接产物，不是风格偏好。

- 一个 Git 进程不允许产生另一个 Git 进程，除非那是它自己的、有界的子进程。入口转发器比较**文件身份**（卷序列号 + 文件索引），不比较字符串：`C:\Git\\cmd\git.exe` 和 `C:\Git\cmd\git.exe` 是同一个文件。
- 转发必须有跳数上限（`GIT_NEXT_BRIDGE_DEPTH`，上限 4）。两个互相指向的文件会被这个上限截断，而不是被"看起来不同"绕过去。
- 拒绝转发时退出 127 并说明修复工具，**绝不启动子进程**。
- 安装程序写入桥接注册前必须拒绝"目标即自身"的映射。
- CRT-free 桥接保持完整 `/O2`。MSVC 14.44 会为结构复制发出 `memcpy`、`memset`、`memmove` 和 `memcmp`；这些符号由 `native-core/launcher/freestanding-memory.c` 提供。不要改用 `/Ob1`，那会关掉转发器自身的内联。四个函数体必须关闭优化，否则编译器会把实现改写成对自身的调用。
- 下次打包前必须重编桥接。`tools/assemble_native_windows.py` 只接受带当前 `freestanding-memory.c` 哈希、`optimizer` 为 `O2`、`ob1_workaround` 为 false，且导入表只有 `kernel32`/`advapi32`（GUI 另加 `user32`）的回执。旧的 `native-core/receipts` 二进制哈希不能当作这次桥接的验收。
- 每个仓库一个常驻进程的默认值是不允许的。`core.fsmonitor` 因此不是默认值：实测每仓库 12.1 MB，且守护进程**在仓库被删除后仍然存活**。谁打开它，谁必须同时提供回收手段（`tools/git_process_watchdog.py --daemons`）。
- 测试必须有进程上限，不能靠信任被测代码。`docker/tests/run.sh` 用 `--pids-limit 128 --memory 2g`；Windows 行为用作业对象限制 64 个进程。一个会无限派生的测试必须死在容器里，而不是死在工作站上。

## 强制运行时默认值

- 默认值只写在一处：`packaging/runtime-defaults/gitconfig`。三处消费者——载荷配置（`tools/assemble_native_windows.py`）、迁移覆盖（`packaging/windows/Takeover.Core.psm1`）、证据复用校验（`tools/native_msi_evidence.py`）——必须全部由它渲染，或与它逐字比对。持有副本是非法的。
- 迁移模块因为语言限制必须保存字面量，所以该字面量与渲染结果逐字比对，漂移即失败。
- 每一项默认值都必须在 `docs/DEFAULTS.md` 里写明理由和实测代价，并由 `tools/check_runtime_defaults.py` 在真实二进制上验证行为。只被"接受"不算生效，必须被证明改变了行为。
- 长路径默认开启：转发器用扩展长度标记 `\\?\`（且必须在规范化之后追加）、`core.longpaths = true`、并报告机器策略 `LongPathsEnabled`。机器策略是管理员设置，产品只报告不代设。
- 改动默认值后运行 `python tools/install_runtime_defaults.py` 让本机生效；它会先试系统级配置，被拒则落到用户全局配置，并逐项问 Git 实际解析出什么。

## 强制产品行为

- 这是独立公开仓库。不要编辑父项目 GitSSS 的实现或生产配置。
- 新仓库默认 `main`；已有分支名和对象格式保持不变。
- 绝不在运行时回退到 Git 或 OpenSSH。用户显式配置的 hook/filter 和基于 Git 的验收夹具，必须与运行时回退区分开。
- 未知命令和不支持的语义必须清晰失败，不得静默忽略选项或修改仓库。
- 保持 `.git` 兼容、ref/index 原子更新、主机密钥校验和已有凭据。改动在临时仓库里验证。
- 不得把凭据或私有部署标识写进源码、夹具、基准或报告。
- 安装命令是 `git`。替换用户 Windows Git 是授权的，但在内嵌兼容性报告说完全对等为假之前必须保持阻断。保留预览、保留回滚元数据，不得接入生产 Git 服务器。
- 未配置的文本一律 LF。`docs/benchmarks/` 与 `packaging/` 下的锁定文件是字节级证据，不得为了换行规范重写它们。

## 强制接口语言

- 一种语言是一份文档：`locales/<code>.json`。`build.rs` 扫描目录并内嵌每份文档，因此加语言不需要碰 Rust 代码、注册表、别名表或测试夹具。
- 英语是源语言。代码字符串是键；文档只列出它要覆盖的消息，缺失的按消息回退到英语。部分翻译是合法的，且优于错误的翻译。
- 语言在文档加入时验证一次。不要把按语言的回归用例加进测试矩阵：测试矩阵验证行为，不验证措辞。
- 每次语言改动必须通过 `python tools/check_locales.py`。它对不可读文档、重复键、源已不再产生的键、丢失的命名占位符、与源不一致的尾随换行、以及未排序的键报错。`--fix` 排序，`--coverage` 报告覆盖率。
- 校验器检查的是配置，不是文采。不要加翻译服务、网络查询或黄金文本比对。
- 永不翻译产品标识、命令与选项名、机器可读输出，以及用户数据（路径、引用、哈希、提交信息）。

## 强制 Windows 安装程序

- 包的职责是声明式的：文件、注册表值、快捷方式。任何有状态的东西——运行时配置、更新注册、接管——都是**集成**，必须幂等，且绝不能让文件安装失败。一个在复制五分钟之后失败的自定义动作，会因为一个无关的问题毁掉一次正确的安装。
- 每个自定义动作先记录自己的失败再上报（`%ProgramData%\git-next\logs\msi-action-<mode>.log`，回退到本地应用数据根）。光秃秃的 `Error 1722` 不是可接受的失败形态。
- 有状态的集成通过 `git install verify` 报告，而不是只靠安装程序退出码。校验器只读、不加锁，因此在产品使用中或其它东西已损坏时依然可用。
- 已接管安装上的 `/x` 被拒绝是有意为之：先回滚接管。不得为了让测试通过而削弱该守卫。
- 用 `tools/msi_lifecycle.py` 测试生命周期，绝不要在可用工具链上覆盖安装。机器作用域只接受 Program Files 内的根目录。

## 强制文档

- `docs/GIT-GUIDE.md` 是取代官方 Git 文档的白话手册，任务优先：没用过 Git 的人要能从头跟到底。它展示的每条命令都必须是二进制真正接受的，有疑问就查 `--help`。
- `docs/WHY-GIT-NEXT.md` 记录实测的加速工作和 Git 命令集之外的检查。数字必须来自 `docs/benchmarks/` 下的基准报告，带报告日期，并且把局限讲得和收益一样直白。
- `docs/LANGUAGE.md` 描述语言文档与设备语言解析。 `docs/DOCUMENTATION_SITE.md` 描述站点如何渲染与发布。`docs/DEFAULTS.md` 记录每一项运行时默认值及其背后的实测。
- 性能或行为声明必须写明作用域：机器、仓库规模、日期。没有作用域的数字不是证据，没有实测的默认值不是决策。
- 在同一次提交里用 `python tools/render_docs.py` 重新生成 `docs/site/`；HTML 是受版本控制的，页面过期是缺陷。
- 用 `python tools/publish_docs.py` 发布。它通过临时索引构建 `gh-pages` 分支，不碰工作区；推送是快进。

## 强制验证

提交前必须通过：

```text
python tools/verify_repo_contract.py        # 本文件的每条硬约束
python tools/verify_freestanding_bridge.py  # /O2 辅助函数不会调用自身，且桥接不链 CRT
python tools/check_runtime_defaults.py      # 默认值：一致性与真实行为
python tools/check_locales.py               # 语言文档
python tools/render_docs.py                 # 站点 HTML 与 Markdown 同步
```

`verify_repo_contract.py` 检查的是本文件写下的规则，而不是某个工具恰好会做的事：默认值只有一处、桥接比较文件身份且有跳数上限、看门狗有上限且能回收守护进程、容器有进程与内存上限、没有 GitHub Actions、提交信息符合约定、受版本控制的文件用 LF。新增硬约束时必须同时更新本文件和该脚本——只写在文档里的约束不会被遵守。

## 发布策略

- 禁止使用 GitHub Actions 打包或消耗 Actions 构建额度。格式化、clippy、测试、打包与校验和全部在本机运行。
- 不得把 GitHub CI 状态当作发布闸门。
- 使用 Conventional Commits 和 `Assisted-by:` 尾注。禁止 force push、amend、squash 和自动 sign-off——sign-off 必须由人完成。
- 禁止发布逐特性的增量版本。完成完整需求范围并逐条验证后，再创建一个整合的标签与包版本。

如果产品行为需要改变这些约束，先更新本文件、`tools/verify_repo_contract.py` 和 README，再改实现。
