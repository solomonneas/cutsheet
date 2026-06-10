.PHONY: test vet build ui sample-report screenshot clean

BINARY ?= cutsheet
SAMPLE_OUT ?= ./reports/change-001
SCREENSHOT_OUT ?= ./docs/assets/cutsheet-report.png
CHROME ?= google-chrome

test:
	go test ./...

vet:
	go vet ./...

build:
	go build -o $(BINARY) ./cmd/cutsheet-cli

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
	rm -f $(BINARY)
	rm -rf ./reports
