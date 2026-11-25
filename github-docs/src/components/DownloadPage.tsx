import type { Section } from "../content";
import {
  checksumDownloads,
  gitNextDownloads,
  metrics,
  previewDownloads,
  RELEASES,
  terminalDownloads,
  type DownloadAsset,
} from "../downloads";
import { Icon } from "./Icon";

type DownloadPageProps = {
  section: Section;
  onNavigate: (id: string) => void;
};

const roleCards = [
  {
    id: "quickstart",
    title: "3 分钟上手",
    text: "打开终端、进入项目、看状态、保存一次修改。四步走完就会用。",
    action: "开始上手",
  },
  {
    id: "artists",
    title: "给美术",
    text: "贴图、模型改了怎么保存，改错了怎么找回旧版本，都有逐步说明。",
    action: "看美术指南",
  },
  {
    id: "ops",
    title: "给运营",
    text: "配置表和文案怎么拿到最新版，怎么把改动安全地交给团队。",
    action: "看运营指南",
  },
];

const featureCards = [
  {
    title: "原生 Go 客户端",
    text: "单一可执行文件，安装和分发都不再依赖一整套运行时。",
  },
  {
    title: "CGO_ENABLED=0",
    text: "不依赖 C 工具链，Windows、macOS、Linux 交叉构建更省事。",
  },
  {
    title: "git-terminal",
    text: "保留熟悉的终端工作方式，Windows 资源管理器里也能右键打开。",
  },
  {
    title: "Git LFS",
    text: "贴图、模型、音频等大文件按 Git LFS 方式管理，适合游戏项目。",
  },
  {
    title: "10 种界面语言",
    text: "客户端界面可切换语言，团队成员可以按自己的习惯使用。",
  },
  {
    title: "写给非程序员",
    text: "文档按美术、运营和日常场景组织，命令可以直接复制粘贴。",
  },
];

function scrollToAnchor(id: string) {
  const target = document.getElementById(id);
  target?.scrollIntoView({ behavior: "smooth", block: "start" });
  target?.focus({ preventScroll: true });
}

function AssetLink({ asset, primary = false }: { asset: DownloadAsset; primary?: boolean }) {
  return (
    <a
      className={primary ? "download-link download-link-primary" : "download-link"}
      href={asset.href}
      target="_blank"
      rel="noopener noreferrer"
    >
      {primary ? "下载" : "获取"}
      <Icon name="arrow" size={15} />
    </a>
  );
}

function Heading({ id, section, anchor }: { id: string; section: Section; anchor: string }) {
  const block = section.blocks.find((item) => item.kind === "heading" && item.anchor === anchor);
  return (
    <h2 id={id} className="site-section-title">
      {block?.kind === "heading" ? block.text : anchor}
    </h2>
  );
}

