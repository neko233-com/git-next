"""Independent common CLI scenarios, including error protection and recovery."""
import argparse
from concurrent.futures import ThreadPoolExecutor
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import zipfile


def sha(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


class Fixture:
    def __init__(self, root, binary, reference, fmt):
        self.root, self.binary, self.reference = root, binary, reference
        self.repo = root / '中文 repo'
        home = root / 'home'
        home.mkdir()
        self.env = {k: v for k, v in os.environ.items() if not k.startswith(('GIT_', 'GCM_', 'SSH_', 'GNUPG'))}
        self.env.update(HOME=str(home), USERPROFILE=str(home), APPDATA=str(home/'appdata'),
                        LOCALAPPDATA=str(home/'local'), XDG_CONFIG_HOME=str(home/'.config'),
                        GIT_CONFIG_GLOBAL=str(home/'config'), GIT_CONFIG_NOSYSTEM='1',
                        GIT_NEXT_CONFIG_HOME=str(home/'git-next'), GIT_NEXT_COMPONENT_HOME=str(home/'components'),
                        SSH_ROOT_PATH=str(home/'.ssh'), GNUPGHOME=str(home/'gnupg'), GIT_TERMINAL_PROMPT='0',
                        GIT_AUTHOR_NAME='Fixture', GIT_COMMITTER_NAME='Fixture', GIT_AUTHOR_EMAIL='fixture@example.invalid',
                        GIT_COMMITTER_EMAIL='fixture@example.invalid', GIT_AUTHOR_DATE='2001-02-03T04:05:06Z',
                        GIT_COMMITTER_DATE='2001-02-03T04:05:06Z', GIT_EDITOR='true', GIT_SEQUENCE_EDITOR='true',
                        LC_ALL='C', LANG='C')
        self.commands = 0
        self.git('init', '-q', '-b', 'main', '--object-format='+fmt, self.repo, where=root)
        self.write('source.txt', b'base\n')
        self.write('中文 目录/asset.unity', b'%YAML 1.1\nvalue: base\n')
        self.commit('base')
        self.base = self.git('rev-parse', 'HEAD').strip().decode()

    def git(self, *command, where=None, ok=True, reference=False):
        executable = self.reference if reference else self.binary
        env = dict(self.env)
        if not reference and os.name == 'nt':
            prefix = self.binary.parent.parent
            env['PATH'] = os.pathsep.join([str(self.binary.parent), str(prefix/'runtime/usr/bin'),
                                         str(Path(os.environ.get('SystemRoot', '/'))/'System32')])
        options = [arg for setting in ('core.fscache=false', 'core.fsmonitor=false', 'core.preloadIndex=false',
                                       'index.threads=1', 'grep.threads=1') for arg in ('-c', setting)]
        if not reference and os.name != 'nt':
            env['PATH'] = os.pathsep.join([str(self.binary.parent), str(self.binary.parent.parent/'runtime/bin'), '/usr/bin', '/bin'])
        p = subprocess.run([str(executable), *options, *map(str, command)], cwd=where or self.repo,
                           env=env, capture_output=True, timeout=40,
                           creationflags=subprocess.CREATE_NO_WINDOW if os.name == 'nt' else 0)
        self.commands += 1
        assert (p.returncode == 0) == ok, (command, hex(p.returncode & 0xffffffff), p.stderr.decode(errors='replace')[:700])
        if not ok:
            assert p.returncode in (1, 2, 128, 129), ('failure must be a normal Git error', command, p.returncode)
        return p.stdout

    def write(self, name, data):
        path = self.repo/name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)
        return path

    def commit(self, message, where=None):
        self.git('add', '.', where=where)
        self.git('commit', '-qm', message, where=where)

    def compare(self, *command):
        value = self.git(*command)
        assert value == self.git(*command, reference=True), command
        return value

    def clean(self):
        assert self.compare('status', '--porcelain') == b''

    def verify(self):
        self.compare('ls-files', '--stage', '-z')
        self.compare('log', '--format=%H', '-10')
        self.git('fsck', '--full')
        self.git('fsck', '--full', reference=True)


