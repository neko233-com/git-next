"""Publish the documentation site to the `gh-pages` branch.

The manual is published as a GitHub Pages site, so the documentation is readable
without cloning the repository. The pages are rendered from the Markdown manuals
in `docs/` and from the README, which keeps the site and the repository from
drifting apart: there is only one source for each page.

The site is published from a branch rather than from a workflow, because this
repository builds, tests and packages everything locally. The publication therefore
runs on the machine that owns the release, and it builds the branch with git's
plumbing commands in a temporary index: the working tree, the current branch and
the staged changes are never touched, so a publication can be run at any time,
including with uncommitted work in the tree.

Usage:
    python tools/publish_docs.py            # render and publish
    python tools/publish_docs.py --dry-run  # render and report, push nothing
"""
from __future__ import annotations

import argparse
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SITE = ROOT / 'docs' / 'site'
BRANCH = 'gh-pages'
REMOTE = 'origin'
COMMIT = 'docs: publish the manual'

# GitHub Pages serves the branch from the repository root, so the rendered pages
# become the top level of the branch. Jekyll is disabled with an empty marker:
# the pages are already rendered, and Jekyll would otherwise rewrite them.
NO_JEKYLL = '.nojekyll'


def run(*args: str, cwd: Path = ROOT, env: dict[str, str] | None = None) -> str:
    """Run a git command and return its output, failing loudly on an error."""
    result = subprocess.run(
        args,
        cwd=cwd,
        check=False,
        capture_output=True,
        text=True,
        encoding='utf-8',
        errors='replace',
        env=env,
    )
    if result.returncode != 0:
        detail = (result.stderr or result.stdout or '').strip()
        raise SystemExit(f'command failed: {" ".join(args)}\n{detail}')
    return (result.stdout or '').strip()


def render() -> int:
    """Render the Markdown manuals into the site directory."""
    run(sys.executable, str(ROOT / 'tools' / 'render_docs.py'))
    pages = sorted(SITE.glob('*.html'))
    if not pages:
        raise SystemExit('the renderer produced no pages')
    return len(pages)


def stage(workspace: Path) -> Path:
    """Copy the rendered site into an empty directory that becomes the branch."""
    workspace.mkdir(parents=True)
    for page in SITE.glob('*.html'):
        shutil.copy2(page, workspace / page.name)
    # Any asset a manual references lives next to the pages, so the whole
    # directory is published, not just the HTML files.
    for entry in SITE.iterdir():
        if entry.is_file() and entry.suffix != '.html':
            shutil.copy2(entry, workspace / entry.name)
    (workspace / NO_JEKYLL).write_text('', encoding='utf-8')
    return workspace


def build_commit(workspace: Path) -> str:
    """Build a commit that contains exactly the rendered site.

    The commit is assembled in a temporary index, so the repository's own index,
    working tree and current branch are left exactly as they were.
    """
    index = workspace.parent / 'index'
    environment = dict(os.environ)
    environment['GIT_INDEX_FILE'] = str(index)

    run('git', 'read-tree', '--empty', env=environment)
    # The work tree is the staged directory, so every file in it is added and
    # nothing outside it can leak into the commit. Without a pathspec, the add
    # covers the whole work tree, which is exactly the staged directory.
    run(
        'git',
        '--work-tree',
        str(workspace),
        'add',
        '--all',
        '--force',
        env=environment,
    )
    tree = run('git', 'write-tree', env=environment)
    parent = previous_commit()
    if parent and run('git', 'rev-parse', f'{parent}^{{tree}}') == tree:
        return ''
    message = COMMIT if parent else f'{COMMIT} (initial publication)'
    arguments = ['git', 'commit-tree', tree, '-m', message]
    if parent:
        arguments += ['-p', parent]
    commit = run(*arguments, env=environment)
    run('git', 'update-ref', f'refs/heads/{BRANCH}', commit)
    return commit


def previous_commit() -> str:
    """The tip of the published branch, or an empty string on the first run."""
    result = subprocess.run(
        ['git', 'rev-parse', '--verify', f'refs/heads/{BRANCH}'],
        cwd=ROOT,
        capture_output=True,
        text=True,
    )
    if result.returncode == 0:
        return result.stdout.strip()
    # A fresh clone has the branch only as a remote reference, and only once it
    # has been fetched. The fetch is allowed to fail: before the first
    # publication there is nothing to fetch.
    subprocess.run(
        ['git', 'fetch', REMOTE, f'{BRANCH}:refs/remotes/{REMOTE}/{BRANCH}'],
        cwd=ROOT,
        capture_output=True,
        text=True,
    )
    result = subprocess.run(
        ['git', 'rev-parse', '--verify', f'refs/remotes/{REMOTE}/{BRANCH}'],
        cwd=ROOT,
        capture_output=True,
        text=True,
    )
    return result.stdout.strip() if result.returncode == 0 else ''


def main() -> int:
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument(
        '--dry-run',
        action='store_true',
        help='render the pages and build the commit without pushing it',
    )
    arguments = parser.parse_args()

    pages = render()
    print(f'rendered {pages} pages into {SITE}')

    with tempfile.TemporaryDirectory(prefix='git-next-docs-') as temporary:
        workspace = stage(Path(temporary) / 'site')
        commit = build_commit(workspace)
        if not commit:
            # The pages are unchanged. The branch may still never have been
            # published - a render that was only committed locally leaves the
            # remote behind - so the push below still runs.
            print('the rendered pages are unchanged')
        else:
            print(f'built the publication commit {commit[:12]}')
        if arguments.dry_run:
            print(f'dry run: {BRANCH} was updated locally but not pushed')
            return 0
        # The branch is built locally and pushed explicitly, so a publication
        # does not depend on the current branch or on a clean working tree. The
        # push is a fast-forward: the commit always descends from the published
        # tip, and a divergence is reported instead of being overwritten.
        run('git', 'push', REMOTE, f'refs/heads/{BRANCH}:refs/heads/{BRANCH}')
    print(f'published the manual to the {BRANCH} branch')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
