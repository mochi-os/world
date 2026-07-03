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

clean:
	rm -f $(bin)/mochi-world

.PHONY: all run1 test clean
