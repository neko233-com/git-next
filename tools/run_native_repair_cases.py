"""Rerun reference-dependent Windows cases after a fixture-only failure."""
import argparse
from concurrent.futures import ThreadPoolExecutor
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import time

from terminal_runtime_fixture import prepare_terminal_runtime

ROOT = Path(__file__).resolve().parents[1]
CASES = {'windows-core-contracts', 'windows-common-cli-contracts', 'windows-dpapi-migration', 'terminal-native-desktop'}


def sha(path):
    with Path(path).open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def main():
    parser = argparse.ArgumentParser(__doc__)
    for name in ('native-packages', 'reference', 'output'):
        parser.add_argument('--'+name, type=Path, required=True)
    args = parser.parse_args(); matrix = json.loads(args.native_packages.read_text())
    for path, digest in matrix['files'].items():
        assert sha(path) == digest, path
    for path, digest in matrix['harnesses'].items():
        assert sha(ROOT/path) == digest, path
    reference = args.reference.resolve(strict=True)
    env = dict(os.environ, PATH=str(reference.parent)+os.pathsep+os.environ.get('PATH', ''),
               GIT_TERMINAL_PROMPT='0', LANG='C', LC_ALL='C')
    core = subprocess.check_output([str(reference), '--exec-path'], env=env, text=True).strip()
    backend = Path(core)/'git-http-backend.exe'
    assert backend.is_file(), 'The complete reference HTTP backend is required before running fixtures'
    output = args.output.resolve(); output.mkdir(parents=True, exist_ok=False)
    win = matrix['platforms']['windows-x64']; binary = Path(win['directory'])/'bin/git.exe'
    terminal = prepare_terminal_runtime(win['directory'], output/'terminal-runtime')
    cases = [
        ('terminal-native-desktop', [sys.executable, str(ROOT.parent/'git-terminal/tests/windows_desktop_acceptance.py'), '--binary', str(terminal/'bin/git-terminal.exe'), '--git', str(terminal/'bin/git.exe'), '--output', str(output/'terminal-desktop.json')]),
        ('windows-core-contracts', [sys.executable, 'tests/native_contract_acceptance.py', str(binary), '--reference', str(reference), '--output', str(output/'windows-core.json')]),
        ('windows-common-cli-contracts', [sys.executable, 'tests/native_common_cli_acceptance.py', str(binary), '--reference', str(reference), '--output', str(output/'windows-common-cli.json')]),
        ('windows-dpapi-migration', [sys.executable, 'tests/gcm_acceptance.py', str(binary), '--helper', win['gcm'], '--native-distribution']),
    ]
    def run(case):
        name, command = case; start = time.monotonic()
        p = subprocess.run(command, cwd=ROOT, env=env, capture_output=True, timeout=1200,
                           creationflags=subprocess.CREATE_NO_WINDOW)
        log = output/(name+'.log'); log.write_bytes(p.stdout+b'\n'+p.stderr)
        result = dict(case=name, status='pass' if p.returncode == 0 else 'fail', exit_code=p.returncode,
                      command=command, seconds=round(time.monotonic()-start, 3), log_sha256=sha(log))
        print(name+' => '+result['status'], flush=True)
        return result
    started = time.monotonic(); results = [run(cases[0])]
    with ThreadPoolExecutor(3) as pool: results.extend(pool.map(run, cases[1:]))
    unchanged = all(sha(path) == digest for path, digest in matrix['files'].items())
    unchanged = unchanged and all(sha(ROOT/path) == digest for path, digest in matrix['harnesses'].items())
    report = dict(schema_version=1, status='pass' if unchanged and all(x['status'] == 'pass' for x in results) else 'fail',
                  scope='unchanged product and test sources; complete reference fixture; four actual Windows case reruns',
                  matrix_sha256=sha(args.native_packages), reference_sha256=sha(reference),
                  server_backend_sha256=sha(backend), source_tree_sha256=matrix['source_tree_sha256'],
                  binary_sha256=sha(binary), harnesses=matrix['harnesses'], frozen_input_readback=unchanged,
                  results=results, seconds=round(time.monotonic()-started, 3))
    (output/'repair.json').write_text(json.dumps(report, indent=2)+'\n', encoding='utf-8', newline='\n')
    raise SystemExit(report['status'] != 'pass')


if __name__ == '__main__':
    main()
