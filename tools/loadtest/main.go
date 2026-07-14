// Air world-server load harness (#81).
//
// Opens N real WebTransport clients against a single match, drives 60 Hz input
// on each, and measures the server's health from the snapshot stream each
// client receives: the tick-advance rate (must hold ~60/s in wall time — if the
// server can't step + interest-manage + SEND to N clients inside the 16.7 ms
// tick it falls behind and this drops) and the snapshot cadence/jitter. This
// exercises the real receive+send I/O the pure-CPU BenchmarkStep100 can't.
//
// Run (world1 must be up):
//   go run ./tools/loadtest -clients 100 -duration 20s
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/quic-go/webtransport-go"
)

var (
	lobby    = flag.String("lobby", "https://127.0.0.1:4433", "lobby base URL")
	clients  = flag.Int("clients", 100, "concurrent WebTransport clients")
	duration = flag.Duration("duration", 20*time.Second, "measurement window")
	warmup   = flag.Duration("warmup", 5*time.Second, "settle time before measuring")
)

type stat struct {
	firstTick, lastTick uint64
	firstAt, lastAt     time.Time
	snapshots           int
	gaps                []float64 // datagram inter-arrival, ms (during measurement)
	joined              bool
}

func main() {
	flag.Parse()
	insecure := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}

	// Create a fresh match sized for the clients (no bots: every aircraft is a
	// real connection, so both the physics AND the per-client send are loaded).
	body, _ := json.Marshal(map[string]any{
		"game": "air", "mode": "furball", "label": "loadtest",
		"capacity": *clients, "parameters": map[string]any{"missiles": true},
	})
	resp, err := insecure.Post(*lobby+"/sessions", "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Println("create session:", err)
		return
	}
	var created struct {
		Session string `json:"session"`
		Address string `json:"address"`
	}
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if created.Session == "" {
		fmt.Println("no session id in response")
		return
	}
	fmt.Printf("match %s at %s — connecting %d clients\n", created.Session, created.Address, *clients)

	measureFrom := time.Now().Add(*warmup)
	stopAt := measureFrom.Add(*duration)
	stats := make([]stat, *clients)
	var connected int64
	var wg sync.WaitGroup

	for c := 0; c < *clients; c++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			runClient(id, created.Session, created.Address, &stats[id], measureFrom, stopAt, &connected)
		}(c)
		time.Sleep(3 * time.Millisecond) // stagger joins so it isn't a thundering herd
	}
	wg.Wait()

	report(stats, *duration)
}

func runClient(id int, session, address string, st *stat, measureFrom, stopAt time.Time, connected *int64) {
	d := webtransport.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h3"}}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, sess, err := d.Dial(ctx, address, nil)
	if err != nil {
		return
	}
	defer sess.CloseWithError(0, "done")
	stream, err := sess.OpenStreamSync(context.Background())
	if err != nil {
		return
	}
	// framed join
	join, _ := cbor.Marshal(map[string]any{"kind": "join", "session": session, "name": fmt.Sprintf("load%03d", id), "protocol": 1})
	head := make([]byte, 4)
	binary.BigEndian.PutUint32(head, uint32(len(join)))
	stream.Write(head)
	stream.Write(join)
	// read framed welcome
	if _, err := io.ReadFull(stream, head); err != nil {
		return
	}
	length := binary.BigEndian.Uint32(head)
	welcome := make([]byte, length)
	if _, err := io.ReadFull(stream, welcome); err != nil {
		return
	}
	var first map[string]any
	if cbor.Unmarshal(welcome, &first) != nil || first["kind"] != "welcome" {
		return
	}
	st.joined = true
	atomic.AddInt64(connected, 1)

	// 60 Hz input sender: a gentle turn so the jets actually fly (worst case for
	// snapshot generation), and the server keeps the connection alive.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		var seq uint32
		t := time.NewTicker(time.Second / 60)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				seq++
				msg, _ := cbor.Marshal(map[string]any{"kind": "input", "inputs": []map[string]any{
					{"sequence": seq, "pitch": 0.15, "roll": 0.1, "throttle": 0.9},
				}})
				sess.SendDatagram(msg)
			}
		}
	}()

	// receive loop: read snapshot datagrams, track the server tick over wall time
	var lastAt time.Time
	for time.Now().Before(stopAt) {
		payload, err := sess.ReceiveDatagram(context.Background())
		if err != nil {
			return
		}
		now := time.Now()
		var m map[string]any
		if cbor.Unmarshal(payload, &m) != nil {
			continue
		}
		kind, _ := m["kind"].(string)
		if kind != "snapshot" && kind != "poses" {
			continue
		}
		tick := toUint(m["tick"])
		if now.Before(measureFrom) {
			lastAt = now
			continue
		}
		if st.firstTick == 0 {
			st.firstTick, st.firstAt = tick, now
		}
		if tick > st.lastTick {
			st.lastTick, st.lastAt = tick, now
		}
		st.snapshots++
		if !lastAt.IsZero() {
			st.gaps = append(st.gaps, float64(now.Sub(lastAt).Microseconds())/1000)
		}
		lastAt = now
	}
}

func toUint(v any) uint64 {
	switch n := v.(type) {
	case uint64:
		return n
	case int64:
		return uint64(n)
	case uint:
		return uint64(n)
	case int:
		return uint64(n)
	}
	return 0
}

func report(stats []stat, window time.Duration) {
	joined, tickRates, snapRates, jitters := 0, []float64{}, []float64{}, []float64{}
	for _, s := range stats {
		if !s.joined {
			continue
		}
		joined++
		if s.lastTick > s.firstTick && s.lastAt.After(s.firstAt) {
			secs := s.lastAt.Sub(s.firstAt).Seconds()
			tickRates = append(tickRates, float64(s.lastTick-s.firstTick)/secs)
			snapRates = append(snapRates, float64(s.snapshots)/secs)
		}
		if len(s.gaps) > 4 {
			jitters = append(jitters, p(s.gaps, 0.99))
		}
	}
	fmt.Printf("\nconnected %d/%d clients\n", joined, len(stats))
	if len(tickRates) == 0 {
		fmt.Println("no snapshots measured — check the server is up and the match started")
		return
	}
	fmt.Printf("server tick rate (target 60/s):  min %.1f  mean %.1f  (a drop below 60 = the server can't hold real time under this load)\n", min(tickRates), mean(tickRates))
	fmt.Printf("snapshot rate per client (~20/s): min %.1f  mean %.1f\n", min(snapRates), mean(snapRates))
	fmt.Printf("datagram inter-arrival p99:       mean %.1f ms  worst %.1f ms  (steady ~25 ms = 2 datagrams/snapshot at 20 Hz)\n", mean(jitters), max(jitters))
}

func p(a []float64, q float64) float64 {
	s := append([]float64(nil), a...)
	sort.Float64s(s)
	i := int(q * float64(len(s)))
	if i >= len(s) {
		i = len(s) - 1
	}
	return s[i]
}
func mean(a []float64) float64 {
	if len(a) == 0 {
		return 0
	}
	t := 0.0
	for _, x := range a {
		t += x
	}
	return t / float64(len(a))
}
func min(a []float64) float64 {
	m := a[0]
	for _, x := range a {
		if x < m {
			m = x
		}
	}
	return m
}
func max(a []float64) float64 {
	m := 0.0
	for _, x := range a {
		if x > m {
			m = x
		}
	}
	return m
}
