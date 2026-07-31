.PHONY: all flutter-web copy-web server run clean check-test-automation test-e2e-tooling maestro-lab-smoke

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

all: flutter-web copy-web server

flutter-web:
	cd app && flutter pub get && flutter build web --release

copy-web: flutter-web
	rm -rf server/internal/web/dist/*
	cp -r app/build/web/* server/internal/web/dist/

server: copy-web
	cd server && CGO_ENABLED=0 go build -ldflags "-X github.com/windoze95/cantinarr-server/internal/version.Version=$(VERSION)" -o cantinarr-server ./cmd/server

run: all
	./server/cantinarr-server

clean:
	rm -rf app/build/web
	rm -rf server/internal/web/dist/*
	touch server/internal/web/dist/.gitkeep
	rm -f server/cantinarr-server

check-test-automation: test-e2e-tooling
	python3 scripts/check_test_automation.py

test-e2e-tooling:
	python3 -m unittest discover -s scripts/tests -p 'test_*.py'
	/bin/bash scripts/tests/maestro-runner-test.sh

maestro-lab-smoke:
	scripts/run-maestro-lab.sh $(E2E_ARGS)
