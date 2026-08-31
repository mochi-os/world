// Mochi world: WebTransport data plane
// Copyright © 2026 Mochisoft OÜ
// SPDX-License-Identifier: AGPL-3.0-only
// This file is part of Mochi, licensed under the GNU AGPL v3 with the
// Mochi Application Interface Exception - see license.txt and license-exception.md.

// The realtime channel: WebTransport over HTTP/3 (QUIC, UDP). Each player
// connection carries one reliable bidirectional control stream (join,
// welcome, events, end) framed as 4-byte big-endian length + CBOR payload,
// plus unframed CBOR datagrams (inputs up, snapshots down). The wire session
// layer only sees the link interface, so a WebSocket fallback can be added
// beside this file without touching game or session code.

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
)

// connections counts live transport connections, so this path enforces the same
// ceiling listener_limit gives the TCP listeners. A per-host rate limiter bounds
// how fast one address may connect, not how many it holds open at once.
var connections atomic.Int64

// transport_admit reserves a connection slot, reporting false when the server is
// already at capacity. Reserve-then-check rather than check-then-reserve: two
// simultaneous upgrades must not both pass the last slot.
func transport_admit() bool {
	if connections.Add(1) > CONNECTIONS_MAXIMUM {
		connections.Add(-1)
		return false
	}
	return true
}

// transport_release returns a slot taken by transport_admit.
func transport_release() { connections.Add(-1) }

// HOST_CONNECTIONS_MAXIMUM caps how many transport connections ONE address may
// hold at once: the sliding-minute limiter bounds connect RATE, not holds.
// Generous, because a household behind one NAT address is ordinary.
const HOST_CONNECTIONS_MAXIMUM = 16

var (
	hosts      = map[string]int{}
	hosts_lock sync.Mutex
)

// origin reduces a remote address to the key every per-address budget counts
// against. A residential IPv6 allocation is a /64, so keying on the literal
// address would let one subscriber bypass every limit in the server for free
// by using a fresh address per request; IPv4 keeps the whole address.
func origin(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	parsed := net.ParseIP(host)
	if parsed == nil || parsed.To4() != nil {
		return host
	}
	return parsed.Mask(net.CIDRMask(64, 128)).String()
}

// transport_host_admit reserves a per-host slot, reporting false when this
// address already holds its share. Paired with transport_host_release.
func transport_host_admit(address string) bool {
	host := origin(address)
	hosts_lock.Lock()
	defer hosts_lock.Unlock()
	if hosts[host] >= HOST_CONNECTIONS_MAXIMUM {
		return false
	}
	hosts[host]++
	return true
}

// transport_host_release returns a slot taken by transport_host_admit, and
// deletes the entry at zero so the map cannot grow with every address that has
// ever connected.
func transport_host_release(address string) {
	host := origin(address)
	hosts_lock.Lock()
	defer hosts_lock.Unlock()
	if hosts[host] <= 1 {
		delete(hosts, host)
		return
	}
	hosts[host]--
}