def history(f):
    f.clean(); f.compare('show', 'HEAD:source.txt')
    assert f.compare('diff', '--no-ext-diff') == b''
    f.write('source.txt', b'changed\n')
    assert b'+changed' in f.compare('diff', '--no-ext-diff')
    f.commit('changed'); assert len(f.compare('rev-list', 'HEAD').splitlines()) == 2


def branch_checkout(f):
    f.git('branch', 'topic'); f.git('branch', '-m', 'topic', 'feature'); f.git('checkout', 'feature')
    assert f.git('branch', '--show-current').strip() == b'feature'
    f.write('source.txt', b'dirty\n'); f.git('checkout', '--', 'source.txt')
    assert (f.repo/'source.txt').read_bytes() == b'base\n'
    f.git('checkout', '-b', 'temporary', 'main'); f.git('checkout', '--detach', f.base)
    f.git('symbolic-ref', '-q', 'HEAD', ok=False)
    assert f.git('rev-parse', 'HEAD').strip().decode() == f.base
    f.git('checkout', 'main'); f.git('branch', '-d', 'temporary', 'feature')
    f.compare('branch', '--format=%(refname:short)'); f.clean()


def merge_revert(f):
    f.git('checkout', '-b', 'topic'); f.write('topic.txt', b'topic\n'); f.commit('topic')
    topic = f.git('rev-parse', 'HEAD').strip().decode()
    f.git('checkout', 'main'); f.git('merge', '--no-ff', '--no-edit', 'topic')
    assert len(f.git('rev-list', '--parents', '-n', '1', 'HEAD').split()) == 3
    f.git('checkout', '-b', 'picked', f.base); f.git('cherry-pick', topic)
    tree = f.git('rev-parse', 'HEAD^{tree}')
    f.git('revert', '--no-edit', 'HEAD'); assert not (f.repo/'topic.txt').exists()
    f.git('revert', '--no-edit', 'HEAD'); assert f.git('rev-parse', 'HEAD^{tree}') == tree
    f.compare('show-ref'); f.clean()


def rebase(f):
    f.git('checkout', '-b', 'topic'); f.write('topic.txt', b'topic\n'); f.commit('topic')
    f.git('checkout', 'main'); f.write('main.txt', b'main\n'); f.commit('main advance')
    f.git('checkout', 'topic'); f.git('rebase', 'main')
    assert f.git('merge-base', 'main', 'HEAD') == f.git('rev-parse', 'main')
    assert (f.repo/'topic.txt').exists() and (f.repo/'main.txt').exists(); f.clean()


def restore_reset(f):
    f.write('second.txt', b'second\n'); f.commit('second')
    head = f.git('rev-parse', 'HEAD').strip().decode(); tree = f.git('write-tree')
    f.write('source.txt', b'staged\n'); f.git('add', 'source.txt'); f.git('restore', '--staged', 'source.txt')
    assert f.git('write-tree') == tree and (f.repo/'source.txt').read_bytes() == b'staged\n'
    f.git('restore', '--worktree', 'source.txt'); assert (f.repo/'source.txt').read_bytes() == b'base\n'
    f.git('reset', '--soft', 'HEAD^'); assert f.git('write-tree') == tree
    f.git('reset', '--mixed', head); f.write('source.txt', b'discarded fixture\n'); f.git('reset', '--hard', head)
    assert (f.repo/'source.txt').read_bytes() == b'base\n'; f.clean()


def stash_untracked(f):
    f.write('source.txt', b'staged\n'); f.git('add', 'source.txt'); untracked = f.write('untracked.txt', b'untracked\n')
    f.git('stash', 'push', '--include-untracked', '-m', 'fixture')
    assert not untracked.exists() and (f.repo/'source.txt').read_bytes() == b'base\n'
    f.clean(); f.git('stash', 'pop', '--index')
    assert untracked.read_bytes() == b'untracked\n' and f.git('show', ':source.txt') == b'staged\n'
    f.git('reset', '--hard', f.base); untracked.unlink(); assert f.git('stash', 'list') == b''; f.clean()


