# git-next

[English](README.md) · [HTML 手册](docs/site/index.html)

基于 Gitoxide 与 Russh 的独立 Rust Git 客户端，目标安装命令为 **git**。新仓库默认 main，运行时不调用外部 Git/OpenSSH 兜底。

**当前仍是实验预览，不能完整替代 Git。** push、更新工作区的 pull/checkout、merge/rebase、LFS 和完整生态兼容尚未完成。因此安装器只安装独立的 git-next，git 同名预览放在 PATH 外；覆盖安装参数会明确拒绝。不要指定为 IDE 或生产 Git 服务器的默认 Git。

平台目标：Windows x64/ARM64、Linux x64/ARM64、macOS ARM64（不提供 Intel Mac）。编译通过与真实平台运行验收分别记录，不承诺所有操作都更快。

查看 [兼容矩阵](docs/COMPATIBILITY.md)、[安装卸载](docs/INSTALLATION.md)、[构建验证](docs/BUILDING.md) 和 [性能测量](docs/PERFORMANCE.md)。支持安装脚本自动尝试固定官方提交 SHA 的 GitHub 镜像传输；不会修改现有账号、凭据、Git 配置或仓库。安装需要 Rust 1.96+ 和本机编译工具链。

PowerShell：下载并检查 install.ps1 后执行 `./install.ps1`。Unix：下载并检查 install.sh 后执行 `sh install.sh`。新终端运行 `git-next compatibility --json` 查看真实兼容状态。

## 仓库终端

`git-terminal --cwd 路径` 打开仓库终端。Windows 优先 PowerShell 7，回退 Windows PowerShell，支持开始菜单和可选目录右键入口；Unix 使用本机 Shell。预览客户端独立提供，原 Git 与 Git Bash 保留。它尚不提供完整 Bash/POSIX 模拟，不能把此终端入口等同于完整 Git Bash 替代。
