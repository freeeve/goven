// Command benchserver is an out-of-process, in-memory maven2 repository used
// by goven's transfer benchmarks, so client-side timings and allocation
// counts exclude server-side work. It prints "LISTEN <addr>" once ready.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// store is the in-memory file map shared by the handler. A non-zero latency
// is slept before every response, simulating the round-trip time of a remote
// repository so concurrency effects can be measured realistically.
type store struct {
	mu      sync.RWMutex
	files   map[string][]byte
	latency time.Duration
}

// ServeHTTP implements PUT (store) and GET/HEAD (serve) over the map.
func (s *store) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.latency > 0 {
		time.Sleep(s.latency)
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if r.Method == http.MethodPut {
		var buf bytes.Buffer
		if r.ContentLength > 0 {
			buf.Grow(int(r.ContentLength))
		}
		if _, err := io.Copy(&buf, r.Body); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		s.mu.Lock()
		s.files[path] = buf.Bytes()
		s.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		return
	}
	s.mu.RLock()
	data, ok := s.files[path]
	s.mu.RUnlock()
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	if r.Method != http.MethodHead {
		w.Write(data)
	}
}

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "listen address (port 0 picks a free port)")
	latency := flag.Duration("latency", 0, "sleep before each response to simulate network RTT")
	flag.Parse()
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("LISTEN %s\n", ln.Addr())
	log.Fatal(http.Serve(ln, &store{files: map[string][]byte{}, latency: *latency}))
}
