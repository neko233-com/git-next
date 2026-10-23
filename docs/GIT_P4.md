# Go git-p4

`git p4` 的产品入口已迁为 Go helper，随五平台原生包部署，不要求 Python。原入口的 Perforce 连接、仓库元数据和 `git-p4.*` 配置名称保留。Git 使用包内相邻的可执行文件；POSIX 的 libexec helper 通过包清单和 runtime 配置定位同一产品的 `bin/git`，不会搜索系统 Git。`p4` 客户端仍是使用 Perforce 集成时的外部依赖，与原入口相同。没有 Perforce 需求的普通 Git 操作不需要安装 `p4`。

## 使用和兼容

```text
git p4 clone --destination workspace //depot/project@all
git p4 sync
git p4 submit
git p4 unshelve --origin refs/remotes/p4/master CHANGE
```

保留 `clone`、`sync`、`rebase`、`submit`、`commit`、`unshelve`、`branches` 入口。`git p4 COMMAND --help` 列出该入口选项。包括快照与完整历史、数字与标签/日期修订区间、增量分块、client view、分支检测、标签、文件类型、编码、LFS、提交与 shelf、编辑器和 hooks。完整选项与验证映射见 [helper 能力表](../tools/git-p4/README.md)。

原仓库中的整行 `[git-p4: ...]` footer 继续作为增量同步依据，兼容旧 `depot-path`、当前 `depot-paths`、字段顺序以及最后出现的字段值。现有 `refs/remotes/p4/` 与 `--import-local` 的 `refs/heads/p4/` 名称保留；升级不重写已有 Git 对象或 refs。`--keep-path`、路径编码、metadata decoding、`mapUser`、`branchList`、标签与大文件配置保持原命名。

提交前校验目标 branch、Perforce 已打开文件和 hooks。`--preserve-user` 在整批提交发生前校验 admin/super 权限与全部作者映射；不会先提交一部分再因未知作者失败。提交模板只收集本次操作拥有的文件，保留其他 depot 中已打开的文件。编辑器、hook 或补丁失败后撤销本次打开的文件和新增文件；`--prepare-p4-only` 明确保留准备状态供人工提交。原 `git-p4.skipSubmitEdit`、`skipSubmitEditCheck`、`skipUserNameCheck` 与 `attemptRCSCleanup` 继续有效。

Windows 使用 Perforce client 的工作目录执行 Perforce 命令，支持 MSYS/Cygwin 风格 Root 与原生 AltRoots。Perforce `p4` 的安装、服务器地址和认证仍按原有 P4 配置管理。认证值不会写入打包回执；不要把密码加入命令行、提交或报告。

子进程默认超时为 30 分钟。长时间导入、同步、提交或编辑可设置 `GIT_P4_TIMEOUT=6h` 等正时长；支持 `h`、`m`、`s` 等 Go duration 单位，非法、零或负值在操作前拒绝。每个子进程单独计时，整个批次可以运行更久。超时会清理本次拥有的进程树；Windows 进程先挂起加入 Job，再恢复运行，POSIX 使用独立进程组。

## 构建、许可证与回滚

```text
go -C tools/devtool run . build-git-p4 --platform windows-x64 --destination target/git-p4/windows-x64
go -C tools/git-p4 test ./...
```

构建支持 Windows x64/ARM64、Linux x64/ARM64、macOS ARM64，使用 `CGO_ENABLED=0`、只读模块与依赖校验。assembler 必须接收 `--git-p4` 的对应目标回执，检查当前源树、产物 SHA、架构及全部许可证文件，并排除 SDK 中旧解释器和旧脚本。打包清单记录 `python_required: false` 和 helper 源码身份；交叉编译回执仅表示构建成功。

实现参照固定 Git 源中的 `git-p4.py`，保留 Simon Hausmann、Trolltech ASA 的 2007 年 MIT 版权。纯 Go 依赖的 BSD 许可证、PATENTS、Go 版本及模块版本随产品放在 `LICENSES/git-p4/`，见 [helper LICENSE](../tools/git-p4/LICENSE)。

回滚采用上一份已验收的完整产品包，不混用不同版本的 helper、core 和回执。原 Git/P4 数据布局没有迁移；回滚不要求重写 Git 对象。已经成功提交到 Perforce 的 changelist 属于正常业务写入，产品回滚不会撤销它。

## 验收边界

独立 P4/P4D 测试在临时 HOME、client 与数据库中使用本地 `rsh` 模式，覆盖真实 clone、增量、二进制、分支祖先、标签、client remap、LFS、submit、shelve/update-shelve、unshelve、rename/delete、编辑器、RCS 清理、hooks 和失败回滚。独立审查回归还覆盖多分支最近 depot、其他 depot 已打开文件、整批作者预检、非管理员拒绝和旧 footer 兼容。原入口一次性对照中，普通历史导入的原始 commit 对象 SHA 相同。

Linux 同环境对照已分别调用原 Python 与 Go 的上游 `t9800`–`t9836` 共 37 套测试，36 套执行，Python 2 专用的 `t9835` 跳过。原入口 379 个成功、16 个标记的已知缺陷；Go 382 个成功、13 个相同已知缺陷，没有新增差异失败。子集分支、同一 changelist 多标签和非法 UTF-16 恢复三项已知缺陷消失，原测试框架将它们记为 XPASS 并返回非零；迁移证据保留原日志和逐项分类。Go 的 metadata suite 只替换 Python launcher 前置检查，保留 12 个行为断言。

差分测试使用的原 Python 与第三方辅助 Python 只属于一次性迁移验证环境，不进入产品或新工具链。现存上游缺陷、平台跳过和 XPASS 与通过项分别记录，完整能力对应见 [helper 验证映射](../tools/git-p4/README.md)。发布还必须通过新产品的原生运行、Rust CLI、平台安装和整合门禁，不能用交叉编译或旧产物替代。