func transport_start(fatal chan<- error) error {
	tlsconf, err := certificate_tls()
	if err != nil {
		return fmt.Errorf("transport tls: %w", err)
	}
	tlsconf = http3.ConfigureTLSConfig(tlsconf) // adds the h3 ALPN (the webtransport server listens with the raw config)
	address := fmt.Sprintf("%s:%d", ini_string("transport", "listen", ""), ini_int("transport", "port", 4433))
	mux := http.NewServeMux()
	server := &webtransport.Server{
		H3: &http3.Server{Addr: address, TLSConfig: tlsconf, Handler: mux, EnableDatagrams: true,
			// quic-go defaults to a 30 s idle timeout with keepalives OFF, which drops a
			// client that goes quiet. Datagrams must be re-enabled here: providing a
			// config replaces the one http3 would otherwise build.
			QUICConfig: &quic.Config{
				MaxIdleTimeout:  60 * time.Second,
				KeepAlivePeriod: 15 * time.Second,
				EnableDatagrams: true,
				// Sized to what the protocol uses rather than quic-go's default
				// of 100 each. transport_serve accepts exactly one bidirectional
				// stream per session and reads no unidirectional ones beyond
				// HTTP/3's own, so the surplus was capacity a peer could fill
				// with data nothing would ever read: streams past the first are
				// never accepted, and their bytes sit in the receive buffers for
				// the life of the connection. Four covers the CONNECT request
				// stream and the session's control stream with headroom; eight
				// covers HTTP/3's control stream and the two QPACK streams.
				MaxIncomingStreams:    4,
				MaxIncomingUniStreams: 8,
			}},
		// Open server: players connect from any Mochi server's origin (and
		// from sandboxed iframes with a null origin) — the library's default
		// same-origin check would refuse them all.
		CheckOrigin: func(*http.Request) bool { return true },
	}
	webtransport.ConfigureHTTP3Server(server.H3) // advertises WebTransport in the HTTP/3 SETTINGS
	// Bind the UDP socket SYNCHRONOUSLY (#175): ListenAndServe hid the bind
	// behind a goroutine that only warn()ed, so a taken port left the process
	// alive but deaf. Serve() then blocks in the background, reporting its
	// terminal error to fatal.
	udp, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return fmt.Errorf("transport address %s: %w", address, err)
	}
	connection, err := net.ListenUDP("udp", udp)
	if err != nil {
		return fmt.Errorf("transport listen %s: %w", address, err)
	}
	mux.HandleFunc("/play", func(w http.ResponseWriter, r *http.Request) {
		// The data plane gets the lobby's per-host sliding-minute limiter: session
		// and player caps bound the steady state, not connection churn. The
		// capacity budgets are already spent by transport_accept, which admitted
		// this connection before HTTP/3 ever saw a request on it.
		if !lobby_permit(plays, r, 30) {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		session, err := server.Upgrade(w, r)
		if err != nil {
			debug("transport: upgrade: %v", err)
			return
		}
		go guard("transport connection", func() { session.CloseWithError(0, "fault") }, func() {
			transport_serve(session)
		})
	})
	// Own the accept loop rather than handing the socket to Serve: it accepts
	// every QUIC handshake and only then routes a request, so a peer that
	// completed the handshake and never asked for /play held a connection, its
	// buffers and its goroutines for the whole 60 s idle timeout, counted by
	// nothing. The budgets belong where the connection is born.
	// The two flags webtransport.Server.serve would have set for us, and which
	// its ServeQUICConn refuses a connection without: taking over the accept
	// loop means taking over the whole of what it did.
	quicconf := server.H3.QUICConfig.Clone()
	quicconf.EnableDatagrams = true
	quicconf.EnableStreamResetPartialDelivery = true
	quictransport := &quic.Transport{Conn: connection}
	listener, err := quictransport.ListenEarly(tlsconf, quicconf)
	if err != nil {
		return fmt.Errorf("transport listen %s: %w", address, err)
	}
	info("transport listening on %s (udp)", address)
	go func() { fatal <- fmt.Errorf("transport: %w", transport_accept(listener, server)) }()
	return nil
}

// transport_accept admits QUIC connections against the same global and
// per-host budgets the TCP listeners get from listener_limit, then hands each
// admitted one to HTTP/3. Returns only when the listener itself fails.
func transport_accept(listener quic_listener, server *webtransport.Server) error {
	for {
		connection, err := listener.Accept(context.Background())
		if err != nil {
			return err
		}
		address := connection.RemoteAddr().String()
		if !transport_hold(address) {
			connection.CloseWithError(0, "busy")
			continue
		}
		go guard("transport accept", func() { connection.CloseWithError(0, "fault") }, func() {
			defer transport_drop(address)
			// The webtransport server's own method, not http3's: it registers
			// the session manager that Upgrade later looks the connection up
			// in. Serves until the connection closes, which is after the
			// session running on it has ended, so the budgets are held for
			// exactly as long as the peer holds resources here.
			_ = server.ServeQUICConn(connection)
		})
	}
}

// quic_listener is what transport_accept needs of a QUIC listener, as an
// interface so a test can supply connections without a network.
type quic_listener interface {
	Accept(context.Context) (*quic.Conn, error)
}

// transport_hold reserves both connection budgets for one address, reporting
// false when either is spent. A refusal consumes nothing: the global slot taken
// by the reserve-then-check in transport_admit is returned before the per-host
// refusal is reported, or the ceiling would ratchet down one connection at a
// time. Concurrent holds, not just connect rate: without the per-host half one
// address could occupy every slot the global cap allows and hold them.
func transport_hold(address string) bool {
	if !transport_admit() {
		return false
	}
	if !transport_host_admit(address) {
		transport_release()
		return false
	}
	return true
}

// transport_drop returns what transport_hold reserved.
func transport_drop(address string) {
	transport_host_release(address)
	transport_release()
}

// transport_serve accepts the client's control stream then hands the
// connection to the session layer.
func transport_serve(session *webtransport.Session) {
	background, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	stream, err := session.AcceptStream(background)
	cancel()
	if err != nil {
		session.CloseWithError(0, "stream")
		return
	}
	l := &wire{
		session:  session,
		stream:   stream,
		inbound:  make(chan []byte, 256),
		outbound: make(chan []byte, 256),
		closed:   make(chan struct{}),
	}
	// Each reader owns its own guard: a panic on one of these goroutines is
	// invisible to the others and to the caller below.
	go guard("wire streams", func() { l.close("fault") }, l.streams)
	go guard("wire datagrams", func() { l.close("fault") }, l.datagrams)
	go guard("wire writer", func() { l.close("fault") }, l.writer)
	connection_serve(l)
}

