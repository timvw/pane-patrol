FROM golang:1.25-bookworm

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    tmux \
    python3 \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /workspace
