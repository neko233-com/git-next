# Documentation site

GitHub Pages serves the git-next documentation from the `gh-pages` branch root at
<https://neko233-com.github.io/git-next/>. The primary site is a React 19 +
vite-plus (Vite+/Rolldown) application in `github-docs/`. The older Markdown-derived
HTML manuals remain in `docs/site/` as a source and migration reference.

## React 19 documentation app

```sh
cd github-docs
pnpm install
pnpm dev       # local development
pnpm exec vp check
pnpm exec vp test --run
pnpm build     # writes dist/ with base /git-next/
```

From the repository root, `task docs:react:verify` runs check, tests and build in
one gate. `task docs:react:build` only builds. The app uses hash-based navigation,
so deep links work on GitHub Pages without server rewrite rules. Its build output
contains `index.html`, hashed assets and `.nojekyll`.

The default landing page is the download center at `#/download`. It links to the
existing GitHub Release assets and, when `task package:release` has been run, also
publishes the locally built CGO-free Windows x64 preview archive under
`/git-next/downloads/`. The preview package is explicitly labelled as not a new
GitHub Release.

Content lives in `github-docs/src/content.ts`. It is written in Simplified Chinese
for non-technical readers, especially artists and operations staff: plain-language
explanations, copyable commands, step-by-step checklists and frequently asked
questions. It deliberately omits commands the current Go client does not accept,
such as `git commit -a` and `rev-parse --abbrev-ref`.

## Legacy Markdown manuals

The older manuals in `docs/` are still rendered to `docs/site/` by
`task docs` (or `go -C tools/devtool run . render-docs`). The renderer verifies
that every local link resolves. These pages are retained for the detailed
engineering contract, release evidence and compatibility history.

| Page | Manual |
| --- | --- |
| Overview | `README.md` |
| Install | `docs/INSTALLATION.md` |
| Compatibility | `docs/COMPATIBILITY.md` |
| Build | `docs/BUILDING.md` |
| CLI acceptance | `docs/GIT_NEXT_ACCEPTANCE.md` |
| Go implementation | `docs/GO_IMPLEMENTATION.md` |
| Engineering workflow | `docs/AGENT_WORKFLOW.md` |
| Delivery progress | `docs/DELIVERY_STATUS.md` |

## Publishing

```sh
task docs:publish:react              # build React app and publish to gh-pages
task docs:publish:react:dry          # build and report; do not change refs
go -C tools/devtool run . publish-docs --dry-run       # legacy manuals preview
```

`publish-docs --dist github-docs/dist` stages the built React site; without
`--dist` it renders and stages the legacy manuals. Publication builds a commit in
a temporary Git index, so the working tree and current branch are untouched. The
branch is rebuilt from scratch on every run, includes `.nojekyll`, and the push is
a fast-forward. A divergence is reported instead of being overwritten.

All publication steps run locally; no GitHub Actions are used.