// wire_stream and wire_session are the transport primitives the wire drives, as
// interfaces (satisfied by *webtransport.Stream / *webtransport.Session) so a
// test can inject a stream whose Write blocks.
type wire_stream interface {
	io.Reader
	Write([]byte) (int, error)
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
	CancelWrite(webtransport.StreamErrorCode)
	Close() error
}

type wire_session interface {
	SendDatagram([]byte) error
	ReceiveDatagram(context.Context) ([]byte, error)
	CloseWithError(webtransport.SessionErrorCode, string) error
}

// wire implements link over a WebTransport session.
type wire struct {
	session  wire_session
	stream   wire_stream
	inbound  chan []byte
	outbound chan []byte
	closed   chan struct{}
	once     sync.Once
	sending  sync.Mutex   // serialises stream writes
	reason   string       // set once by close before closed is closed
	oversize sync.Once    // bounds the too-large datagram warning to one per connection
	queued   atomic.Int64 // bytes sitting in inbound, bounded by queue_bytes
	rate     struct {
		sync.Mutex
		window time.Time
		count  int
	}
}

// queue_bytes bounds the payload one connection may have waiting for the
// consumer. The 256-message channel depth alone permits 256 x frame_most.
const queue_bytes = 1 << 20

// frames_minute bounds inbound control-stream messages per connection. A 60 Hz
// client sends ~3600/min of input plus chat and stores traffic, so this leaves
// generous headroom while still bounding the CBOR decode an attacker can buy.
const frames_minute = 12000

// admit applies both inbound budgets to a frame about to be queued and
// returns the close reason, or "" to accept it.
//
// Rate first: the per-kind limits (chat flood, jettison cooldown) all run AFTER
// the CBOR decode, and input and radar have none at all, so without a budget
// here one connection buys a core's worth of decoding. Then bytes rather than
// messages: the queue is 256 deep and a frame may be 64 KiB, so counting
// messages alone permits 16 MiB in flight per connection.
func (l *wire) admit(size int) string {
	if !l.allow() {
		return "rate"
	}
	if l.queued.Add(int64(size)) > queue_bytes {
		l.queued.Add(-int64(size))
		return "backlog"
	}
	return ""
}

// allow reports whether this connection may enqueue another frame, on a
// sliding one-minute window.
func (l *wire) allow() bool {
	l.rate.Lock()
	defer l.rate.Unlock()
	now := time.Now()
	if now.Sub(l.rate.window) >= time.Minute {
		l.rate.window = now
		l.rate.count = 0
	}
	l.rate.count++
	return l.rate.count <= frames_minute
}

// send_deadline bounds a single stream write: a peer that stops reading its
// stream must not block the writer (and thus the whole session teardown) on
// QUIC flow control forever. A healthy client acks far inside this.
const send_deadline = 5 * time.Second

const frame_most = 65536 // largest accepted message

// frame_deadline bounds the DELIVERY of a frame once it has begun; silence
// between frames is legitimate. QUIC's idle timeout watches connection packets,
// not stream progress, so two bytes would park the reader forever.
const frame_deadline = 10 * time.Second

