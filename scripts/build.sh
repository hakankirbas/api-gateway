#!/bin/bash

set -e

BINARY_NAME="gateway"

MAIN_PACKAGE="./cmd/gateway"

echo "Go API Gateway app compiling..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w" -o ${BINARY_NAME} ${MAIN_PACKAGE}

echo "Compilation completed! Binary: ${BINARY_NAME}"
echo "To run the binary: ./${BINARY_NAME}"
