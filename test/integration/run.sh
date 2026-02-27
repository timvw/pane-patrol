#!/usr/bin/env bash
set -euo pipefail

docker compose -f test/integration/docker-compose.yml run --rm pane-patrol-int
