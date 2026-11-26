package main

// A small WebSocket (RFC 6455) server side, written with the standard library only,
// so the server is one file with nothing else to install.  Created By ArshiaRM

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	wsGUID     = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	maxMessage = 16 * 1024
	readWait   = 75 * time.Second
	pingEvery  = 25 * time.Second
	writeWait  = 10 * time.Second
	sendQueue  = 256
)

type wsConn struct {
	nc        net.Conn
	br        *bufio.Reader
	out       chan []byte
	done      chan struct{}
	closeOnce sync.Once
}

func headerHas(v, token string) bool {
	for _, p := range strings.Split(v, ",") {
		if strings.EqualFold(strings.TrimSpace(p), token) {
			return true
		}
	}
	return false
}

func upgrade(w http.ResponseWriter, r *http.Request) (*wsConn, error) {
	if r.Method != http.MethodGet || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || !headerHas(r.Header.Get("Connection"), "upgrade") {
		http.Error(w, "this address is for the museum app (WebSocket)", http.StatusBadRequest)
		return nil, errors.New("not a websocket request")
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		w.Header().Set("Sec-WebSocket-Version", "13")
		http.Error(w, "unsupported websocket version", http.StatusUpgradeRequired)
		return nil, errors.New("bad version")
	}
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	if key == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return nil, errors.New("missing key")
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "cannot upgrade", http.StatusInternalServerError)
		return nil, errors.New("no hijacker")
	}
	nc, brw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}
	sum := sha1.Sum([]byte(key + wsGUID))
	resp := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " +
		base64.StdEncoding.EncodeToString(sum[:]) + "\r\n\r\n"
	_ = nc.SetWriteDeadline(time.Now().Add(writeWait))
	if _, err := nc.Write([]byte(resp)); err != nil {
		nc.Close()
		return nil, err
	}
	c := &wsConn{nc: nc, br: brw.Reader, out: make(chan []byte, sendQueue), done: make(chan struct{})}
	go c.writer()
	return c, nil
}

func frame(op byte, payload []byte) []byte {
	n := len(payload)
	var h []byte
	switch {
	case n < 126:
		h = []byte{0x80 | op, byte(n)}
	case n < 65536:
		h = []byte{0x80 | op, 126, 0, 0}
		binary.BigEndian.PutUint16(h[2:], uint16(n))
	default:
		h = make([]byte, 10)
		h[0], h[1] = 0x80|op, 127
		binary.BigEndian.PutUint64(h[2:], uint64(n))
	}
	return append(h, payload...)
}

func (c *wsConn) writer() {
	t := time.NewTicker(pingEvery)
	defer func() { t.Stop(); c.Close() }()
	for {
		select {
		case b := <-c.out:
			_ = c.nc.SetWriteDeadline(time.Now().Add(writeWait))
			if _, err := c.nc.Write(b); err != nil {
				return
			}
		case <-t.C:
			_ = c.nc.SetWriteDeadline(time.Now().Add(writeWait))
			if _, err := c.nc.Write(frame(0x9, nil)); err != nil {
				return
			}
		case <-c.done:
			return
		}
	}
}

// SendText queues a text message. A client that cannot keep up is disconnected.
func (c *wsConn) SendText(b []byte) {
	select {
	case <-c.done:
	case c.out <- frame(0x1, b):
	default:
		c.Close()
	}
}

func (c *wsConn) Close() {
	c.closeOnce.Do(func() {
		close(c.done)
		c.nc.Close()
	})
}

// ReadText returns the next text message; pings are answered and pongs skipped.
func (c *wsConn) ReadText() ([]byte, error) {
	var msg []byte
	started := false
	for {
		_ = c.nc.SetReadDeadline(time.Now().Add(readWait))
		var h [2]byte
		if _, err := io.ReadFull(c.br, h[:]); err != nil {
			return nil, err
		}
		fin, op, masked := h[0]&0x80 != 0, h[0]&0x0f, h[1]&0x80 != 0
		if h[0]&0x70 != 0 || !masked {
			return nil, errors.New("protocol error")
		}
		n := uint64(h[1] & 0x7f)
		if n == 126 {
			var b [2]byte
			if _, err := io.ReadFull(c.br, b[:]); err != nil {
				return nil, err
			}
			n = uint64(binary.BigEndian.Uint16(b[:]))
		} else if n == 127 {
			var b [8]byte
			if _, err := io.ReadFull(c.br, b[:]); err != nil {
				return nil, err
			}
			n = binary.BigEndian.Uint64(b[:])
		}
		if n > maxMessage || uint64(len(msg))+n > maxMessage {
			return nil, errors.New("message too big")
		}
		var mask [4]byte
		if _, err := io.ReadFull(c.br, mask[:]); err != nil {
			return nil, err
		}
		p := make([]byte, n)
		if _, err := io.ReadFull(c.br, p); err != nil {
			return nil, err
		}
		for i := range p {
			p[i] ^= mask[i&3]
		}
		if op >= 0x8 {
			if !fin || n > 125 {
				return nil, errors.New("bad control frame")
			}
			switch op {
			case 0x8:
				return nil, io.EOF
			case 0x9:
				select {
				case c.out <- frame(0xA, p):
				default:
				}
			}
			continue
		}
		switch op {
		case 0x0:
			if !started {
				return nil, errors.New("unexpected continuation")
			}
			msg = append(msg, p...)
		case 0x1, 0x2:
			if started {
				return nil, errors.New("unexpected new message")
			}
			started, msg = true, p
		default:
			return nil, errors.New("unknown opcode")
		}
		if fin {
			return msg, nil
		}
	}
}
