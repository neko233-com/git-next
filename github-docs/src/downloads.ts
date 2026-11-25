/**
 * 官网下载中心的资产清单。
 * 所有链接都指向 GitHub Release 的 latest/download，便于发布新版本时无需改站。
 */

export type DownloadKind = "installer" | "archive" | "source" | "script" | "checksum";

export type DownloadAsset = {
  id: string;
  app: "git-next" | "git-terminal";
  label: string;
  detail: string;
  kind: DownloadKind;
  href: string;
};

export const RELEASES = {
  gitNext: "v1.0.5",
  terminal: "v0.2.0",
} as const;

const GIT_NEXT_BASE = "https://github.com/neko233-com/git-next/releases/latest/download";
const TERMINAL_BASE = "https://github.com/neko233-com/git-terminal/releases/latest/download";

/** git-next 当前 GitHub Release 中的五个平台安装包。 */
export const gitNextDownloads: DownloadAsset[] = [
  {
    id: "git-next-windows-x64",
    app: "git-next",
    label: "Windows x64",
    detail: "git-next-1.0.5-windows-x64.msi",
    kind: "installer",
    href: `${GIT_NEXT_BASE}/git-next-1.0.5-windows-x64.msi`,
  },
  {
    id: "git-next-windows-arm64",
    app: "git-next",
    label: "Windows ARM64",
    detail: "git-next-1.0.5-windows-arm64.msi",
    kind: "installer",
    href: `${GIT_NEXT_BASE}/git-next-1.0.5-windows-arm64.msi`,
  },
  {
    id: "git-next-macos-arm64",
    app: "git-next",
    label: "macOS ARM64",
    detail: "git-next-1.0.5-macos-arm64.tar.gz",
    kind: "archive",
    href: `${GIT_NEXT_BASE}/git-next-1.0.5-macos-arm64.tar.gz`,
  },
  {
    id: "git-next-linux-x64",
    app: "git-next",
    label: "Linux x64",
    detail: "git-next-1.0.5-linux-x64.tar.gz",
    kind: "archive",
    href: `${GIT_NEXT_BASE}/git-next-1.0.5-linux-x64.tar.gz`,
  },
  {
    id: "git-next-linux-arm64",
    app: "git-next",
    label: "Linux ARM64",
    detail: "git-next-1.0.5-linux-arm64.tar.gz",
    kind: "archive",
    href: `${GIT_NEXT_BASE}/git-next-1.0.5-linux-arm64.tar.gz`,
  },
];

/** git-terminal 当前 GitHub Release 中的压缩包与辅助文件。 */
export const terminalDownloads: DownloadAsset[] = [
  {
    id: "terminal-windows-x64",
    app: "git-terminal",
    label: "Windows x64",
    detail: "git-terminal-0.2.0-windows-x64.zip",
    kind: "archive",
    href: `${TERMINAL_BASE}/git-terminal-0.2.0-windows-x64.zip`,
  },
  {
    id: "terminal-windows-arm64",
    app: "git-terminal",
    label: "Windows ARM64",
    detail: "git-terminal-0.2.0-windows-arm64.zip",
    kind: "archive",
    href: `${TERMINAL_BASE}/git-terminal-0.2.0-windows-arm64.zip`,
  },
  {
    id: "terminal-source",
    app: "git-terminal",
    label: "源码",
    detail: "source.zip",
    kind: "source",
    href: `${TERMINAL_BASE}/source.zip`,
  },
  {
    id: "terminal-install-bundle",
    app: "git-terminal",
    label: "安装脚本",
    detail: "install_bundle.ps1",
    kind: "script",
    href: `${TERMINAL_BASE}/install_bundle.ps1`,
  },
];

/** 校验文件单独展示，避免混在平台卡片里。 */
export const checksumDownloads: DownloadAsset[] = [
  {
    id: "git-next-checksums",
    app: "git-next",
    label: "git-next SHA256SUMS",
    detail: "SHA256SUMS",
    kind: "checksum",
    href: `${GIT_NEXT_BASE}/SHA256SUMS`,
  },
  {
    id: "terminal-checksums",
    app: "git-terminal",
    label: "git-terminal SHA256SUMS",
    detail: "SHA256SUMS",
    kind: "checksum",
    href: `${TERMINAL_BASE}/SHA256SUMS`,
  },
];

/**
 * 本地 CGO=0 构建产物，发布到本站 /git-next/downloads/。
 * 这是预览包，不冒充 GitHub Release 的正式资产。
 */
export const previewDownloads: DownloadAsset[] = [
  {
    id: "go-preview-windows-x64",
    app: "git-next",
    label: "Windows x64 预览包",
    detail: "git-next-1.0.5-windows-x64.zip",
    kind: "archive",
    href: "https://neko233-com.github.io/git-next/downloads/git-next-1.0.5-windows-x64.zip",
  },
  {
    id: "go-preview-checksums",
    app: "git-next",
    label: "预览包 SHA256SUMS",
    detail: "SHA256SUMS",
    kind: "checksum",
    href: "https://neko233-com.github.io/git-next/downloads/SHA256SUMS",
  },
];

/** 全部资产，用于校验 id 唯一与链接来源。 */
export const allDownloads: DownloadAsset[] = [
  ...gitNextDownloads,
  ...terminalDownloads,
  ...checksumDownloads,
];

export type Metric = {
  value: string;
  label: string;
  note: string;
};

/** 只使用仓库与 Release 中已有的事实，不填推测的性能数据。 */
export const metrics: Metric[] = [
  { value: "11.3 MB", label: "Go 客户端体积", note: "原生 Go 构建的单文件客户端" },
  { value: "39/39", label: "L0 用例通过", note: "核心流程的兼容性基线" },
  { value: "57/100", label: "full 用例现状", note: "完整用例仍在持续补齐" },
  { value: "0", label: "CGO_ENABLED", note: "不依赖 C 工具链交叉编译" },
  { value: "10", label: "界面语言", note: "客户端内可切换的语言数量" },
];