export function DownloadPage({ section, onNavigate }: DownloadPageProps) {
  return (
    <div className="site">
      <section className="site-hero" aria-labelledby="site-hero-title">
        <div className="site-hero-glow" aria-hidden="true" />
        <div className="site-hero-inner">
          <span className="site-kicker">{section.kicker}</span>
          <h1 id="site-hero-title">{section.title}</h1>
          <p className="site-hero-summary">
            {section.summary}下载页列出的是 GitHub Release
            上已经发布的资产；能力与边界请以文档中的说明为准。
          </p>
          <div className="site-hero-actions">
            <button
              type="button"
              className="site-button site-button-primary"
              onClick={() => scrollToAnchor("download-platforms")}
            >
              下载安装包
              <Icon name="arrow" size={17} />
            </button>
            <button
              type="button"
              className="site-button site-button-secondary"
              onClick={() => onNavigate("quickstart")}
            >
              先看 3 分钟上手
            </button>
          </div>
          <p className="site-hero-footnote">
            Windows 提供 MSI 安装包，macOS 与 Linux 提供 tar.gz 压缩包。
          </p>
        </div>
      </section>

      <section
        id="download-platforms"
        className="site-section"
        tabIndex={-1}
        aria-labelledby="download-platforms-title"
      >
        <Heading id="download-platforms-title" section={section} anchor="platforms" />
        <div className="version-panel">
          <div className="version-panel-head">
            <span className="status-dot" aria-hidden="true" />
            <strong>当前 Release 版本</strong>
            <span>以下均为 GitHub Release 现有资产</span>
          </div>
          <div className="version-grid">
            <div className="version-item">
              <span>git-next</span>
              <strong>{RELEASES.gitNext}</strong>
              <small>原生 Go 客户端</small>
            </div>
            <div className="version-item">
              <span>git-terminal</span>
              <strong>{RELEASES.terminal}</strong>
              <small>终端与右键入口</small>
            </div>
            <div className="version-item">
              <span>兼容性基线</span>
              <strong>39/39</strong>
              <small>L0 用例通过；full 57/100</small>
            </div>
          </div>
          <p className="version-note">
            Go 单文件客户端仍在补齐与系统 Git 的能力范围，尚未做到全量 parity； 当前发布资产与系统
            Git 可以同时使用，详细边界见“版本与边界”。
          </p>
        </div>

        <div className="download-columns">
          <div className="download-panel">
            <div className="download-panel-head">
              <h3>git-next 客户端</h3>
              <p>原生 Go 客户端 · {RELEASES.gitNext} · MSI / tar.gz</p>
            </div>
            <ul className="platform-list">
              {gitNextDownloads.map((asset) => (
                <li className="platform-item" key={asset.id}>
                  <div className="platform-info">
                    <strong>{asset.label}</strong>
                    <code>{asset.detail}</code>
                  </div>
                  <AssetLink asset={asset} primary />
                </li>
              ))}
            </ul>
          </div>

          <div className="download-panel">
            <div className="download-panel-head">
              <h3>git-terminal 终端</h3>
              <p>终端入口 · {RELEASES.terminal} · ZIP 压缩包，不是 MSI</p>
            </div>
            <ul className="platform-list">
              {terminalDownloads.map((asset) => (
                <li className="platform-item" key={asset.id}>
                  <div className="platform-info">
                    <strong>{asset.label}</strong>
                    <code>{asset.detail}</code>
                  </div>
                  <AssetLink asset={asset} />
                </li>
              ))}
            </ul>
          </div>
        </div>

        <div className="checksum-row">
          <strong>下载后先校验</strong>
          <span>校验文件与安装包在同一个 GitHub Release 中：</span>
          {checksumDownloads.map((asset) => (
            <a
              className="checksum-link"
              href={asset.href}
              key={asset.id}
              target="_blank"
              rel="noopener noreferrer"
            >
              {asset.label}
              <Icon name="arrow" size={14} />
            </a>
          ))}
        </div>
      </section>

      <section
        id="download-go-preview"
        className="site-section"
        tabIndex={-1}
        aria-labelledby="download-go-preview-title"
      >
        <Heading id="download-go-preview-title" section={section} anchor="go-preview" />
        <div className="download-panel">
          <div className="download-panel-head">
            <h3>本地构建的 Go-only 包</h3>
            <p>Windows x64 · CGO_ENABLED=0 · 仅作预览，不替代上面的正式 Release 资产</p>
          </div>
          <ul className="platform-list">
            {previewDownloads.map((asset) => (
              <li className="platform-item" key={asset.id}>
                <div className="platform-info">
                  <strong>{asset.label}</strong>
                  <code>{asset.detail}</code>
                </div>
                <AssetLink asset={asset} />
              </li>
            ))}
          </ul>
          <p className="version-note">
            这个包直接来自仓库的 <code>task package:release</code>，含 Go 客户端与 git-terminal。
            它还没有进入 GitHub Release，也不改变当前 v1.0.5 正式包的支持范围。
          </p>
        </div>
      </section>

      <section
        id="download-before-install"
        className="site-section"
        tabIndex={-1}
        aria-labelledby="download-before-install-title"
      >
        <Heading id="download-before-install-title" section={section} anchor="before-install" />
        <div className="note-grid">
          <article className="note-card">
            <h3>系统 Git 不用卸载</h3>
            <p>
              git-next 可以和已有的 Git 共存，不需要先卸载系统里的 Git。现有的项目和命令仍然可用。
            </p>
          </article>
          <article className="note-card">
            <h3>Windows：选 user 还是系统 MSI</h3>
            <p>
              user MSI 只安装给当前账号，不需要管理员权限，个人电脑优先选它；系统 MSI
              安装到整台机器、对所有账号生效，需要管理员权限，适合运维统一部署。拿不准就选 user
              MSI。
            </p>
          </article>
          <article className="note-card">
            <h3>macOS / Linux：解压后加入 PATH</h3>
            <p>
              下载 tar.gz 后先解压，把解压出来的目录加入
              PATH，再打开终端运行版本命令确认可用。只在当前窗口临时生效的做法不适合长期使用。
            </p>
          </article>
          <article className="note-card">
            <h3>git-terminal 是压缩包</h3>
            <p>
              git-terminal 提供的是 Windows 平台的 zip 压缩包，不是 MSI
              安装包。下载后解压到固定目录，再从目录里启动；source.zip 是源码，install_bundle.ps1
              是辅助安装脚本。
            </p>
          </article>
        </div>
      </section>

      <section
        id="download-facts"
        className="site-section"
        tabIndex={-1}
        aria-labelledby="download-facts-title"
      >
        <Heading id="download-facts-title" section={section} anchor="facts" />
        <div className="metric-grid">
          {metrics.map((metric) => (
            <div className="metric-card" key={metric.label}>
              <strong>{metric.value}</strong>
              <span>{metric.label}</span>
              <small>{metric.note}</small>
            </div>
          ))}
        </div>
        <div className="feature-grid">
          {featureCards.map((card) => (
            <article className="feature-card" key={card.title}>
              <h3>{card.title}</h3>
              <p>{card.text}</p>
            </article>
          ))}
        </div>
      </section>

      <section
        id="download-roles"
        className="site-section"
        tabIndex={-1}
        aria-labelledby="download-roles-title"
      >
        <Heading id="download-roles-title" section={section} anchor="roles" />
        <div className="role-grid">
          {roleCards.map((card) => (
            <article className="role-card" key={card.id}>
              <h3>{card.title}</h3>
              <p>{card.text}</p>
              <button type="button" onClick={() => onNavigate(card.id)}>
                {card.action}
                <Icon name="arrow" size={16} />
              </button>
            </article>
          ))}
        </div>
      </section>
    </div>
  );
}
