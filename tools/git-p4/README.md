# Go git-p4

`git-p4` 是随原生产品部署的 Go 辅助程序，保留 Perforce 与 Git 双向集成入口。它不加载 Python，不寻找系统 Git；Git 使用相邻的产品可执行文件。Perforce 的 `p4` 客户端仍是可选集成的外部依赖，与原入口相同。

协议和行为参照固定源版本的 [git-p4.py](https://github.com/git-for-windows/git/blob/2b8a3ab140826ac423c2845ef81d4c6ac4f7bf3c/git-p4.py)。保留 Simon Hausmann、Trolltech ASA 的 2007 年 MIT 版权，见 [LICENSE](LICENSE)。文本编码使用纯 Go `golang.org/x/text`；Windows 子进程生命周期使用 `golang.org/x/sys`。构建包收集这两个模块及 Go 标准库的 BSD 许可证、PATENTS 和依赖版本。

## 构建和验证

从 `git-next` 根目录运行：

```text
go -C tools/devtool run . build-git-p4 --platform windows-x64 --destination target/git-p4/windows-x64
go -C tools/git-p4 test ./...
```

支持 `windows-x64`、`windows-arm64`、`linux-x64`、`linux-arm64`、`macos-arm64`。`build-git-p4` 使用 `CGO_ENABLED=0`、只读模块和依赖校验，记录全部源文件、依赖许可证及产物 SHA-256。交叉编译回执始终是 `built-not-runtime-accepted`。

真实 Perforce 测试要求显式指定三个测试程序：`GIT_P4_TEST_P4`、`GIT_P4_TEST_P4D`、`GIT_P4_TEST_GIT`。测试使用临时数据库、独立 HOME/USERPROFILE/P4 配置以及 P4D 的本地 `rsh` 模式，不监听网络端口，不连接个人 Perforce 账户。普通单元测试不要求这些程序。`GIT_P4_GIT`、`GIT_P4_P4`、`GIT_P4_SHELL` 仅用于明确指定开发夹具中的程序。

## 能力与验证对应

| 入口或能力 | 实现 | 已有针对性验证 |
| --- | --- | --- |
| `clone`、`sync`；完整历史、快照、修订区间、增量与二进制文件 | `import.go`、`changes.go` | `TestP4LiveCloneAndIncrementalSync`；一次性原入口差分得到相同原始 commit 对象 SHA |
| `--branch`、`--import-local`、`--bare`、`--destination`、`--changesfile`、`--max-changes` | `main.go`、`import.go` | 参数/元数据测试；完整上游组合仍需平台验收 |
| `--changes-block-size`、服务器行数限制后的减小分块 | `changes.go` | 编译与普通增量夹具；限制组专项仍需验收 |
| `--detect-branches`、Perforce branch view、`git-p4.branchList`、分支祖先 | `branches.go` | `TestP4LiveBranchAncestryAndLabels` |
| `--use-client-spec`、client view 重映射与排除、`-/` 排除路径 | `import.go`、`main.go` | `TestP4LiveLFSAndMappedClientView`；短选项、长选项缩写测试 |
| `--keep-path`、路径编码、metadata strict/passthrough/fallback、`mapUser` | `encoding.go`、`driver.go`、`import.go` | CP1252、CP850、非法字节转义、strict 失败、Git 路径引用测试 |
| `--detect-labels`、`--import-labels`、`--export-labels`、标签过滤和忽略列表 | `import.go`、`export.go` | 真实标签导入和对应提交验证；导出组合仍需专项验收 |
| `GitLFS`、`MockLFS`、扩展名/大小/压缩阈值、存储目录与可选推送 | `largefiles.go` | 真实 Git LFS 指针、内容寻址对象和 `.gitattributes` 验证；推送组合仍需专项验收 |
| `submit` / `commit`；dry-run、prepare-only、提交选择、变更类型、rename/copy | `export.go` | `TestP4LiveSubmitShelveAndUnshelve` 实际提交和内容读回；复杂类型组合仍需平台验收 |
| `--shelve`、`--update-shelve`、`--preserve-user`、冲突选择、禁用同步/rebase | `export.go` | 真实 shelve 和回滚；更新 shelf、权限和冲突组合仍需专项验收 |
| `p4-pre-submit`、`p4-prepare-changelist`、`p4-changelist`、`p4-post-changelist`；`--no-verify` | `export.go` | 已接入 Git hook runner；完整 hooks 拒绝/绕过/编辑器组合仍需专项验收 |
| `P4EDITOR` / Git editor、未编辑确认、`skipSubmitEdit`、`skipSubmitEditCheck` | `export.go` | skip-submit-edit 真实提交覆盖；交互编辑专项仍需验收 |
| `unshelve`、指定 origin、父快照、已有分支备份 | `export.go` | 真实 shelf 到 Git 文件内容读回 |
| `rebase`、`branches`、origin/p4 更新 | `import.go`、`origin.go` | 实现已接入；跨 Git origin 和冲突专项仍需验收 |
| P4 `-G` marshal 字典、引用、整数、流式多记录、二进制内容 | `internal/marshal` | Python wire-format 固定金样、恶意长度/引用/嵌套拒绝、真实 P4/P4D 数据流 |
| 子进程退出和取消 | `child_windows.go`、`child_unix.go` | Windows Job / POSIX process group 清理；真实 P4/P4D 测试退出 |

上表将实现与实际验证分开。不能用这些定向测试替代全部上游 `t9800`–`t9836` 场景，也不能用交叉编译代替五平台原生运行验收。最终删旧入口、移除解释器和发布由仓库整体验收门禁决定。
