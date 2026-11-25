# git-next 文档站

这是 git-next 的 **GitHub Pages 文档源**，面向不懂技术的美术和运营人员，用简体中文说明日常操作。它是一个独立的前端项目，只依赖本目录内的文件，不和主项目的 Go 构建混合。

技术栈：React 19 + TypeScript + Vite+（vite-plus 本地 CLI，底层为 Vite 8 / Rolldown）。构建结果全部是静态文件，无需服务器。

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
4. 访问地址为 `https://<账号>.github.io/git-next/`。文档使用 hash 路由（`#/章节id`），不需要为深链配置重写规则，也不会在 GitHub Pages 上出现 404。

本目录不创建 `.github/workflows`；发布流水线由主仓库统一管理。

## 内容组织

- `src/content.ts`：全部章节文字、操作步骤、命令速查表和搜索关键字。要改文案或补命令，优先改这里。
- `src/App.tsx`：hash 路由、浅色/深色主题、移动端菜单状态。
- `src/components/`：侧边导航、搜索、正文渲染、页内目录、代码块与复制按钮、内联 SVG 图标。
- `src/styles.css`：样式、主题变量与响应式布局。
- `src/content.test.ts`：校验章节 id 唯一、步骤与检查单非空、速查表不含当前实现没有的命令，并确认这些命令被明确标注为“不支持”。

主题默认是浅色，切换后写入浏览器本地存储。文档站不收集或上传任何信息；截图反馈时按“语言与帮助”章节提供的清单即可，不需要记录终端内容到日志。
