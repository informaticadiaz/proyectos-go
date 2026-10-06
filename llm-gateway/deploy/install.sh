#!/usr/bin/env bash
# Builds the gateway and (re)installs it as a systemd user service.
# Client keys are managed separately; see README.md, "Deploy".
set -euo pipefail

cd "$(dirname "$0")/.."

bin_dir="$HOME/proyectos-go/data/llm-gateway"
unit_dir="$HOME/.config/systemd/user"
mkdir -p "$bin_dir" "$unit_dir"

go test -race -count=1 ./...

# Build next to the target and rename, so a running service never sees a
# half-written binary.
go build -trimpath -o "$bin_dir/gateway.new" ./cmd/gateway
mv "$bin_dir/gateway.new" "$bin_dir/gateway"

install -m 0644 deploy/llm-gateway.service "$unit_dir/llm-gateway.service"
systemctl --user daemon-reload
systemctl --user enable llm-gateway.service
systemctl --user restart llm-gateway.service
systemctl --user --no-pager status llm-gateway.service
