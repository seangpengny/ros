GO ?= go
BIN_DIR := bin

.PHONY: format test race vet build capacity recovery session-agent session-agent-stack validate-deploy verify-ipa clean server protocol-client phase4 phase11 phase11-backend

format:
	$(GO)fmt -w $$(find cmd internal -name '*.go' -type f)

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

build:
	mkdir -p $(BIN_DIR)
	$(GO) build -o $(BIN_DIR)/api-server ./cmd/api-server
	$(GO) build -o $(BIN_DIR)/game-server ./cmd/game-server
	$(GO) build -o $(BIN_DIR)/protocol-client ./cmd/protocol-client
	$(GO) build -o $(BIN_DIR)/storage-smoke ./cmd/storage-smoke

capacity: build
	/usr/bin/python3 scripts/phase11/capacity-benchmark.py

recovery: build
	/usr/bin/python3 scripts/phase11/recovery-test.py

session-agent: build
	/usr/bin/python3 scripts/phase11/session-agent-test.py

session-agent-stack: build
	scripts/phase11/run-session-agent.sh

validate-deploy:
	scripts/phase8/validate.sh

server: build
	$(BIN_DIR)/game-server

protocol-client: build
	$(BIN_DIR)/protocol-client

phase4:
	scripts/phase4/verify.sh

phase11:
	scripts/phase11/run.sh

phase11-backend:
	PROBE_LEGACY_IPA=0 RUN_XCTEST=0 KEEP_SERVICES=0 scripts/phase11/run.sh

verify-ipa:
	scripts/phase1/verify-ipa.sh

clean:
	find $(BIN_DIR) -type f -maxdepth 1 -delete
