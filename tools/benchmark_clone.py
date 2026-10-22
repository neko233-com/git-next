"""Benchmark `clone` against a real remote.

The comparison that matters for a user is wall-clock time to a working tree, from
a remote that behaves like the one they use. This runs the reference Git and this
client against the same URL, alternating so that neither benefits from a warm cache
the other did not have, and reports the median of the runs after a discarded warm-up.

It reports the phase split of this client as well, because a clone is dominated by
different things on a small and a large repository: on a small one the server spends
most of the time building the pack, on a large one the transfer and the disk do.

Usage:
    python tools/benchmark_clone.py --target gitsss=http://127.0.0.1:3000/root/bench-big.git
    python tools/benchmark_clone.py --reps 5 \
        --target gitsss=http://127.0.0.1:3000/root/bench-big.git \
        --target github=https://github.com/neko233-com/git-next \
        --json docs/benchmarks/clone-20261022.json
"""
from __future__ import annotations

import argparse
import json
import os
import platform
import shutil
import statistics
import subprocess
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
WORK = ROOT / 'target' / 'bench-clone'
TRACE = 'GIT_NEXT_TRACE_TIMING'


def client_commands() -> list[tuple[str, list[str]]]:
    """The two clients, as (label, argv prefix)."""
    git_next = ROOT / 'target' / 'release' / ('git-next.exe' if os.name == 'nt' else 'git-next')
    if not git_next.is_file():
        raise SystemExit(f'build the client first: {git_next} is missing')
    return [
        ('git', ['git']),
        ('git-next', [str(git_next)]),
    ]


def run_once(argv: list[str], url: str, destination: Path, extra: dict[str, str]) -> tuple[float, str]:
    """Clone once, returning milliseconds and the client's phase trace."""
    shutil.rmtree(destination, ignore_errors=True)
    environment = dict(os.environ)
    environment[TRACE] = '1'
    # Configuration is passed through the environment so that both clients receive
    # exactly the same settings. A backend that one of them does not know is simply
    # ignored by it, which keeps the comparison honest.
    for index, (key, value) in enumerate(extra.items()):
        environment[f'GIT_CONFIG_KEY_{index}'] = key
        environment[f'GIT_CONFIG_VALUE_{index}'] = value
    if extra:
        environment['GIT_CONFIG_COUNT'] = str(len(extra))
    started = time.perf_counter()
    result = subprocess.run(
        [*argv, 'clone', url, str(destination)],
        capture_output=True,
        text=True,
        encoding='utf-8',
        errors='replace',
        env=environment,
        check=False,
    )
    elapsed = (time.perf_counter() - started) * 1000
    if result.returncode != 0:
        detail = (result.stderr or result.stdout or '').strip().splitlines()
        raise SystemExit(f'{" ".join(argv)} clone failed: {detail[-1] if detail else "no output"}')
    trace = ''
    for line in result.stderr.splitlines():
        if line.startswith('trace:'):
            trace = line
    return elapsed, trace


def repository_bytes(destination: Path) -> int:
    """The size of the checkout, as a sanity check that the clone is complete."""
    total = 0
    for path in destination.rglob('*'):
        if path.is_file():
            total += path.stat().st_size
    return total


