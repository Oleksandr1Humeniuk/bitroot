#!/bin/sh

set -eu

go fmt ./... || exit 1
go vet ./... || exit 1
go test ./... || exit 1
