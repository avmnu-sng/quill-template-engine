# Quill uses go-task (https://taskfile.dev) as its primary build tool.
# See Taskfile.yml; run `task --list` for the available targets.
#
# This Makefile is a thin pointer that forwards to `task` so muscle-memory
# `make <target>` still works. Install task with:
#   go install github.com/go-task/task/v3/cmd/task@latest

.PHONY: build test vet fmt check ci clean

build:
	task build

test:
	task test

vet:
	task check:vet

fmt:
	task check:fmt

check:
	task check:all

ci:
	task ci

clean:
	task clean