def invalid_operations(f):
    index = sha(f.repo/'.git/index'); head = f.git('rev-parse', 'HEAD')
    f.git('checkout', 'missing-fixture-reference', ok=False); f.git('branch', '-D', 'main', ok=False)
    f.git('revert', 'not-a-fixture-commit', ok=False)
    assert f.git('rev-parse', 'HEAD') == head and sha(f.repo/'.git/index') == index; f.clean()


def worktree(f):
    linked, moved = f.root/'linked 中文', f.root/'moved 中文'
    f.git('worktree', 'add', '-b', 'wt', linked, 'main'); assert (linked/'.git').is_file()
    (linked/'linked.txt').write_bytes(b'linked\n'); f.commit('linked fixture', where=linked)
    assert f.git('rev-parse', 'HEAD').strip().decode() == f.base
    f.compare('worktree', 'list', '--porcelain'); f.git('worktree', 'lock', '--reason', 'fixture', linked)
    f.git('worktree', 'remove', linked, ok=False); assert linked.exists()
    f.git('worktree', 'unlock', linked); f.git('worktree', 'move', linked, moved)
    assert moved.exists() and not linked.exists()
    (moved/'source.txt').write_bytes(b'dirty linked\n'); f.git('worktree', 'remove', moved, ok=False); assert moved.exists()
    f.git('worktree', 'remove', '--force', moved); f.git('worktree', 'prune'); f.git('branch', '-D', 'wt')
    assert not moved.exists(); f.compare('worktree', 'list', '--porcelain'); f.clean()


def transport(f):
    remote, second = f.root/'remote.git', f.root/'second'
    f.git('clone', '--bare', f.repo, remote, where=f.root); f.git('remote', 'add', 'origin', remote)
    f.git('push', '-u', 'origin', 'main'); f.git('branch', '--set-upstream-to', 'origin/main', 'main')
    f.git('clone', '--no-local', remote, second, where=f.root)
    (second/'remote.txt').write_bytes(b'remote\n'); f.commit('remote fixture', where=second)
    f.git('push', 'origin', 'main', where=second); f.git('fetch', 'origin'); f.git('pull', '--ff-only', 'origin', 'main')
    assert f.git('rev-parse', 'HEAD') == f.git('rev-parse', 'HEAD', where=second)
    f.git('checkout', '--track', '-b', 'tracked', 'origin/main')
    assert f.git('rev-parse', '@{upstream}') == f.git('rev-parse', 'origin/main'); f.clean()


def conflicts(f):
    f.git('checkout', '-b', 'topic'); f.write('source.txt', b'topic\n'); f.commit('topic')
    topic = f.git('rev-parse', 'HEAD').strip().decode()
    f.git('checkout', 'main'); f.write('source.txt', b'main\n'); f.commit('main')
    head = f.git('rev-parse', 'HEAD')
    for command, abort in [(('merge', '--no-edit', 'topic'), ('merge', '--abort')),
                            (('cherry-pick', topic), ('cherry-pick', '--abort'))]:
        f.git(*command, ok=False); assert f.compare('ls-files', '--unmerged')
        f.git(*abort); assert f.git('rev-parse', 'HEAD') == head; f.clean()
    f.git('checkout', 'topic'); f.git('rebase', 'main', ok=False); f.git('rebase', '--abort')
    assert f.git('rev-parse', 'HEAD').strip().decode() == topic; f.clean()


def bundle_archive_tags(f):
    f.git('tag', 'lightweight'); f.git('tag', '-a', 'v1', '-m', 'fixture tag'); f.compare('tag', '--list')
    assert f.git('rev-parse', 'v1^{}').strip().decode() == f.base
    archive, bundle, clone = f.root/'repo.zip', f.root/'repo.bundle', f.root/'bundle-clone'
    f.git('archive', '--format=zip', '-o', archive, 'HEAD')
    with zipfile.ZipFile(archive) as content: assert content.read('source.txt') == b'base\n'
    f.git('bundle', 'create', bundle, '--all'); f.git('bundle', 'verify', bundle)
    f.git('clone', bundle, clone, where=f.root)
    assert f.git('rev-parse', 'HEAD', where=clone).strip().decode() == f.base
    assert f.git('reflog', '--format=%H').splitlines(); f.git('tag', '-d', 'lightweight', 'v1'); f.clean()


