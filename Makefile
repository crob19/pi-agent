.PHONY: build build-pi run clean

BINARY  := pi-agent

# Default: build for the current platform
build:
	CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o $(BINARY) .

# Cross-compile for Raspberry Pi (linux/arm64).
# Requires: apt-get install gcc-aarch64-linux-gnu
# Produces a statically-linked binary with no runtime dependencies.
build-pi:
	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 CC=aarch64-linux-gnu-gcc \
		go build -trimpath -ldflags='-s -w -extldflags "-static"' -o $(BINARY)-linux-arm64 .

run: build
	./$(BINARY)

clean:
	rm -f $(BINARY) $(BINARY)-linux-arm64
