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
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
)

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
			// Without an explicit config, quic-go defaults to a 30 s idle
			// timeout with keepalives OFF — a client that goes quiet (asset
			// load, backgrounded tab, GC pause) was silently dropped. The
			// server pings every 15 s so a quiet-but-alive connection never
			// idles out; a genuinely dead one still reaps in 60 s. Datagrams
			// must be re-enabled here: providing a config replaces the one
			// http3 would otherwise build.
			QUICConfig: &quic.Config{
				MaxIdleTimeout:  60 * time.Second,
				KeepAlivePeriod: 15 * time.Second,
				EnableDatagrams: true,
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
		// The data plane gets the same per-host sliding-minute limiter the
		// lobby endpoints have — session and player caps bound the steady
		// state, but connection CHURN from one address was unthrottled.
		if !lobby_permit(plays, r, 30) {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		session, err := server.Upgrade(w, r)
		if err != nil {
			debug("transport: upgrade: %v", err)
			return
		}
		go transport_serve(session)
	})
	info("transport listening on %s (udp)", address)
	go func() { fatal <- fmt.Errorf("transport: %w", server.Serve(connection)) }()
	return nil
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
	go l.streams()
	go l.datagrams()
	go l.writer()
	connection_serve(l)
}

// wire implements link over a WebTransport session.
type wire struct {
	session  *webtransport.Session
	stream   *webtransport.Stream
	inbound  chan []byte
	outbound chan []byte
	closed   chan struct{}
	once     sync.Once
	sending  sync.Mutex // serialises stream writes
	reason   string     // set once by close before closed is closed
}

const frame_most = 65536 // largest accepted message

// streams reads length-framed messages off the control stream.
func (l *wire) streams() {
	header := make([]byte, 4)
	for {
		if _, err := io.ReadFull(l.stream, header); err != nil {
			l.close("gone")
			return
		}
		length := binary.BigEndian.Uint32(header)
		if length == 0 || length > frame_most {
			l.close("frame")
			return
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(l.stream, payload); err != nil {
			l.close("gone")
			return
		}
		select {
		case l.inbound <- payload:
		case <-l.closed:
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
		select {
		case l.inbound <- payload:
		case <-l.closed:
			return
		default: // input flood: drop — newer samples supersede
		}
	}
}

// send frames one message onto the control stream.
func (l *wire) send(payload []byte) error {
	l.sending.Lock()
	defer l.sending.Unlock()
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
	for {
		select {
		case payload := <-l.outbound:
			if err := l.send(payload); err != nil {
				l.close("gone")
				return
			}
		case <-l.closed:
			for {
				select {
				case payload := <-l.outbound:
					l.send(payload)
				default:
					l.stream.Close()
					time.Sleep(200 * time.Millisecond) // let QUIC deliver before teardown
					l.session.CloseWithError(0, l.reason)
					return
				}
			}
		}
	}
}

func (l *wire) read() ([]byte, error) {
	select {
	case payload := <-l.inbound:
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
	l.session.SendDatagram(bytes)
}

func (l *wire) close(reason string) {
	l.once.Do(func() {
		l.reason = reason
		close(l.closed) // readers stop; the writer drains the queue, FINs the stream, then tears the session down
	})
}
