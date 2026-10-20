ARG BASE
FROM ${BASE}
RUN apt-get update && apt-get install --no-install-recommends -y python3 python3-cryptography git strace ca-certificates && rm -rf /var/lib/apt/lists/*
ENV HOME=/tmp/home USERPROFILE=/tmp/home SSH_ROOT_PATH=/tmp/ssh PYTHONDONTWRITEBYTECODE=1
WORKDIR /acceptance
ENTRYPOINT ["python3", "/harness/native_run.py"]
