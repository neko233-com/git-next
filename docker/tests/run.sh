#!/bin/sh
# Copyright 2026 git-next contributors. MIT license.
#
# Run the suite with a hard process and memory ceiling. A regression that starts a copy of
# itself per invocation now dies inside the container instead of taking the workstation
# with it: --pids-limit is the ceiling the container enforces, --memory is the second one.
#
#   docker/tests/run.sh              unit and integration tests
#   docker/tests/run.sh defaults     the runtime defaults agreement check
#   docker/tests/run.sh stress       the same image, concurrent invocation burst
#   docker/tests/run.sh shell        a shell inside the image, for debugging
set -eu

# Under Git Bash, MSYS rewrites arguments that look like paths, so -w /src would arrive at
# the daemon as a path inside the MSYS installation. Every mode passes container paths.
export MSYS_NO_PATHCONV=1

mode="${1:-test}"
image="git-next-tests:local"
pids="${GIT_NEXT_TEST_PIDS:-128}"
memory="${GIT_NEXT_TEST_MEMORY:-2g}"
cpus="${GIT_NEXT_TEST_CPUS:-2}"

root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)

# Only the build-image modes need the build image. The defaults check runs in a plain
# Python image, so a broken Cargo cache cannot block it.
case "$mode" in
test|stress|shell)
    if ! docker image inspect "$image" >/dev/null 2>&1; then
        docker build -t "$image" -f "$root/docker/tests/Dockerfile" "$root"
    fi
    ;;
esac

case "$mode" in
test)
    docker run --rm --init --pids-limit "$pids" --memory "$memory" --cpus "$cpus" \
        -v "$root:/src" -v "git-next-target:/src/target" -w /src "$image" \
        cargo test --release
    ;;
stress)
    docker run --rm --init --pids-limit "$pids" --memory "$memory" --cpus "$cpus" \
        -v "$root:/src" -v "git-next-target:/src/target" -w /src "$image" \
        sh docker/tests/stress.sh
    ;;
defaults)
    # The behavioural half needs a real Windows binary, so the container proves the half
    # that can be proven anywhere: that every consumer of the defaults agrees. It needs
    # Python and nothing else, so it uses its own image instead of the build image and
    # costs a pull rather than a Rust toolchain.
    docker run --rm --init --pids-limit "$pids" --memory "$memory" --cpus "$cpus" \
        -v "$root:/src" -w /src python:3.13-alpine \
        python tools/check_runtime_defaults.py --agreement-only
    ;;
shell)
    docker run --rm -it --init --pids-limit "$pids" --memory "$memory" --cpus "$cpus" \
        -v "$root:/src" -v "git-next-target:/src/target" -w /src "$image" sh
    ;;
*)
    echo "usage: $0 [test|stress|shell]" >&2
    exit 2
    ;;
esac
