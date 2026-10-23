# Go git-p4

`git-p4` 是随原生产品部署的 Go 辅助程序，保留 Perforce 与 Git 双向集成入口。它不加载 Python，不寻找系统 Git；优先使用相邻 Git，POSIX 的 `runtime/usr/libexec/git-core/git-p4` 则通过包清单和 runtime 配置定位同一产品的 `bin/git`。Perforce 的 `p4` 客户端仍是可选集成的外部依赖，与原入口相同。

协议和行为参照固定源版本的 [git-p4.py](https://github.com/git-for-windows/git/blob/2b8a3ab140826ac423c2845ef81d4c6ac4f7bf3c/git-p4.py)。保留 Simon Hausmann、Trolltech ASA 的 2007 年 MIT 版权，见 [LICENSE](LICENSE)。文本编码使用纯 Go `golang.org/x/text`；Windows 子进程生命周期使用 `golang.org/x/sys`。构建包收集这两个模块及 Go 标准库的 BSD 许可证、PATENTS 和依赖版本。

## 构建和验证

从 `git-next` 根目录运行：

```text
go -C tools/devtool run . build-git-p4 --platform windows-x64 --destination target/git-p4/windows-x64
go -C tools/git-p4 test ./...
```

支持 `windows-x64`、`windows-arm64`、`linux-x64`、`linux-arm64`、`macos-arm64`。`build-git-p4` 使用 `CGO_ENABLED=0`、只读模块和依赖校验，记录全部源文件、依赖许可证及产物 SHA-256。交叉编译回执始终是 `built-not-runtime-accepted`。

真实 Perforce 测试要求显式指定三个测试程序：`GIT_P4_TEST_P4`、`GIT_P4_TEST_P4D`、`GIT_P4_TEST_GIT`。测试使用临时数据库、独立 HOME/USERPROFILE/P4 配置以及 P4D 的本地 `rsh` 模式，不监听网络端口，不连接个人 Perforce 账户。普通单元测试不要求这些程序。`GIT_P4_GIT`、`GIT_P4_P4`、`GIT_P4_SHELL` 仅用于明确指定开发夹具中的程序。

每个子进程默认最多运行 30 分钟。大仓导入、同步、提交和编辑器可通过 `GIT_P4_TIMEOUT=6h` 等正时长延长；支持 Go duration 的 `h`、`m`、`s` 等单位，非法、零或负值会在操作前拒绝。超时会清理本次拥有的子进程树；Windows 在挂起状态下加入 Job 后再恢复，POSIX 使用独立进程组。该限制只约束单个子进程，不限制整个导入的总耗时。

## 能力与验证对应

| 入口或能力 | 实现 | 已有针对性验证 |
| --- | --- | --- |
| `clone`、`sync`；完整历史、快照、修订区间、增量与二进制文件 | `import.go`、`changes.go` | `TestP4LiveCloneAndIncrementalSync`；一次性原入口差分得到相同原始 commit 对象 SHA |
| `--branch`、`--import-local`、`--bare`、`--destination`、`--changesfile`、`--max-changes` | `main.go`、`import.go` | 上游 `t9800`、`t9806`、`t9808`；独立 local namespace 回归 |
| `--changes-block-size`、服务器行数限制后的减小分块 | `changes.go` | 上游 `t9818` 的 14 个分块、修订区间与限制断言 |
| `--detect-branches`、Perforce branch view、`git-p4.branchList`、分支祖先 | `branches.go` | 上游 `t9801`；真实分支祖先与最近 depot 独立回归 |
| `--use-client-spec`、client view 重映射与排除、`-/` 排除路径 | `import.go`、`main.go` | 上游 `t9801`、`t9809`、`t9817`、`t9821`，包含映射后分支继承及增量 rebase |
| `--keep-path`、路径编码、metadata strict/passthrough/fallback、`mapUser` | `encoding.go`、`driver.go`、`import.go` | 上游 `t9822`、`t9828`、`t9836`；CP1252、CP850、非法字节和旧 footer 回归 |
| `--detect-labels`、`--import-labels`、`--export-labels`、标签过滤和忽略列表 | `import.go`、`export.go` | 上游 `t9804`、`t9811`；真实导出、不覆盖既有标签与 dry-run 回归 |
| `GitLFS`、`MockLFS`、扩展名/大小/压缩阈值、存储目录与可选推送 | `largefiles.go` | 上游 `t9823`、`t9824`；真实指针和内容对象，压缩阈值使用 owned Git 的 ZIP zlib 结果 |
| `submit` / `commit`；dry-run、prepare-only、提交选择、变更类型、rename/copy | `export.go` | 上游 `t9807`、`t9814`、`t9815`、`t9827`、`t9829`、`t9830`、`t9831`；真实内容读回和失败回滚 |
| `--shelve`、`--update-shelve`、`--preserve-user`、冲突选择、禁用同步/rebase | `export.go` | 上游 `t9807`、`t9813`、`t9815`；真实 update-shelve、全批作者预检和非管理员拒绝 |
| `p4-pre-submit`、`p4-prepare-changelist`、`p4-changelist`、`p4-post-changelist`；`--no-verify` | `export.go` | 真实编辑器/hooks 拒绝、绕过、copy 回滚和其他 depot 已打开文件保留回归 |
| `P4EDITOR` / Git editor、未编辑确认、`skipSubmitEdit`、`skipSubmitEditCheck` | `export.go` | 上游 `t9805`、`t9820`；未编辑确认和 RCS 清理真实回归 |
| `unshelve`、指定 origin、父快照、已有分支备份 | `export.go` | 上游 `t9832`；真实 shelf 和父快照内容读回 |
| `rebase`、`branches`、origin/p4 更新 | `import.go`、`origin.go` | 上游 `t9800`、`t9801`、`t9807`；最终产品另验空 PATH 的两个 helper 入口 |
| P4 `-G` marshal 字典、引用、整数、流式多记录、二进制内容 | `internal/marshal` | Python wire-format 固定金样、恶意长度/引用/嵌套拒绝、真实 P4/P4D 数据流 |
| 子进程退出和取消 | `child_windows.go`、`child_unix.go` | Windows Job / POSIX process group 清理；真实 P4/P4D 测试退出 |

## 上游差分结果

Linux 隔离环境使用同一参考 Git 2.39.5、官方 Perforce r24.2 客户端/服务器和固定上游测试源码，对原 Python 与 Go 各调用 `t9800`–`t9836` 的 37 套测试。36 套实际执行；Python 2 专用的 `t9835` 因无 Python 2 跳过。Go 的 `t9836` 仅调整 Python launcher 前置检查，保留全部 12 个行为断言。

原入口为 379 个成功、16 个上游标记的已知缺陷；Go 为 382 个成功、13 个相同已知缺陷，新增差异失败为 0。额外成功的三项是子集分支、同一 changelist 的两个标签、非法 UTF-16 内容恢复。原测试框架把这三项已知缺陷消失记为 XPASS，因此对应三套原始退出码仍为 1；证据按 TAP 断言与原入口逐项比较，保留原日志，不把它们伪装为原始全绿。锁定文件、overlay client view、大小写路径自动检测和已有 RCS 限制中的 13 项仍与原入口一致。

Windows 独立真实 P4/P4D 测试及 marshal 单元测试共 26 项通过，另外验证原生 AltRoots、权限/作者预检、hooks 回滚和包内 Git 定位。差分夹具中的原 Python 与辅助 Python 只用于这次迁移对照，不进入产品或正式 Go 工具链。上述行为证据与五平台包的原生运行、Rust CLI、平台安装和整合门禁分别记录；交叉编译不能替代运行验收。
