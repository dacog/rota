//go:build linux

package proxy

import (
	"net"

	"golang.org/x/sys/unix"
)

// trySplice attempts zero-copy transfer using Linux splice(2) syscall.
// Returns (true, bytes, err) if splice was used, (false, 0, nil) if caller should
// fall back to io.Copy (e.g. non-TCP connections).
func trySplice(dst, src net.Conn) (bool, int64, error) {
	// Both connections must be raw TCP to get file descriptors.
	srcTCP, ok := src.(*net.TCPConn)
	if !ok {
		return false, 0, nil
	}
	dstTCP, ok := dst.(*net.TCPConn)
	if !ok {
		return false, 0, nil
	}

	// Get raw file descriptors via SyscallConn (no dup, keeps non-blocking mode).
	srcRC, err := srcTCP.SyscallConn()
	if err != nil {
		return false, 0, nil
	}
	dstRC, err := dstTCP.SyscallConn()
	if err != nil {
		return false, 0, nil
	}

	// Create a kernel pipe as the intermediate buffer for splice.
	pipeFDs := make([]int, 2)
	if err := unix.Pipe2(pipeFDs, unix.O_NONBLOCK|unix.O_CLOEXEC); err != nil {
		return false, 0, nil
	}
	pipeR := pipeFDs[0]
	pipeW := pipeFDs[1]
	defer unix.Close(pipeR)
	defer unix.Close(pipeW)

	// Increase pipe buffer to 64KB for better throughput.
	// Use raw IoctlSetInt instead of Fcntl which may not be available on all archs.
	unix.IoctlSetInt(pipeR, unix.F_SETPIPE_SZ, 65536) //nolint:errcheck

	var spliceErr error
	var transferred int64

	// The outer Read call gives us the src fd.
	srcRC.Read(func(srcFD uintptr) bool {
		// The inner Write call gives us the dst fd.
		dstRC.Write(func(dstFD uintptr) bool {
			transferred, spliceErr = splicePump(int(srcFD), int(dstFD), pipeR, pipeW)
			return true
		})
		return true
	})

	if spliceErr != nil {
		return true, transferred, spliceErr
	}
	return true, transferred, nil
}

// splicePump moves data: src → pipeW → pipeR → dst using splice(2).
// Runs until src returns EOF (n==0) or an error occurs.
func splicePump(srcFD, dstFD, pipeR, pipeW int) (int64, error) {
	const spliceFlags = unix.SPLICE_F_MOVE | unix.SPLICE_F_NONBLOCK

	var total int64
	for {
		// Move data from src socket into the pipe write end.
		n, err := unix.Splice(srcFD, nil, pipeW, nil, 65536, spliceFlags)
		if err != nil {
			if err == unix.EAGAIN {
				if pollErr := pollFD(srcFD, false); pollErr != nil {
					return total, pollErr
				}
				continue
			}
			if n == 0 {
				return total, nil // EOF
			}
			return err
		}
		if n == 0 {
			return total, nil // EOF — src closed
		}

		// Drain the pipe into the dst socket.
		for written := int64(0); written < int64(n); {
			w, err := unix.Splice(pipeR, nil, dstFD, nil, int(int64(n)-written), spliceFlags)
			if err != nil {
				if err == unix.EAGAIN {
					if pollErr := pollFD(dstFD, true); pollErr != nil {
						return total, pollErr
					}
					continue
				}
				return total, err
			}
			written += int64(w)
		}
		total += int64(n)
	}
}

// pollFD waits for a file descriptor to become ready for reading or writing.
func pollFD(fd int, write bool) error {
	events := int16(unix.POLLIN)
	if write {
		events = unix.POLLOUT
	}
	fds := []unix.PollFd{{Fd: int32(fd), Events: events}}
	for {
		// AUD-30: use a finite poll timeout but renew it on expiry instead of
		// returning ETIMEDOUT. A fixed 60s idle kill tears down idle-but-healthy
		// CONNECT tunnels (websockets, long-poll); the io.Copy fallback imposes
		// no such limit, so we match it — we only return on a real error/hangup.
		// POLLERR/POLLHUP/POLLNVAL are reported regardless of Events, so a
		// closed/errored fd still wakes the poll and lets the caller's next
		// splice observe the error and exit.
		n, err := unix.Poll(fds, 60000)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			return total, err
		}
		if n == 0 {
			// Idle timeout: nothing ready yet. Keep waiting rather than killing
			// a healthy tunnel.
			continue
		}
		return nil
	}
}
