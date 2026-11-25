# git-next 官网 · 下载中心 · 文档

这是 git-next 的 **GitHub Pages 站点源**，把官网下载中心和使用文档合在同一个静态站里。默认打开的就是下载中心（`#/download`），文档首页保留在 `#/home`。站点面向不懂技术的美术和运营人员，所有安装说明与操作步骤都用简体中文。它是一个独立的前端项目，只依赖本目录内的文件，不和主项目的 Go 构建混合。

技术栈：React 19 + TypeScript + Vite+（vite-plus 本地 CLI，底层为 Vite 8 / Rolldown）。构建结果全部是静态文件，无需服务器。

## 对外入口

- 官网下载中心：<https://neko233-com.github.io/git-next/#/download>
- 文档首页：<https://neko233-com.github.io/git-next/#/home>
- 3 分钟上手：<https://neko233-com.github.io/git-next/#/quickstart>

gitsss 的“git-next”入口应跳转到下载中心，URL 固定为：

```text
https://neko233-com.github.io/git-next/#/download
```

不要在 gitsss 里复制或改写本站的下载链接，也不要指向文档首页；下载卡片上的链接统一指向 GitHub Release。

## 常用命令

在本目录（`git-next/github-docs`）下执行：

| 目的                               | 命令                                                |
| ---------------------------------- | --------------------------------------------------- |
| 安装依赖（首次或锁文件变化后）     | `pnpm install`                                      |
| 本地开发，浏览器打开终端提示的地址 | `pnpm dev`                                          |
| 格式、lint 与类型检查              | `pnpm check`（等价于 `pnpm exec vp check`）         |
| 运行单测（一次执行后退出）         | `pnpm test:run`（等价于 `pnpm exec vp test --run`） |
| 构建静态产物                       | `pnpm build`                                        |
| 本地预览构建结果                   | `pnpm preview`                                      |

`dev`、`check`、`test`、`build` 这四个 npm 脚本内部调用项目本地的 `vp`，因此贡献者不需要安装全局 Vite+ CLI。依赖版本由 `package.json`、`pnpm-lock.yaml` 与 `pnpm-workspace.yaml` 中的覆盖规则锁定：`vite` 指向 `@voidzero-dev/vite-plus-core@8.3.8001`，`vitest` 固定为 `5.1.1`。

## 发布路径

`vite.config.ts` 中设置了 `base: '/git-next/'`，因此产物里的资源地址都是 `/git-next/assets/...`，不含任何本机绝对路径。

1. 运行 `pnpm build`，产物生成在 `dist/`。
2. `dist/.nojekyll` 由 `public/.nojekyll` 自动复制而来，用来阻止 GitHub Pages 忽略下划线开头的文件，发布时不要删除。
3. 把 `dist/` 的**内容**发布到站点根目录下的 `git-next/` 目录。例如使用 `gh-pages` 分支时，应得到 `git-next/index.html`、`git-next/assets/...` 和 `git-next/.nojekyll`。
4. 访问地址为 `https://<账号>.github.io/git-next/`。站点使用 hash 路由（`#/章节id`），不需要为深链配置重写规则，也不会在 GitHub Pages 上出现 404。

本目录不创建 `.github/workflows`；发布流水线由主仓库统一管理。

## 内容组织

- `src/downloads.ts`：下载资产清单与指标数据。所有 href 指向 GitHub Release 的 `latest/download`，新版本发布后只需改这里的文件名。当前资产为 git-next v1.0.5 与 git-terminal v0.2.0。
- `src/downloads.test.ts`：校验每个 href 是 https、资产 id 唯一、包含 5 个 git-next 平台与 2 个 git-terminal 平台，以及两个 Release 的 SHA256SUMS。
- `src/components/DownloadPage.tsx`：官网落地页，包含 Hero、当前版本、平台卡片、安装前先看、指标与特性、按角色入口。
- `src/content.ts`：文档的章节文字、操作步骤、命令速查表和搜索关键字；`download` 章节提供下载页的导航项与目录标题。要改文案优先改这里。
- `src/App.tsx`：hash 路由（无 hash 时落到 `download`）、浅色/深色主题、移动端菜单状态。
- `src/components/`：侧边导航、搜索、正文渲染、页内目录、代码块与复制按钮、内联 SVG 图标。
- `src/styles.css`：官网落地页与文档样式、主题变量与响应式布局。
- `src/content.test.ts`：校验章节 id 唯一、步骤与检查单非空、速查表不含当前实现没有的命令，并确认这些命令被明确标注为“不支持”。

## 内容边界

下载中心只列出 GitHub Release 里已经存在的资产；Go 单文件客户端尚未与系统 Git 做到全量 parity，L0 为 39/39、full 为 57/100。站点不提供性能对比或推测数据，系统 Git 也不需要为了安装 git-next 而卸载。主题默认是浅色，切换后写入浏览器本地存储。本站不收集或上传任何信息。
