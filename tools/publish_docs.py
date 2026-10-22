"""Publish the documentation site to the `gh-pages` branch.

The manual is published as a GitHub Pages site, so the documentation is
readable without cloning the repository. The pages are rendered from the
Markdown manuals in `docs/` and from the README, which keeps the site and the
repository from drifting apart: there is only one source for each page.

The site is published from a branch rather than from a workflow, because this
repository builds, tests and packages everything locally. The script therefore
does the same thing a workflow would do, but on the machine that owns the
release: it renders the pages, prepares a commit that contains only the rendered
site, and pushes it to the `gh-pages` branch.

Usage:
    python tools/publish_docs.py            # render and publish
    python tools/publish_docs.py --dry-run  # render and report, push nothing
"""
from __future__ import annotations

import argparse
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SITE = ROOT / 'docs' / 'site'
BRANCH = 'gh-pages'
COMMIT = 'docs: publish the manual'


def run(*args: str, cwd: Path = ROOT, capture: bool = True) -> str:
    """Run a command and return its output, failing loudly on an error."""
    result = subprocess.run(
        args,
        cwd=cwd,
        check=False,
        capture_output=capture,
        text=True,
        encoding='utf-8',
        errors='replace',
    )
    if result.returncode != 0:
        detail = (result.stderr or result.stdout or '').strip()
        raise SystemExit(f'command failed: {" ".join(args)}\n{detail}')
    return (result.stdout or '').strip()


def render() -> None:
    """Render the Markdown manuals into the site directory."""
    run(sys.executable, str(ROOT / 'tools' / 'render_docs.py'))


def prepare(workspace: Path) -> Path:
    """Copy the rendered site into a clean directory for the commit."""
    if workspace.exists():
        shutil.rmtree(workspace)
    shutil.copytree(SITE, workspace)
    # The branch carries the rendered pages only. A leftover file from an
    # earlier publication would be served forever, so the directory starts
    # empty every time.
    return workspace


def publish(workspace: Path, dry_run: bool) -> None:
    """Commit the rendered site to the `gh-pages` branch and push it."""
    existing = run('git', 'rev-parse', '--verify', BRANCH, capture=True) if _branch_exists() else ''
    if existing:
        # Start from the published state so the branch keeps its history.
        run('git', 'checkout', BRANCH)
        run('git', 'reset', '--hard', existing)
    else:
        run('git', 'checkout', '--orphan', BRANCH)

    for entry in workspace.iterdir():
        target = ROOT / entry.name
        if target.exists():
            if target.is_dir():
                shutil.rmtree(target)
            else:
                target.unlink()
        shutil.move(str(entry), str(target))

    run('git', 'add', '--all', 'docs/site')
    if run('git', 'status', '--porcelain', 'docs/site') == '':
        print('the published site is already up to date')
        run('git', 'checkout', 'main')
        return

    run('git', 'commit', '-m', COMMIT)
    if dry_run:
        print('dry run: the commit was not pushed')
        run('git', 'checkout', 'main')
        return
    run('git', 'push', 'origin', BRANCH)
    print(f'published the manual to the {BRANCH} branch')


def _branch_exists() -> bool:
    result = subprocess.run(
        ['git', 'rev-parse', '--verify', BRANCH],
        cwd=ROOT,
        check=False,
        capture_output=True,
        text=True,
    )
    return result.returncode == 0


def main() -> int:
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument(
        '--dry-run',
        action='store_true',
        help='render the pages and report the result without pushing',
    )
    arguments = parser.parse_args()

    render()
    print(f'rendered {len(list(SITE.glob("*.html")))} pages into {SITE}')
    with tempfile.TemporaryDirectory(prefix='git-next-docs-') as temporary:
        workspace = prepare(Path(temporary) / 'site')
        publish(workspace, arguments.dry_run)
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
