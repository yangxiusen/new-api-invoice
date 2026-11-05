package server

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
)

// commitProxy loses COMMIT's response only after PostgreSQL's ReadyForQuery.
// Unlike a mock Commit error, this proves recovery after a durable commit.
func commitProxy(t *testing.T, target string) (string, *atomic.Bool) {
	t.Helper()
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	armed := &atomic.Bool{}
	var wg sync.WaitGroup
	var mu sync.Mutex
	connections := []net.Conn{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			client, e := listener.Accept()
			if e != nil {
				return
			}
			upstream, e := net.Dial("tcp", target)
			if e != nil {
				client.Close()
				continue
			}
			mu.Lock()
			connections = append(connections, client, upstream)
			mu.Unlock()
			drop := &atomic.Bool{}
			wg.Add(2)
			go func() {
				defer wg.Done()
				defer client.Close()
				defer upstream.Close()
				buf := make([]byte, 65536)
				for {
					n, e := client.Read(buf)
					if n > 0 {
						if armed.Load() && bytes.Contains(bytes.ToUpper(buf[:n]), []byte("COMMIT\x00")) {
							drop.Store(true)
						}
						if _, err := upstream.Write(buf[:n]); err != nil {
							return
						}
					}
					if e != nil {
						return
					}
				}
			}()
			go func() {
				defer wg.Done()
				defer client.Close()
				defer upstream.Close()
				for {
					header := make([]byte, 5)
					if _, e := io.ReadFull(upstream, header); e != nil {
						return
					}
					size := int(binary.BigEndian.Uint32(header[1:]))
					if size < 4 || size > 2*1024*1024 {
						return
					}
					payload := make([]byte, size-4)
					if _, e := io.ReadFull(upstream, payload); e != nil {
						return
					}
					if drop.Load() {
						if header[0] == 'Z' {
							return
						}
						continue
					}
					if _, e = client.Write(append(header, payload...)); e != nil {
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		mu.Lock()
		for _, c := range connections {
			c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return listener.Addr().String(), armed
}