// streams reads length-framed messages off the control stream.
func (l *wire) streams() {
	header := make([]byte, 4)
	for {
		// The first byte waits without a deadline: between frames the peer is
		// entitled to be quiet. Everything after it is mid-frame and bounded.
		if _, err := io.ReadFull(l.stream, header[:1]); err != nil {
			l.close("gone")
			return
		}
		_ = l.stream.SetReadDeadline(time.Now().Add(frame_deadline))
		if _, err := io.ReadFull(l.stream, header[1:]); err != nil {
			l.close("partial")
			return
		}
		length := binary.BigEndian.Uint32(header)
		if length == 0 || length > frame_most {
			l.close("frame")
			return
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(l.stream, payload); err != nil {
			l.close("partial")
			return
		}
		_ = l.stream.SetReadDeadline(time.Time{}) // between frames again
		if reason := l.admit(len(payload)); reason != "" {
			l.close(reason)
			return
		}
		select {
		case l.inbound <- payload:
		case <-l.closed:
			l.queued.Add(-int64(len(payload)))
			return
		}
	}
}

// datagrams reads unreliable messages.
func (l *wire) datagrams() {
	for {
		payload, err := l.session.ReceiveDatagram(context.Background())
		if err != nil {
			l.close("gone")
			return
		}
		if len(payload) > frame_most {
			continue
		}
		// Both budgets apply here too. Without the rate half, frames_minute
		// bounded only the control stream, while this path - the one carrying
		// 60 Hz input - bought a CBOR decode per packet for nothing: the
		// consumer is one goroutine per connection looping read() then decode().
		// Without the byte half, read() still released every datagram from
		// `queued`, so the counter drifted negative and the backlog cap stopped
		// firing for the control stream as well.
		if reason := l.admit(len(payload)); reason != "" {
			l.close(reason)
			return
		}
		select {
		case l.inbound <- payload:
		case <-l.closed:
			l.queued.Add(-int64(len(payload)))
			return
		default: // input flood: drop — newer samples supersede
			// Released here because read() never will: a dropped payload
			// reaches no consumer, and leaving it counted is the same
			// asymmetry in the other direction.
			l.queued.Add(-int64(len(payload)))
		}
	}
}

// send frames one message onto the control stream.
func (l *wire) send(payload []byte) error {
	l.sending.Lock()
	defer l.sending.Unlock()
	// A stalled write fails rather than blocking the writer forever. The two
	// SetReadDeadline siblings discard deliberately; match them explicitly so
	// this does not read as an oversight.
	_ = l.stream.SetWriteDeadline(time.Now().Add(send_deadline))
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(payload)))
	if _, err := l.stream.Write(header); err != nil {
		return err
	}
	_, err := l.stream.Write(payload)
	return err
}

// writer drains reliable writes onto the control stream. Only the writer
// ever finishes the stream, so every queued frame (welcome, refuse, end)
// reaches the peer before the FIN.
func (l *wire) writer() {
	graceful := false
	defer func() { l.teardown(graceful) }()
	for {
		select {
		case payload := <-l.outbound:
			if err := l.send(payload); err != nil {
				return // a failed write means the connection is bad: hard teardown
			}
		case <-l.closed:
			if l.reason == "slow" {
				return // irrecoverable: do NOT drain (send would re-block on the same flow control)
			}
			// Graceful reason: best-effort flush of what is already queued,
			// each send bounded by its write deadline so a peer that stopped
			// reading cannot strand us here.
			for {
				select {
				case payload := <-l.outbound:
					if err := l.send(payload); err != nil {
						return
					}
				default:
					graceful = true
					return
				}
			}
		}
	}
}

// teardown ends the transport once the writer exits - always, via the writer's
// defer. A graceful exit FINs the stream and gives QUIC a beat to deliver it; a
// hard exit cancels the write side immediately.
func (l *wire) teardown(graceful bool) {
	l.close("gone") // set a reason if none yet (send-error path); no-op if already closed
	if graceful {
		l.stream.Close()
		time.Sleep(200 * time.Millisecond) // let QUIC deliver the FIN + last bytes
	} else {
		l.stream.CancelWrite(0)
	}
	l.session.CloseWithError(0, l.reason)
}

func (l *wire) read() ([]byte, error) {
	select {
	case payload := <-l.inbound:
		l.queued.Add(-int64(len(payload))) // the budget is released as the consumer drains
		return payload, nil
	case <-l.closed:
		return nil, io.EOF
	}
}

func (l *wire) write(bytes []byte, reliable bool) {
	if reliable {
		select {
		case l.outbound <- bytes:
		case <-l.closed:
		default:
			// The reliable queue is full — the client cannot keep up. Silently
			// dropping a reliable message (welcome, roster, chat, end) would
			// break the delivery guarantee, so tear the slow connection down
			// instead; the reader's leave path then removes the player cleanly.
			l.close("slow")
		}
		return
	}
	// An unreliable send may legitimately fail (the session is going away), but
	// a payload above the peer's datagram ceiling fails EVERY time, so a busy
	// match dropped the recipient's pose updates entirely and silently: the
	// interest window reaches 31 remotes, which is ~1280 bytes of CBOR, and
	// before MTU discovery lifts it the usable datagram is ~1240. Fall back to
	// the reliable stream so the player still sees the world, and say so once.
	if err := l.session.SendDatagram(bytes); err != nil {
		var large *quic.DatagramTooLargeError
		if errors.As(err, &large) {
			l.oversize.Do(func() {
				warn("transport: datagram of %d bytes exceeds the peer's limit; falling back to the control stream", len(bytes))
			})
			l.write(bytes, true)
		}
	}
}

func (l *wire) close(reason string) {
	l.once.Do(func() {
		l.reason = reason
		close(l.closed) // readers stop; the writer flushes/aborts, then tears the session down
		if reason == "slow" {
			// Abort the write side NOW so a writer blocked in send() on QUIC flow
			// control unblocks at once instead of waiting out the write deadline.
			l.stream.CancelWrite(0)
		}
	})
}
