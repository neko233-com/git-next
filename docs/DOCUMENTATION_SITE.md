# Documentation site

The manual of git-next is published as a GitHub Pages site, so it can be read without cloning the repository. The site is rendered from the Markdown manuals in `docs/` and from the README, which means there is only one source for each page: a change to a manual is the change to the published page.

## Pages

| Page | Manual |
| --- | --- |
| Overview | `README.md` |
| Install | `docs/INSTALLATION.md` |
| Compatibility | `docs/COMPATIBILITY.md` |
| Build | `docs/BUILDING.md` |
| Performance | `docs/PERFORMANCE.md` |
| Acceptance | `docs/ACCEPTANCE.md` |
| Test coverage | `docs/TEST_COVERAGE.md` |
| Complete takeover | `docs/TAKEOVER.md` |
| Native LFS | `docs/LFS.md` |
| Git-Terminal | `docs/TERMINAL.md` |
| Game projects | `docs/GAME_PROJECTS.md` |
| Read commands | `docs/READ_COMMANDS.md` |
| Restore and reset | `docs/RESTORATION.md` |
| LF policy | `docs/LINE_ENDINGS.md` |
| Native discovery | `docs/NATIVE_DISCOVERY.md` |
| Credential support | `docs/CREDENTIALS.md` |
| Native merge | `docs/MERGE.md` |
| MSI and updates | `docs/UPDATES.md` |
| Interface languages | `docs/LANGUAGE.md` |
| Native core integration | `docs/NATIVE_CORE.md` |
| Engineering workflow | `docs/AGENT_WORKFLOW.md` |
| Delivery progress | `docs/DELIVERY_STATUS.md` |
| Runtime sources and licenses | `docs/CORRESPONDING_SOURCES.md` |

## Rendering

```sh
go -C tools/devtool run . render-docs
```

The renderer writes every page into `docs/site/` and then verifies that every local link of every page resolves. A broken link fails the run, so a page that is published always points at pages that exist.

## Publishing

```sh
go -C tools/devtool run . publish-docs            # render and publish
go -C tools/devtool run . publish-docs --dry-run  # render and report; do not create commits, update refs or push
```

The site is published from the `gh-pages` branch, which Pages serves from the branch root. This repository builds, tests and packages everything locally, so the publication is done by the same script that a workflow would run, on the machine that owns the release.

The script renders the pages, builds a commit that contains only the rendered site, and pushes it. The commit is assembled in a temporary index with git's plumbing commands, so the repository's index, work tree and current branch are never touched: a publication can run at any time, including with uncommitted work in the tree.

The branch carries the rendered pages only. A leftover file from an earlier publication would be served forever, so the branch is rebuilt from scratch on every run. An empty `.nojekyll` marker is published with the pages, because they are already rendered and Jekyll would otherwise rewrite them.

The push is a fast-forward. The publication commit always descends from the published tip, and a divergence is reported instead of being overwritten.

## Published site

<https://neko233-com.github.io/git-next/>

Pages is configured to build from the `gh-pages` branch root. A publication becomes visible once the Pages build of the new commit finishes, which usually takes a few seconds; the build status is reported by `gh api repos/neko233-com/git-next/pages --jq .status`.

## Languages

The published manual is written in English, and the Chinese manual is the translated `README.zh-CN.md` in the repository. The interface language of the program itself is chosen by the device, and every language is a locale document; see [interface languages](LANGUAGE.md).
