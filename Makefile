.PHONY: test build controller agent dispatcher frontend

test:
	go test -race ./...
	cd frontend && npm run build

build: controller agent dispatcher frontend

controller:
	CGO_ENABLED=0 go build -trimpath -o bin/alicdt-controller ./cmd/controller

agent:
	CGO_ENABLED=0 go build -trimpath -o bin/cdt-relay-agent ./cmd/relay-agent

dispatcher:
	CGO_ENABLED=0 go build -trimpath -o bin/cdt-dispatcher ./cmd/dispatcher

frontend:
	cd frontend && npm run build
