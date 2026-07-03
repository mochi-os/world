version = 0.1.0
bin = ../bin
ldflags = -s -w -X main.build_version=$(version)
go_sources := $(shell find server game games -name '*.go') go.mod go.sum

all: $(bin)/mochi-world

$(bin)/mochi-world: $(go_sources) | $(bin)
	CGO_ENABLED=0 go build -v -ldflags "$(ldflags)" -o $(bin)/mochi-world ./server

$(bin):
	mkdir -p $(bin)

run1: all
	$(bin)/mochi-world -f /etc/mochi/world1.conf

test:
	go test ./...

# Run the simulation-core tests on the browser target: the golden-trace
# comparison under wasm IS the native-versus-wasm divergence bound.
test-wasm:
	GOOS=js GOARCH=wasm PATH="$(shell go env GOROOT)/lib/wasm:$$PATH" go test $(testflags) ./games/furball/flight/

# Compile-check the simulation core and its boundary for the browser target.
wasm:
	GOOS=js GOARCH=wasm CGO_ENABLED=0 go build ./games/furball/flight/ ./wasm/

clean:
	rm -f $(bin)/mochi-world

.PHONY: all run1 test test-wasm wasm clean
