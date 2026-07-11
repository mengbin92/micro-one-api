# This Makefile delegates to the repository-root Makefile.
# Usage: cd app/<service> && make build   (or make run, test, etc.)
ROOT := $(shell git rev-parse --show-toplevel 2>/dev/null || echo ../..)
SERVICE := $(notdir $(CURDIR))
CMD_DIR := ./app/$(SERVICE)/cmd/$(SERVICE)

.PHONY: api config build run test tidy proto generate wire wire-check
api:
	cd $(ROOT) && make api
config:
	cd $(ROOT) && make config
build:
	cd $(ROOT) && go build ./app/$(SERVICE)/...
run:
	cd $(ROOT) && go run $(CMD_DIR)
test:
	cd $(ROOT) && go test ./app/$(SERVICE)/...
tidy:
	cd $(ROOT) && go mod tidy
proto:
	cd $(ROOT) && make proto
generate:
	cd $(ROOT) && make generate
wire:
	cd $(ROOT) && wire $(CMD_DIR)
wire-check:
	cd $(ROOT) && go test -tags wireinject $(CMD_DIR)
