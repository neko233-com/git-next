ARG BASE=python:3.14-slim@sha256:a2b82f3c48559aa0a8446d9af49826b6e2b2016f4cd2afabfe6013ec53729170
ARG SERVER_BASE=git-next-local-acceptance:20261020
FROM ${BASE} AS client
RUN apt-get update && apt-get install --no-install-recommends -y ca-certificates libicu76 strace && rm -rf /var/lib/apt/lists/*
RUN test ! -e /usr/bin/git && test ! -e /usr/bin/ssh && test ! -d /usr/lib/git-core
RUN python -c "from pathlib import Path; p=Path('/etc/passwd'); rows=[line.split(':') for line in p.read_text().splitlines()]; [row.__setitem__(5, '/migration/home') for row in rows if row[0]=='root']; p.write_text(''.join(':'.join(row)+'\n' for row in rows))"
WORKDIR /migration

FROM ${SERVER_BASE} AS server
RUN apt-get update && apt-get install --no-install-recommends -y libicu76 && rm -rf /var/lib/apt/lists/*
RUN pip install --no-cache-dir paramiko==5.0.0 cryptography==46.0.6
WORKDIR /migration
