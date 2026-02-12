#!/bin/bash
set -euo pipefail

echo "Running unit tests with coverage..."

# Generate coverage report using the same test scope as current `make test`
# This aligns with GO_TEST_PACKAGES=./pkg/... in the Makefile
go test -mod=vendor -race -coverprofile=coverage.out -covermode=atomic ./pkg/...

echo "Coverage report generated: coverage.out"

# Only upload to Codecov if token is provided (for CI environments)
if [[ -n "${CODECOV_TOKEN:-}" ]]; then
    echo "Uploading coverage to Codecov..."

    # Download Codecov CLI (more reliable than codecov-action in Prow)
    # Detect platform and download appropriate binary
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    case "$OS" in
        linux*)
            CODECOV_URL="https://cli.codecov.io/latest/linux/codecov"
            ;;
        darwin*)
            CODECOV_URL="https://cli.codecov.io/latest/macos/codecov"
            ;;
        *)
            echo "Unsupported OS: $OS"
            exit 1
            ;;
    esac

    curl -Os "$CODECOV_URL"
    chmod +x codecov

    # Upload with unit-tests flag for proper categorization
    ./codecov upload-process \
        --token "${CODECOV_TOKEN}" \
        --slug "openshift/kueue-operator" \
        --flag unit-tests \
        --file coverage.out \
        --branch "${PULL_BASE_REF:-main}"

    echo "Coverage uploaded successfully!"
else
    echo "CODECOV_TOKEN not provided, skipping upload (local development)"
fi