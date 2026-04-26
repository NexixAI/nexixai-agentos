.PHONY: test test-race test-integration test-all coverage vuln-check load-test docker-build docker-push k8s-apply

test:
	go test ./...

test-race:
	go test -race ./...

test-integration:
	go test -race -tags integration ./internal/storage/postgres/...

test-all: test-race test-integration

coverage:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

vuln-check:
	govulncheck ./...

load-test:
	k6 run tests/load/chat_burst.js
	k6 run tests/load/multi_tenant.js
	k6 run tests/load/federation.js

docker-build:
	docker build -t ghcr.io/nexixai/agentos:latest .

docker-push:
	docker push ghcr.io/nexixai/agentos:latest

k8s-apply:
	kubectl apply -k deploy/k8s/overlays/local-dev
