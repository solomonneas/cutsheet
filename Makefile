.PHONY: test vet build demo docker-build ui sample-report screenshot clean

BINARY ?= cutsheet
CLI_BINARY ?= cutsheet-cli
VERSION ?= $(shell git describe --tags --always)
DEMO_DIR ?= ./demo-data
DOCKER_IMAGE ?= cutsheet:latest
SAMPLE_OUT ?= ./reports/change-001
SCREENSHOT_OUT ?= ./docs/assets/cutsheet-report.png
CHROME ?= google-chrome

test:
	go test ./...

vet:
	go vet ./...

# Builds both binaries: the server/platform (cutsheet) and the offline diff
# CLI (cutsheet-cli). The server reports VERSION via /healthz.
build:
	go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY) ./cmd/cutsheet
	go build -o $(CLI_BINARY) ./cmd/cutsheet-cli

# Seeds a zero-hardware demo data dir with sample devices and analyzed
# changes (refuses to touch a non-empty directory).
demo:
	go run ./cmd/cutsheet demo --data-dir $(DEMO_DIR)

# The build context excludes .git (see .dockerignore), so the version is
# resolved here and passed in as a build arg.
docker-build:
	docker build --build-arg VERSION=$(VERSION) -t $(DOCKER_IMAGE) .

# Rebuilds the web UI into web/dist, which is committed to git and embedded
# into the server binary via go:embed. Run `make ui` then rebuild the server
# after changing anything under web/src. A plain `go build ./cmd/cutsheet`
# works without Node because web/dist is checked in.
ui:
	cd web && npm ci && npm run build

sample-report:
	go run ./cmd/cutsheet-cli explain \
		--before ./testdata/sample-before.cfg \
		--after ./testdata/sample-after.cfg \
		--vendor auto \
		--out $(SAMPLE_OUT)

screenshot: sample-report
	mkdir -p $(dir $(SCREENSHOT_OUT))
	$(CHROME) --headless=new --disable-gpu --no-sandbox \
		--window-size=1440,1050 \
		--screenshot=$(SCREENSHOT_OUT) \
		file://$(abspath $(SAMPLE_OUT))/report.html

clean:
	rm -f $(BINARY) $(CLI_BINARY)
	rm -rf ./reports
