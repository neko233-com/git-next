# Copyright 2026 git-next contributors. MIT license.
#
# The test suite spawns processes on purpose: a guard that is only reasoned about is a
# guard that fails silently. Running it on a workstation is how a regression becomes a
# frozen machine, so the suite runs in a container with a process ceiling of its own.
#
#   docker/tests/run.sh            unit and integration tests
#   docker/tests/run.sh stress     concurrent invocation burst
FROM rust:1-slim-bookworm

RUN apt-get update \
    && apt-get install -y --no-install-recommends pkg-config libssl-dev cmake perl make gcc \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src

# Dependencies are cached in their own layer so a source edit does not rebuild the world.
COPY Cargo.toml Cargo.lock build.rs rust-toolchain.toml /src/
RUN mkdir -p src && echo 'fn main() {}' > src/main.rs && echo 'pub fn x() {}' > src/lib.rs \
    && cargo fetch \
    && rm -rf src

COPY src /src/src
COPY locales /src/locales
COPY tests /src/tests
COPY tools /src/tools

ENV CARGO_TERM_COLOR=never
CMD ["cargo", "test", "--release"]
