ARG BASE
FROM ${BASE}
RUN apt-get update && apt-get install --no-install-recommends -y git strace ca-certificates && rm -rf /var/lib/apt/lists/*
ENV HOME=/tmp/home USERPROFILE=/tmp/home SSH_ROOT_PATH=/tmp/ssh GIT_NEXT_ROOT=/repo
WORKDIR /acceptance
ENTRYPOINT ["/harness/git-next-tool", "native-docker-run"]
