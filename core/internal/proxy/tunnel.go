package proxy

import (
	"io"
	"net"
	"sync"
)

// TunnelStats records payload bytes flowing through an HTTPS CONNECT tunnel.
// BytesUp is client -> upstream and BytesDown is upstream -> client.
type TunnelStats struct {
	BytesUp   int64
	BytesDown int64
}

// BidirectionalCopy copies data between client and upstream in both directions
// concurrently and returns byte counters for project/provider accounting.
func BidirectionalCopy(client, upstream net.Conn) (TunnelStats, error) {
	var wg sync.WaitGroup
	var clientErr, upstreamErr error
	var stats TunnelStats

	wg.Add(2)

	// upstream -> client
	go func() {
		defer wg.Done()
		stats.BytesDown, clientErr = copyOneDirection(client, upstream)
		if tc, ok := client.(*net.TCPConn); ok {
			tc.CloseWrite() //nolint:errcheck
		}
	}()

	// client -> upstream
	go func() {
		defer wg.Done()
		stats.BytesUp, upstreamErr = copyOneDirection(upstream, client)
		if tc, ok := upstream.(*net.TCPConn); ok {
			tc.CloseWrite() //nolint:errcheck
		}
	}()

	wg.Wait()

	if clientErr != nil {
		return stats, clientErr
	}
	return stats, upstreamErr
}

// copyOneDirection copies from src to dst using the most efficient method
// available and returns the number of payload bytes transferred.
func copyOneDirection(dst, src net.Conn) (int64, error) {
	ok, n, err := trySplice(dst, src)
	if ok {
		return n, err
	}

	buf := bufPool.Get().([]byte)
	defer bufPool.Put(buf)
	return io.CopyBuffer(dst, src, buf)
}

// bufPool reuses 32KB buffers for io.CopyBuffer to reduce GC pressure.
var bufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 32*1024)
		return buf
	},
}
