module world

go 1.25.0

// Build with go1.25.12+, matching core: the stdlib fixes it carries are the
// whole remedy for the HTTP/2 SETTINGS flood, the TLS KeyUpdate retention, both
// X.509 validation defects, certificate hostname parsing, ECH, net/textproto,
// net/url and os.Root — every one of which is reachable from the public lobby
// and WebTransport listeners. GOTOOLCHAIN=auto treats this as a floor: a newer
// host Go is used as-is, an older one auto-downloads 1.25.12. The x/ packages
// track core's versions for the same reason, x/text being the one with a
// vulnerability of its own (an infinite loop on invalid input, reached through
// x/net/idna when a hostname is parsed).
// Re-evaluate when bumping the go directive or moving to the 1.26 line.
toolchain go1.25.12

require (
	github.com/fxamacker/cbor/v2 v2.9.2
	github.com/quic-go/quic-go v0.60.0
	github.com/quic-go/webtransport-go v0.11.0
	golang.org/x/crypto v0.53.0
	golang.org/x/sys v0.46.0
	gopkg.in/ini.v1 v1.67.3
)

require (
	github.com/dunglas/httpsfv v1.1.0 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/text v0.39.0 // indirect
)
