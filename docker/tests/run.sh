#!/bin/sh
# Copyright 2026 git-next contributors. MIT license.
#
# Run the suite with a hard process and memory ceiling. A regression that starts a copy of
# itself per invocation now dies inside the container instead of taking the workstation
# with it: --pids-limit is the ceiling the container enforces, --memory is the second one.
#
#   docker/tests/run.sh              unit and integration tests
#   docker/tests/run.sh defaults     the runtime defaults agreement check
#   docker/tests/run.sh contract     every rule in AGENTS.md
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

# Only the Rust build modes need the build image; repository/default checks use Go.
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
defaults|contract)
    # The static checks use a CGO-free Linux tool; Windows behavior stays a separate gate.
    tool="$root/target/devtool/linux-amd64/git-next-tool"
    mkdir -p "$(dirname "$tool")"
    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go -C "$root/tools/devtool" build -o "$tool" .
    if [ "$mode" = contract ]; then
        command="verify-repo-contract"
    else
        command="check-runtime-defaults --agreement-only"
    fi
    docker run --rm --init --pids-limit "$pids" --memory "$memory" --cpus "$cpus" \
        --platform linux/amd64 -v "$root:/src" -w /src \
        debian@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587 \
        /src/target/devtool/linux-amd64/git-next-tool $command
    ;;
shell)
    docker run --rm -it --init --pids-limit "$pids" --memory "$memory" --cpus "$cpus" \
        -v "$root:/src" -v "git-next-target:/src/target" -w /src "$image" sh
    ;;
*)
    echo "usage: $0 [test|defaults|contract|stress|shell]" >&2
    exit 2
    ;;
esac
