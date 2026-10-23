ARG BASE=debian@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587
ARG SERVER_BASE=debian@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587
FROM ${BASE} AS client
RUN apt-get update && apt-get install --no-install-recommends -y ca-certificates libicu72 strace && rm -rf /var/lib/apt/lists/*
RUN test ! -e /usr/bin/git && test ! -e /usr/bin/ssh && test ! -d /usr/lib/git-core
RUN sed -i 's@:/root:@:/migration/home:@' /etc/passwd && mkdir -p /harness && touch /harness/Cargo.toml /harness/compatibility.json
ENV GIT_NEXT_ROOT=/harness
WORKDIR /migration

FROM ${SERVER_BASE} AS server
RUN apt-get update && apt-get install --no-install-recommends -y git git-lfs ca-certificates libicu72 && rm -rf /var/lib/apt/lists/*
RUN mkdir -p /harness && touch /harness/Cargo.toml /harness/compatibility.json
ENV GIT_NEXT_ROOT=/harness
WORKDIR /migration
