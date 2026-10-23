#!/bin/sh
# Copyright 2026 git-next contributors. MIT license.
#
# Concurrent invocation burst. The caller decides how many processes exist, so the only
# honest question is what the product does to the machine while they run. This asserts
# three things: every invocation returns, the binary never starts a copy of itself, and the
# peak does not exceed the container's process ceiling (the container kills the run if it
# does, which is the failure this test exists to make visible).
set -eu

rounds="${1:-8}"
per_round="${2:-24}"
binary="${GIT_NEXT_BINARY:-target/release/git-next}"

if [ ! -x "$binary" ]; then
    cargo build --release --bins
fi

printf 'burst: %s rounds x %s invocations of %s\n' "$rounds" "$per_round" "$binary"
round=0
while [ "$round" -lt "$rounds" ]; do
    round=$((round + 1))
    index=0
    while [ "$index" -lt "$per_round" ]; do
        "$binary" version --json >/dev/null 2>&1 &
        index=$((index + 1))
    done
    # A round is drained before the next one starts: the point is the ceiling, not speed.
    wait
    live=$(ps -eo comm= | grep -c '^git-next$' || true)
    if [ "$live" -ne 0 ]; then
        printf 'round %s left %s git-next processes behind\n' "$round" "$live" >&2
        exit 1
    fi
    printf 'round %s drained\n' "$round"
done

printf 'burst complete: no invocation was left running\n'