def ignore_clean_attributes(f):
    f.write('.gitignore', b'ignored/\n'); f.write('.gitattributes', b'*.unity text eol=lf\n*.bin -text\n')
    binary = b'\0\r\n\xff\r\n'; f.write('blob.bin', binary); f.commit('attributes')
    ignored = f.write('ignored/cache.txt', b'ignored\n'); scratch = f.write('scratch.txt', b'scratch\n')
    assert f.git('check-ignore', 'ignored/cache.txt').strip() == b'ignored/cache.txt'
    assert b'ignored/' in f.git('clean', '-ndx') and ignored.exists() and scratch.exists()
    f.git('clean', '-fd'); assert ignored.exists() and not scratch.exists()
    f.git('clean', '-fdx'); assert not ignored.exists()
    f.write('中文 目录/asset.unity', b'%YAML 1.1\r\nvalue: base\r\n')
    f.git('add', '.'); assert f.git('diff', '--cached', '--no-ext-diff') == b''
    assert f.git('show', 'HEAD:blob.bin') == binary
    assert b'eol: lf' in f.compare('check-attr', 'text', 'eol', '--', '中文 目录/asset.unity')


def locks_and_move(f):
    head = f.git('rev-parse', 'HEAD'); original = sha(f.repo/'.git/index')
    lock = f.repo/'.git/index.lock'; lock.write_bytes(b'owned fixture lock\n')
    f.write('source.txt', b'modified\n'); f.git('add', '.', ok=False)
    assert lock.read_bytes() == b'owned fixture lock\n' and sha(f.repo/'.git/index') == original
    lock.unlink(); f.git('restore', 'source.txt')
    f.git('mv', 'source.txt', '中文 renamed.txt'); f.commit('rename')
    assert f.git('show', 'HEAD:中文 renamed.txt') == b'base\n'
    f.git('rm', '中文 renamed.txt'); f.commit('remove'); assert not (f.repo/'中文 renamed.txt').exists()
    assert f.git('rev-parse', 'HEAD') != head; f.clean()


def checkout_protection(f):
    f.git('checkout', '-b', 'topic'); f.write('source.txt', b'topic\n')
    f.write('topic-only.txt', b'committed topic\n'); f.commit('topic')
    f.git('checkout', 'main'); f.write('source.txt', b'user work\n')
    index = sha(f.repo/'.git/index')
    f.git('checkout', 'topic', ok=False)
    assert f.git('branch', '--show-current').strip() == b'main'
    assert sha(f.repo/'.git/index') == index and (f.repo/'source.txt').read_bytes() == b'user work\n'
    f.git('restore', 'source.txt'); collision = f.write('topic-only.txt', b'untracked work\n')
    f.git('checkout', 'topic', ok=False)
    assert collision.read_bytes() == b'untracked work\n' and f.git('branch', '--show-current').strip() == b'main'
    collision.unlink(); f.clean()


def resolved_conflicts(f):
    f.git('checkout', '-b', 'topic'); f.write('source.txt', b'topic\n'); f.commit('topic')
    topic = f.git('rev-parse', 'HEAD').strip().decode()
    f.git('checkout', 'main'); f.write('source.txt', b'main\n'); f.commit('main')
    f.git('cherry-pick', topic, ok=False); f.write('source.txt', b'merged\n'); f.git('add', 'source.txt')
    f.git('cherry-pick', '--continue'); assert f.git('show', 'HEAD:source.txt') == b'merged\n'; f.clean()
    f.git('checkout', 'topic'); f.git('rebase', 'main', ok=False)
    f.write('source.txt', b'rebased resolution\n'); f.git('add', 'source.txt'); f.git('rebase', '--continue')
    assert f.git('merge-base', 'main', 'HEAD') == f.git('rev-parse', 'main'); f.clean()
    f.git('revert', '--no-edit', topic, ok=False); assert f.compare('ls-files', '--unmerged')
    f.git('revert', '--abort'); assert (f.repo/'source.txt').read_bytes() == b'rebased resolution\n'; f.clean()


