"""Compare native long-status output with reference Git in isolated SHA-1/SHA-256 repositories."""
# Retired CLI gate; definitions remain importable by native package fixtures.
if __name__ == '__main__':
    raise SystemExit('Retired Python CLI suite. Use accept-runner/git-next-accept --tier full (see docs/GIT_NEXT_ACCEPTANCE.md).')

import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

root = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(__doc__)
parser.add_argument('binary', type=Path)
args = parser.parse_args()
binary = args.binary.resolve()
digest = hashlib.sha256(binary.read_bytes()).hexdigest()
harness_digest = hashlib.sha256(Path(__file__).read_bytes()).hexdigest()
reference = shutil.which('git')
assert reference
results = []
for object_format in ('sha1', 'sha256'):
    with tempfile.TemporaryDirectory(prefix='git-next-long-status-more-') as temporary:
        directory = Path(temporary)
        home, empty, repo = directory / 'home', directory / 'empty', directory / 'repo'
        home.mkdir()
        empty.mkdir()
        env = {key: value for key, value in os.environ.items() if not key.startswith(('GIT_', 'GIT_NEXT_'))}
        env.update(HOME=str(home), USERPROFILE=str(home), SSH_ROOT_PATH=str(home / '.ssh'), XDG_CONFIG_HOME=str(home / '.config'),
                   GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=str(home / 'config'), GIT_OPTIONAL_LOCKS='0', LC_ALL='C',
                   GIT_AUTHOR_NAME='Fixture', GIT_COMMITTER_NAME='Fixture', GIT_AUTHOR_EMAIL='fixture@example.invalid',
                   GIT_COMMITTER_EMAIL='fixture@example.invalid', GIT_AUTHOR_DATE='2001-02-03T04:05:06Z', GIT_COMMITTER_DATE='2001-02-03T04:05:06Z')

        def run(native, *arguments, cwd=None):
            variables = dict(env)
            if native:
                variables.update(PATH=str(empty), GIT_EXEC_PATH=str(empty))
            return subprocess.run([str(binary) if native else reference, *map(str, arguments)],
                                  cwd=cwd or (repo if repo.exists() else directory), env=variables, capture_output=True, timeout=30)

        def git(*arguments):
            result = run(False, *arguments)
            assert result.returncode == 0, (arguments, result.stderr)
            return result.stdout.strip()

        def compare(label, *arguments, cwd=None):
            before = {p.relative_to(repo / '.git').as_posix(): hashlib.sha256(p.read_bytes()).hexdigest()
                      for p in (repo / '.git').rglob('*') if p.is_file()}
            expected = run(False, *arguments, cwd=cwd)
            actual = run(True, *arguments, cwd=cwd)
            after = {p.relative_to(repo / '.git').as_posix(): hashlib.sha256(p.read_bytes()).hexdigest()
                     for p in (repo / '.git').rglob('*') if p.is_file()}
            assert before == after, label
            results.append({'object_format': object_format, 'case': label, 'arguments': arguments,
                            'equal': expected.returncode == actual.returncode and expected.stdout == actual.stdout,
                            'reference_exit': expected.returncode, 'native_exit': actual.returncode,
                            'reference': expected.stdout.decode(), 'native': actual.stdout.decode()})

        git('init', '-q', '-b', 'main', '--object-format=' + object_format, repo)
        git('config', 'core.autocrlf', 'input')
        compare('empty unborn', 'status')
        (repo / 'sub').mkdir()
        for name in ('alpha.txt', 'delete.txt', 'sub/keep.txt'):
            (repo / name).write_bytes(b'base\n')
        compare('unborn untracked', 'status')
        git('add', '.')
        compare('unborn staged', 'status')
        compare('unborn staged with hidden untracked', 'status', '-uno')
        git('commit', '-qm', 'fixture: initial')
        initial = git('rev-parse', 'HEAD').decode()
        compare('explicit long', 'status', '--long')
        compare('short configuration', '-c', 'status.short=true', 'status')
        compare('short branch configuration', '-c', 'status.short=true', '-c', 'status.branch=true', 'status')
        compare('hide untracked footer', 'status', '-uno')
        compare('hide untracked footer without hints', '-c', 'advice.statusHints=false', 'status', '-uno')
        compare('CLI long overrides short configuration', '-c', 'status.short=true', 'status', '--long')
        compare('branch config in porcelain v1', '-c', 'status.branch=true', 'status', '--porcelain')
        compare('branch config in porcelain v2', '-c', 'status.branch=true', 'status', '--porcelain=v2')
        for key in ('status.short', 'status.branch', 'advice.statusHints'):
            compare('invalid ' + key + ' under porcelain', '-c', key + '=bogus', 'status', '--porcelain')
        (repo / 'alpha.txt').write_bytes(b'changed\n')
        (repo / 'delete.txt').unlink()
        (repo / 'space file.txt').write_bytes(b'untracked\n')
        (repo / 'intent.txt').write_bytes(b'intent\n')
        git('add', '-N', 'intent.txt')
        compare('mixed staged hidden untracked without hints', '-c', 'advice.statusHints=false', 'status', '-uno')
        for flags in (('--short', '--long'), ('--long', '--short'), ('--porcelain=v2', '--long'),
                      ('--long', '--porcelain=v2'), ('--long', '-z'), ('-z', '--long')):
            compare('format precedence ' + ' '.join(flags), 'status', *flags)
        compare('mixed unstaged deletion and intent', 'status')
        compare('no status hints', '-c', 'advice.statusHints=false', 'status')
        compare('long relative paths', 'status', cwd=repo / 'sub')
        compare('disable relative paths', '-c', 'status.relativePaths=false', 'status', cwd=repo / 'sub')
        git('add', '-A')
        git('mv', 'alpha.txt', 'renamed.txt')
        compare('staged rename add deletion', 'status')
        git('commit', '-qm', 'fixture: second')
        git('config', 'remote.origin.url', 'https://example.invalid/fixture.git')
        git('config', 'remote.origin.fetch', '+refs/heads/*:refs/remotes/origin/*')
        git('config', 'branch.main.remote', 'origin')
        git('config', 'branch.main.merge', 'refs/heads/main')
        git('update-ref', 'refs/remotes/origin/main', 'HEAD')
        compare('equal upstream', 'status')
        compare('equal upstream no hints', '-c', 'advice.statusHints=false', 'status')
        git('update-ref', 'refs/remotes/origin/main', initial)
        compare('ahead upstream', 'status')
        compare('unknown ahead behind counts', 'status', '--no-ahead-behind')
        git('update-ref', 'refs/remotes/origin/main', 'HEAD')
        git('reset', '-q', '--hard', initial)
        compare('behind upstream', 'status')
        (repo / 'alpha.txt').write_bytes(b'diverged local\n')
        git('add', 'alpha.txt')
        git('commit', '-qm', 'fixture: divergence')
        compare('diverged upstream', 'status')
        git('update-ref', '-d', 'refs/remotes/origin/main')
        compare('gone upstream', 'status')
        git('checkout', '-q', '--detach', 'main')
        compare('detached at branch', 'status')
        head_log = repo / '.git/logs/HEAD'
        saved_log = head_log.read_bytes()
        head_log.unlink()
        compare('detached without reflog', 'status')
        head_log.write_bytes(saved_log)
        for mask in range(1, 8):
            git('read-tree', 'HEAD')
            git('update-index', '--force-remove', 'alpha.txt')
            blob = git('rev-parse', 'HEAD:alpha.txt').decode()
            stream = ''.join(f'100644 {blob} {stage}\talpha.txt\n' for stage in (1, 2, 3) if mask & (1 << (stage - 1)))
            command = [reference, 'update-index', '--index-info']
            process = subprocess.run(command, cwd=repo, env=env, input=stream.encode(), capture_output=True, timeout=30)
            assert process.returncode == 0, process.stderr
            compare('unmerged mask ' + str(mask), 'status')
        merge_head = repo / '.git/MERGE_HEAD'
        merge_head.write_bytes((initial + '\n').encode())
        compare('merge unmerged', 'status')
        compare('merge unmerged without hints', '-c', 'advice.statusHints=false', 'status')
        compare('merge excludes conflict paths', 'status', '--', 'sub')
        git('read-tree', 'HEAD')
        compare('merge resolved', 'status')
        compare('merge resolved without hints', '-c', 'advice.statusHints=false', 'status')
        (repo / 'alpha.txt').write_bytes(b'merge resolution\n')
        git('add', 'alpha.txt')
        compare('merge resolved staged', 'status')
        merge_head.unlink()
        (repo / 'alpha.txt').write_bytes(b'detached advancement\n')
        git('add', 'alpha.txt')
        git('commit', '-qm', 'fixture: detached advancement')
        compare('detached from branch after commit', 'status')
        git('tag', 'fixture-tag', 'HEAD')
        git('checkout', '-q', '--detach', 'fixture-tag')
        compare('detached at tag', 'status')
        git('fsck', '--full')
        before = {p.relative_to(repo / '.git').as_posix(): hashlib.sha256(p.read_bytes()).hexdigest()
                  for p in (repo / '.git').rglob('*') if p.is_file()}
        process = subprocess.run([str(binary), 'status', '--long', '-z'], cwd=repo,
                                 env={**env, 'PATH': str(empty), 'GIT_EXEC_PATH': str(empty), 'GIT_OPTIONAL_LOCKS': '1'},
                                 capture_output=True, timeout=30)
        after = {p.relative_to(repo / '.git').as_posix(): hashlib.sha256(p.read_bytes()).hexdigest()
                 for p in (repo / '.git').rglob('*') if p.is_file()}
        assert process.returncode != 0 and not process.stdout and before == after
        results.append({'object_format': object_format, 'case': 'long and NUL refuse without writes', 'equal': True})
assert digest == hashlib.sha256(binary.read_bytes()).hexdigest()
assert harness_digest == hashlib.sha256(Path(__file__).read_bytes()).hexdigest()
assert results and all(item['equal'] for item in results), [item for item in results if not item['equal']]
print(json.dumps({'status': 'pass', 'scope': 'Long status reference subset; no native operation mutator or complete Git parity claim',
                  'binary_sha256': digest, 'harness_sha256': harness_digest,
                  'checks': [item['object_format'] + ': ' + item['case'] + ' ' + ' '.join(item.get('options', item.get('arguments', [])))
                             for item in results], 'runtime_git_ssh_on_path': False}, indent=2))
