"""Native language selection/persistence and language-independent Git byte contracts."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    binary = Path(sys.argv[1]).resolve(strict=True)
    reference = shutil.which('git')
    assert reference
    identity = digest(binary)
    results = []
    with tempfile.TemporaryDirectory(prefix='git-next-language-') as temporary:
        root = Path(temporary)
        home = root / 'home'
        empty = root / 'empty'
        settings = root / 'language' / 'settings.json'
        for path in (home, empty, settings.parent):
            path.mkdir()
        env = {key: value for key, value in os.environ.items() if not key.startswith('GIT_') and key not in ('LANG','LANGUAGE','LC_ALL','LC_MESSAGES')}
        env.update(HOME=str(home), USERPROFILE=str(home), XDG_CONFIG_HOME=str(home / '.config'),
                   GIT_NEXT_CONFIG_HOME=str(settings.parent), GIT_CONFIG_NOSYSTEM='1',
                   GIT_CONFIG_GLOBAL=str(home / 'gitconfig'), GIT_TERMINAL_PROMPT='0',
                   GIT_AUTHOR_NAME='Language Fixture', GIT_COMMITTER_NAME='Language Fixture',
                   GIT_AUTHOR_EMAIL='fixture@example.invalid', GIT_COMMITTER_EMAIL='fixture@example.invalid',
                   LC_ALL='C')

        def run(*arguments, native=True, changes=None, cwd=None):
            variables = dict(env)
            if native:
                variables.update(PATH=str(empty), GIT_EXEC_PATH=str(empty))
            for key, value in (changes or {}).items():
                if value is None:
                    variables.pop(key, None)
                else:
                    variables[key] = value
            return subprocess.run([str(binary) if native else reference, *map(str, arguments)], cwd=cwd or root,
                                  env=variables, capture_output=True, timeout=30)

        def good(label, *arguments, **options):
            response = run(*arguments, **options)
            assert response.returncode == 0, (label, response.stderr.decode(errors='replace'))
            results.append(label)
            return response.stdout

        assert good('C auto fallback', 'language', 'get') == b'english\n'
        assert not settings.exists(), 'read-only language discovery wrote configuration'
        assert good('locale zh_CN', 'language', 'get', changes={'LC_ALL':'zh_CN.UTF-8'}) == b'chinese\n'
        assert good('locale zh-TW', 'language', 'get', changes={'LC_ALL':'zh-TW'}) == b'chinese\n'
        assert good('unsupported locale fallback', 'language', 'get', changes={'LC_ALL':'fr_FR.UTF-8'}) == b'english\n'
        assert good('locale order', 'language', 'get', changes={'LC_ALL':'C','LC_MESSAGES':'zh_CN.UTF-8'}) == b'english\n'
        assert good('language preference list', 'language', 'get', changes={'LC_ALL':None,'LANGUAGE':'fr:zh_CN:en'}) == b'chinese\n'
        native_system = json.loads(good('native OS discovery', 'language', 'get', '--json', changes={'LC_ALL':None}))
        assert native_system['source'] == 'system' and native_system['language'] in ('english','chinese')
        listing = good('language list', 'language', 'list').decode()
        assert all(value in listing for value in ('english','chinese','auto','简体中文'))
        for alias in ('chinese','Chinese','zh','zh-CN','zh_cn','zh-Hans','中文'):
            assert '中文' in good('persist ' + alias, 'language', 'set', alias).decode()
            assert good('persisted child ' + alias, 'language', 'get') == b'chinese\n'
            saved = settings.read_bytes()
            assert not saved.startswith(b'\xef\xbb\xbf') and b'\r' not in saved and saved.endswith(b'\n')
        assert good('single command override', '--language', 'english', 'language', 'get') == b'english\n'
        assert good('equals override', '--language=en', 'language', 'get') == b'english\n'
        assert good('environment override', 'language', 'get', changes={'GIT_NEXT_LANGUAGE':'en-US'}) == b'english\n'
        assert good('CLI wins over environment', '--language', 'chinese', 'language', 'get', changes={'GIT_NEXT_LANGUAGE':'english'}) == b'chinese\n'
        assert good('override is not persistent', 'language', 'get') == b'chinese\n'
        report = json.loads(good('get JSON contract', 'language', 'get', '--json'))
        assert report == {'schema_version':1,'language':'chinese','configured':'chinese','source':'settings','supported':['english','chinese']}
        for arguments in (('--help',), ('language','--help'), ('language','set','--help'), ('status','--help')):
            output = good('Chinese help ' + ' '.join(arguments), *arguments).decode()
            assert '用法:' in output and 'Usage:' not in output
        assert '管理界面语言' in good('root Chinese descriptions', '--help').decode()
        english_help = good('English help', '--language','english','--help').decode()
        assert 'Usage:' in english_help and '用法:' not in english_help
        before = settings.read_bytes()
        for arguments in (('language','set','klingon'), ('--language','klingon','status'), ('language','set')):
            rejected = run(*arguments)
            assert rejected.returncode != 0 and settings.read_bytes() == before
            results.append('invalid selection rejected ' + ' '.join(arguments))
        saved = json.loads(before)
        saved['future_option'] = {'preserve':'未知值'}
        settings.write_text(json.dumps(saved, ensure_ascii=False), encoding='utf-8')
        good('unknown settings preserved', 'language','set','english')
        assert json.loads(settings.read_text())['future_option'] == saved['future_option']
        for invalid in (b'{broken', b'{"schema_version":99,"language":"english"}', b'{"schema_version":1,"language":"unsupported"}', b'x' * 65537):
            settings.write_bytes(invalid)
            rejected = run('language','get')
            assert rejected.returncode != 0 and settings.read_bytes() == invalid
            results.append('invalid settings preserved ' + str(len(invalid)))
        settings.write_bytes(before)
        lock = settings.with_name('settings.json.lock')
        lock.write_bytes(b'owned by fixture\n')
        rejected = run('language','set','english')
        assert rejected.returncode != 0 and settings.read_bytes() == before and lock.read_bytes() == b'owned by fixture\n'
        lock.unlink()
        results.append('lock contention preserves both files')
        good('reset auto', 'language','set','auto')
        assert good('auto uses new locale', 'language','get', changes={'LC_ALL':'zh_CN.UTF-8'}) == b'chinese\n'
        for object_format in ('sha1','sha256'):
            repo = root / ('项目 {count} On branch ' + object_format)
            good('seed ' + object_format, 'init','-q','-b','feature/{name}','--object-format=' + object_format,repo,native=False)
            path = repo / 'On branch {count} 中文.txt'
            path.write_bytes(b'one\ntwo\n')
            good('seed add ' + object_format,'add','--all',native=False,cwd=repo)
            good('seed commit ' + object_format,'commit','-qm','On branch {name}',native=False,cwd=repo)
            path.write_bytes(b'changed\n')
            (repo / 'new {name}.txt').write_bytes(b'new\n')
            profile_before = digest(home / 'gitconfig') if (home / 'gitconfig').exists() else None
            for language in ('chinese','english','auto'):
                git_before = {p.relative_to(repo).as_posix():digest(p) for p in repo.rglob('*') if p.is_file()}
                good('language does not mutate repository ' + object_format + language,'language','set',language,cwd=repo)
                assert git_before == {p.relative_to(repo).as_posix():digest(p) for p in repo.rglob('*') if p.is_file()}
            for command in (('status','--porcelain=v1','-z'), ('status','--porcelain=v2','--branch','-z'),
                            ('config','--list','--null'), ('rev-parse','HEAD'), ('ls-files','--stage'),
                            ('show-ref',), ('cat-file','-p','HEAD'), ('diff','--no-ext-diff','--no-textconv'),
                            ('log','-n','1','--format=%H%x00%s'), ('version','--json'), ('doctor','--json'), ('eol','--json')):
                english = good('machine English ' + object_format + ' '.join(command), '--language','english',*command,cwd=repo)
                chinese = good('machine Chinese ' + object_format + ' '.join(command), '--language','chinese',*command,cwd=repo)
                assert english == chinese, command
            human = good('Chinese long status ' + object_format,'--language','chinese','status',cwd=repo).decode()
            assert '位于分支 feature/{name}' in human and '尚未暂存的变更' in human and '未跟踪的文件' in human
            assert '{count}' in human and '{name}' in human
            assert 'Changes not staged for commit:' not in human
            outside = run('--language','chinese','status')
            assert outside.returncode == 128 and '致命错误：' in outside.stderr.decode() and '当前目录不是 Git 仓库' in outside.stderr.decode()
            results.append('Chinese diagnostic ' + object_format)
            assert profile_before == (digest(home / 'gitconfig') if (home / 'gitconfig').exists() else None)
            good('reference fsck ' + object_format,'fsck','--full',native=False,cwd=repo)
        assert not list(root.rglob('*.lock'))
    assert digest(binary) == identity
    print(json.dumps({'status':'pass','binary_sha256':identity,'harness_sha256':digest(Path(__file__)),
                      'scope':'native English/Chinese selection, persistence, help/human status and byte-stable machine formats; no full Git/IDE acceptance',
                      'checks':results}, indent=2))


if __name__ == '__main__':
    main()
