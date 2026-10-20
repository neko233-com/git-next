"""Compare native long-status output with reference Git in isolated SHA-1/SHA-256 repositories."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
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
    with tempfile.TemporaryDirectory(prefix='git-next-long-operations-') as temporary:
        directory = Path(temporary)
        home, empty = directory / 'home', directory / 'empty'
        home.mkdir()
        empty.mkdir()
        env = {key: value for key, value in os.environ.items() if not key.startswith(('GIT_', 'GIT_NEXT_'))}
        env.update(HOME=str(home), USERPROFILE=str(home), SSH_ROOT_PATH=str(home / '.ssh'), XDG_CONFIG_HOME=str(home / '.config'),
                   GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=str(home / 'config'), GIT_OPTIONAL_LOCKS='0', LC_ALL='C',
                   GIT_AUTHOR_NAME='Fixture', GIT_COMMITTER_NAME='Fixture', GIT_AUTHOR_EMAIL='fixture@example.invalid',
                   GIT_COMMITTER_EMAIL='fixture@example.invalid', GIT_AUTHOR_DATE='2001-02-03T04:05:06Z', GIT_COMMITTER_DATE='2001-02-03T04:05:06Z')
        repo = directory

        def run(native, *arguments):
            variables = dict(env)
            if native:
                variables.update(PATH=str(empty), GIT_EXEC_PATH=str(empty))
            return subprocess.run([str(binary) if native else reference, *map(str, arguments)], cwd=repo,
                                  env=variables, capture_output=True, timeout=30)

        def git(*arguments):
            result = run(False, *arguments)
            assert result.returncode == 0, (arguments, result.stderr)
            return result.stdout.strip()

        def compare(label, hints=True):
            for options in ((), ('--porcelain',), ('--porcelain=v2',)):
                before = {p.relative_to(repo / '.git').as_posix(): hashlib.sha256(p.read_bytes()).hexdigest()
                          for p in (repo / '.git').rglob('*') if p.is_file()}
                prefix = () if hints else ('-c', 'advice.statusHints=false')
                expected, actual = run(False, *prefix, 'status', *options), run(True, *prefix, 'status', *options)
                after = {p.relative_to(repo / '.git').as_posix(): hashlib.sha256(p.read_bytes()).hexdigest()
                         for p in (repo / '.git').rglob('*') if p.is_file()}
                assert before == after
                results.append({'object_format': object_format, 'case': label, 'options': options,
                                'equal': actual.returncode == expected.returncode and actual.stdout == expected.stdout,
                                'reference_exit': expected.returncode, 'native_exit': actual.returncode,
                                'reference': expected.stdout.decode(), 'native': actual.stdout.decode()})

        def initialize(name):
            global repo
            repo = directory / name
            repo.mkdir()
            git('init', '-q', '-b', 'main', '--object-format=' + object_format)
            git('config', 'core.autocrlf', 'input')
            (repo / 'file.txt').write_bytes(b'base\n')
            git('add', '.')
            git('commit', '-qm', 'fixture: base')

        def diverge():
            git('checkout', '-qb', 'topic')
            (repo / 'file.txt').write_bytes(b'topic\n')
            git('commit', '-qam', 'fixture: topic')
            git('checkout', '-q', 'main')
            (repo / 'file.txt').write_bytes(b'main\n')
            git('commit', '-qam', 'fixture: main')

        initialize('cherry')
        diverge()
        assert run(False, 'cherry-pick', 'topic').returncode == 1
        compare('cherry-pick conflict')
        (repo / 'file.txt').write_bytes(b'resolved\n')
        git('add', 'file.txt')
        compare('cherry-pick resolved')
        initialize('revert')
        for number in range(2):
            (repo / 'file.txt').write_bytes(f'change {number}\n'.encode())
            git('commit', '-qam', 'fixture: change ' + str(number))
        assert run(False, 'revert', 'HEAD~1').returncode == 1
        compare('revert conflict')
        (repo / 'file.txt').write_bytes(b'resolved\n')
        git('add', 'file.txt')
        compare('revert resolved')
        initialize('bisect')
        for number in range(3):
            (repo / 'file.txt').write_bytes(f'change {number}\n'.encode())
            git('commit', '-qam', 'fixture: bisect ' + str(number))
        git('bisect', 'start', 'HEAD', 'HEAD~3')
        compare('bisect in progress')
        for interactive in (False, True):
            initialize('interactive' if interactive else 'rebase')
            diverge()
            git('checkout', '-q', 'topic')
            command = ['-c', 'sequence.editor=true', 'rebase', *(('-i',) if interactive else ()), 'main']
            assert run(False, *command).returncode == 1
            compare('interactive rebase conflict' if interactive else 'rebase conflict')
            (repo / 'file.txt').write_bytes(b'resolved\n')
            git('add', 'file.txt')
            compare('interactive rebase resolved' if interactive else 'rebase resolved')
        initialize('apply-backend')
        diverge()
        git('checkout', '-q', 'topic')
        assert run(False, 'rebase', '--apply', 'main').returncode == 1
        compare('apply backend rebase conflict')
        (repo / 'file.txt').write_bytes(b'resolved\n')
        git('add', 'file.txt')
        compare('apply backend rebase resolved')
        initialize('mailbox')
        diverge()
        patch = directory / 'change.patch'
        patch.write_bytes(git('format-patch', '-1', 'topic', '--stdout') + b'\n')
        assert run(False, 'am', patch).returncode == 128
        compare('mailbox conflict')
        (repo / 'file.txt').write_bytes(b'resolved\n')
        git('add', 'file.txt')
        compare('mailbox resolved')
        initialize('editing')
        base = git('rev-parse', 'HEAD').decode()
        git('branch', '-m', 'topic')
        for number in range(4):
            (repo / f'topic-{number}.txt').write_bytes(b'topic content\n')
            git('add', '.')
            git('commit', '-qm', f'fixture: topic {number}')
        editor = directory / 'sequence-editor.py'
        editor.write_text("from pathlib import Path\nimport sys\np=Path(sys.argv[1])\np.write_text(p.read_text().replace('pick ', 'edit ', 1))\n")
        command = f'"{Path(sys.executable).as_posix()}" "{editor.as_posix()}"'
        git('-c', 'sequence.editor=' + command, 'rebase', '-i', base)
        compare('interactive editing clean')
        compare('interactive editing clean without hints', hints=False)
        (repo / 'file.txt').write_bytes(b'editing content\n')
        compare('interactive editing dirty')
        git('add', 'file.txt')
        compare('interactive editing staged')
        git('reset', '--hard', 'HEAD~1')
        compare('interactive splitting commit')
assert digest == hashlib.sha256(binary.read_bytes()).hexdigest()
assert harness_digest == hashlib.sha256(Path(__file__).read_bytes()).hexdigest()
assert results and all(item['equal'] for item in results), [item for item in results if not item['equal']]
print(json.dumps({'status': 'pass', 'scope': 'Long status reference subset; no native operation mutator or complete Git parity claim',
                  'binary_sha256': digest, 'harness_sha256': harness_digest,
                  'checks': [item['object_format'] + ': ' + item['case'] + ' ' + ' '.join(item.get('options', item.get('arguments', [])))
                             for item in results], 'runtime_git_ssh_on_path': False}, indent=2))