def bench(clients: list[tuple[str, list[str]]], url: str, reps: int, extra: dict[str, str]) -> list[dict]:
    """Run the clients against one target, interleaved round by round.

    Running one client to completion and then the other would let the machine and the
    host warm up for whichever went second, and the size of that effect is larger than
    the difference being measured. Every round therefore runs both clients, and the
    order is swapped each round so neither systematically goes first.
    """
    timings: dict[str, list[float]] = {name: [] for name, _ in clients}
    traces: dict[str, str] = {}
    sizes: dict[str, int] = {}

    for index in range(reps + 1):
        order = clients if index % 2 == 0 else list(reversed(clients))
        for name, argv in order:
            destination = WORK / f'out-{name}'
            elapsed, trace = run_once(argv, url, destination, extra)
            # The first round pays for the file-system cache and, on a remote host,
            # for the connection; it is discarded rather than reported as a result.
            if index == 0:
                continue
            timings[name].append(elapsed)
            if trace:
                traces[name] = trace
            sizes[name] = repository_bytes(destination)

    results = []
    for name, _ in clients:
        values = timings[name]
        if not values:
            continue
        results.append(
            {
                'client': name,
                'runs_ms': [round(value, 1) for value in values],
                'median_ms': round(statistics.median(values), 1),
                'min_ms': round(min(values), 1),
                'max_ms': round(max(values), 1),
                'checkout_bytes': sizes.get(name, 0),
                'phase_trace': traces.get(name, ''),
            }
        )
    for name, _ in clients:
        shutil.rmtree(WORK / f'out-{name}', ignore_errors=True)
    return results


def main() -> int:
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument(
        '--target',
        action='append',
        required=True,
        metavar='LABEL=URL',
        help='a remote to clone, with a label used in the report',
    )
    parser.add_argument('--reps', type=int, default=5, help='measured runs per client (default 5)')
    parser.add_argument(
        '--config',
        action='append',
        default=[],
        metavar='KEY=VALUE',
        help='git configuration exported to both clients (repeatable)',
    )
    parser.add_argument('--json', type=Path, help='write the result to this file')
    arguments = parser.parse_args()

    targets: list[tuple[str, str]] = []
    for entry in arguments.target:
        if '=' not in entry:
            raise SystemExit(f'--target needs LABEL=URL, got {entry!r}')
        label, url = entry.split('=', 1)
        targets.append((label, url))

    extra: dict[str, str] = {}
    for entry in arguments.config:
        if '=' not in entry:
            raise SystemExit(f'--config needs KEY=VALUE, got {entry!r}')
        key, value = entry.split('=', 1)
        extra[key] = value

    WORK.mkdir(parents=True, exist_ok=True)
    clients = client_commands()
    report = {
        'product': 'git-next',
        'measured_at': datetime.now(timezone.utc).isoformat(timespec='seconds'),
        'platform': f'{platform.system()} {platform.machine()}',
        'reference': subprocess.run(
            ['git', '--version'], capture_output=True, text=True, check=False
        ).stdout.strip(),
        'reps': arguments.reps,
        'configuration': extra,
        'note': (
            'The first run of each client is discarded. A clone includes the server-side '
            'pack generation, which is identical for both clients; the phase trace of this '
            'client separates transfer+index from checkout but the server share is included '
            'in both wall-clock figures.'
        ),
        'targets': [],
    }

    for label, url in targets:
        print(f'\n== {label}: {url}')
        results = bench(clients, url, arguments.reps, extra)
        for result in results:
            line = (
                f"   {result['client']:<9} median {result['median_ms']:>8.0f} ms  "
                f"min {result['min_ms']:>8.0f}  max {result['max_ms']:>8.0f}"
            )
            print(line)
            if result['phase_trace']:
                print(f'             {result["phase_trace"]}')
        reference = next((entry for entry in results if entry['client'] == 'git'), None)
        subject = next((entry for entry in results if entry['client'] == 'git-next'), None)
        if reference and subject and subject['median_ms'] and reference['median_ms']:
            ratio = reference['median_ms'] / subject['median_ms']
            change = (subject['median_ms'] - reference['median_ms']) / reference['median_ms'] * 100
            print(f"   ratio       {ratio:.2f}x  ({change:+.1f}% against the reference)")
            report['targets'].append(
                {
                    'label': label,
                    'url': url,
                    'results': results,
                    'speedup_vs_reference': round(ratio, 3),
                }
            )
        else:
            report['targets'].append({'label': label, 'url': url, 'results': results})

    if arguments.json:
        arguments.json.parent.mkdir(parents=True, exist_ok=True)
        arguments.json.write_text(
            json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf-8'
        )
        print(f'\nwrote {arguments.json}')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
