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
python -m pip install markdown-it-py
python tools/render_docs.py
```

The renderer writes every page into `docs/site/` and then verifies that every local link of every page resolves. A broken link fails the run, so a page that is published always points at pages that exist.

## Publishing

```sh
python tools/publish_docs.py            # render and publish
python tools/publish_docs.py --dry-run  # render and report, push nothing
```

The site is published from the `gh-pages` branch. This repository builds, tests and packages everything locally, so the publication is done by the same script that a workflow would run, on the machine that owns the release. The script renders the pages, replaces the contents of the branch with the rendered site, commits it and pushes it.

The branch carries the rendered pages only. A leftover file from an earlier publication would be served forever, so the directory is rebuilt from scratch on every run.

The first publication has to point the repository at the branch once, in the repository settings: **Settings → Pages → Build and deployment → Source → Deploy from a branch → `gh-pages` / `root`**. After that the script keeps the branch up to date.

## Languages

The published manual is written in English, and the Chinese manual is the translated `README.zh-CN.md` in the repository. The interface language of the program itself is chosen by the device, and every language is a locale document; see [interface languages](LANGUAGE.md).
