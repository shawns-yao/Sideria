.PHONY: build test check web clean
build: web
	mkdir -p bin
	go build -trimpath -o bin/sideria-server ./cmd/server
	go build -trimpath -o bin/sideria-agent ./cmd/agent
web:
	npm --prefix web ci
	npm --prefix web run build
test:
	go test -race ./cmd/... ./internal/... ./web
	npm --prefix web run test
check:
	python3 -c 'import ast,pathlib; ast.parse(pathlib.Path("scripts/database-backup.py").read_text())'
	@for script in scripts/*.mjs; do node --check "$$script" || exit; done
	web/node_modules/.bin/prettier --check scripts/*.mjs
	@test -z "$$(gofmt -l cmd internal web/embed.go)"
	go vet ./cmd/... ./internal/... ./web
	npm --prefix web run typecheck
	npm --prefix web run lint
	npm --prefix web run format:check
clean:
	rm -rf bin web/dist