def rejected_push(f):
    remote, second = f.root/'remote.git', f.root/'second'
    f.git('clone', '--bare', f.repo, remote, where=f.root); f.git('remote', 'add', 'origin', remote)
    f.git('push', '-u', 'origin', 'main'); f.git('clone', remote, second, where=f.root)
    (second/'other.txt').write_bytes(b'other writer\n'); f.commit('other writer', where=second)
    f.git('push', 'origin', 'main', where=second)
    remote_head = f.git('rev-parse', 'refs/heads/main', where=remote)
    f.write('local.txt', b'local writer\n'); f.commit('local writer'); head = f.git('rev-parse', 'HEAD')
    f.git('push', 'origin', 'main', ok=False)
    f.git('push', '--force-with-lease=refs/heads/main:'+f.base, 'origin', 'main', ok=False)
    assert f.git('rev-parse', 'HEAD') == head and f.git('rev-parse', 'refs/heads/main', where=remote) == remote_head
    f.git('fetch', 'origin'); f.git('rebase', 'origin/main'); f.git('push', 'origin', 'main')
    assert f.git('rev-parse', 'HEAD') == f.git('rev-parse', 'refs/heads/main', where=remote); f.clean()


CASES = {function.__name__: function for function in (history, branch_checkout, merge_revert, rebase,
         restore_reset, stash_untracked, invalid_operations, worktree, transport, conflicts,
         bundle_archive_tags, ignore_clean_attributes, locks_and_move, checkout_protection,
         resolved_conflicts, rejected_push)}


def main():
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('binary', type=Path); parser.add_argument('--reference', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True); parser.add_argument('--case', choices=CASES, action='append')
    parser.add_argument('--jobs', type=int, default=4, choices=range(1, 9)); args = parser.parse_args()
    binary, reference = args.binary.resolve(strict=True), args.reference.resolve(strict=True)
    before, started = sha(binary), time.monotonic()
    jobs = [(name, fmt) for name in (args.case or CASES) for fmt in ('sha1', 'sha256')]
    def execute(job):
        name, fmt = job; begin = time.monotonic(); fixture = None
        result = dict(case=name, object_format=fmt, status='fail')
        try:
            with tempfile.TemporaryDirectory(prefix='git-next-common-cli-') as temporary:
                fixture = Fixture(Path(temporary).resolve(), binary, reference, fmt)
                CASES[name](fixture); fixture.verify(); result['status'] = 'pass'
        except Exception as error:
            result['error'] = str(error)
        result.update(seconds=round(time.monotonic()-begin, 3), commands=fixture.commands if fixture else 0)
        print(json.dumps(result, ensure_ascii=True), flush=True)
        return result
    with ThreadPoolExecutor(args.jobs) as pool: results = list(pool.map(execute, jobs))
    assert sha(binary) == before
    report = dict(status='pass' if all(r['status'] == 'pass' for r in results) else 'fail',
                  binary_sha256=before, reference_sha256=sha(reference), harness_sha256=sha(Path(__file__)),
                  scope='independent common CLI scenarios and recovery; SHA-1 and SHA-256; reference object/index readers',
                  seconds=round(time.monotonic()-started, 3), parallel_jobs=args.jobs,
                  personal_profiles_accessed=False, external_git_runtime_fallback=False, results=results)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2)+'\n', encoding='utf-8', newline='\n')
    print(json.dumps({k: v for k, v in report.items() if k != 'results'}))
    raise SystemExit(0 if report['status'] == 'pass' else 1)


if __name__ == '__main__':
    main()
