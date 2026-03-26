#!/usr/bin/env bash
set -euo pipefail

echo "=== aiprod bootstrap ==="

# Build
echo "Building aiprod..."
go build -o aiprod ./cmd/aiprod

# Initialize
echo "Initializing data directory..."
./aiprod init

# Create default admin agent
echo "Creating admin agent..."
./aiprod auth create-agent --name admin --description "System administrator"

# Create admin API key
echo "Creating admin API key..."
./aiprod auth create-key --agent admin --scopes "*" --name "admin-key"

echo ""
echo "=== Bootstrap complete ==="
echo "Run './aiprod serve' to start the server."
echo "Set AIPROD_NO_AUTH=1 for local development without auth."
