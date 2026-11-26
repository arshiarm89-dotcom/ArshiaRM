package main

// Museum server: visitors on phones and computers walk the museum together,
// see each other with names, and chat. It also serves the museum itself as a website.
// Created By ArshiaRM

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	version       = "1.1"
	maxPerRoom    = 40
	maxTotal      = 500
	maxPerIP      = 8
	maxNameRunes  = 20
	maxChatRunes  = 200
	maxRoomCode   = 12
	publicRoom    = "main"
	tickEvery     = 100 * time.Millisecond
	worldLimit    = 60.0
	colorCount    = 10
	chatBurst     = 5.0
	chatPerSecond = 0.7
)

type player struct {
	id      int
	name    string
	color   int
	room    *room
	ip      string
	c       *wsConn
	x, z, y float64
	dirty   bool
	tokens  float64
	lastTok time.Time
	posN    int
	posWin  time.Time
}

type room struct {
	code    string
	players map[int]*player
}

type hub struct {
	mu     sync.Mutex
	rooms  map[string]*room
	byIP   map[string]int
	total  int
	nextID int
}

var H = &hub{rooms: map[string]*room{}, byIP: map[string]int{}}

func mustJSON(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

func (r *room) broadcast(b []byte, except int) {
	for id, p := range r.players {
		if id != except {
			p.c.SendText(b)
		}
	}
}

func (p *player) info() map[string]interface{} {
	return map[string]interface{}{"id": p.id, "name": p.name, "color": p.color, "x": round2(p.x), "z": round2(p.z), "y": round2(p.y)}
}

/* ---------------- cleaning what people type ---------------- */

var persianDigits = strings.NewReplacer(
	"۰", "0", "۱", "1", "۲", "2", "۳", "3", "۴", "4", "۵", "5", "۶", "6", "۷", "7", "۸", "8", "۹", "9",
	"٠", "0", "١", "1", "٢", "2", "٣", "3", "٤", "4", "٥", "5", "٦", "6", "٧", "7", "٨", "8", "٩", "9")

func cleanText(s string, max int) string {
	s = strings.ToValidUTF8(s, "")
	var b strings.Builder
	n, space := 0, false
	for _, r := range s {
		if r == 0x200C || r == 0x200D { // zero-width joiners are part of Persian writing
			b.WriteRune(r)
			continue
		}
		if (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) || r == 0x200B || r == 0xFEFF {
			continue
		}
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if unicode.IsControl(r) {
			continue
		}
		if space && b.Len() > 0 {
			if n >= max {
				break
			}
			b.WriteRune(' ')
			n++
		}
		space = false
		if n >= max {
			break
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

func cleanRoom(s string) string {
	s = strings.ToUpper(persianDigits.Replace(s))
	var b strings.Builder
	for _, r := range s {
		if (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		}
		if b.Len() >= maxRoomCode {
			break
		}
	}
	if b.Len() == 0 {
		return publicRoom
	}
	return b.String()
}

/* ---------------- one visitor's connection ---------------- */

type inMsg struct {
	T    string   `json:"t"`
	Name string   `json:"name"`
	Room string   `json:"room"`
	Text string   `json:"text"`
	X    *float64 `json:"x"`
	Z    *float64 `json:"z"`
	Y    *float64 `json:"y"`
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func serveWS(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	H.mu.Lock()
	if H.byIP[ip] >= maxPerIP || H.total >= maxTotal {
		H.mu.Unlock()
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	H.byIP[ip]++
	H.total++
	H.mu.Unlock()

	c, err := upgrade(w, r)
	if err != nil {
		H.mu.Lock()
		H.byIP[ip]--
		if H.byIP[ip] <= 0 {
			delete(H.byIP, ip)
		}
		H.total--
		H.mu.Unlock()
		return
	}
	var me *player
	defer func() {
		c.Close()
		H.mu.Lock()
		if me != nil && me.room != nil {
			rm := me.room
			delete(rm.players, me.id)
			rm.broadcast(mustJSON(map[string]interface{}{"t": "leave", "id": me.id, "n": len(rm.players)}), -1)
			if len(rm.players) == 0 {
				delete(H.rooms, rm.code)
			}
			log.Printf("left: %q (room %s, %d there now)", me.name, roomLabel(rm.code), len(rm.players))
		}
		H.byIP[ip]--
		if H.byIP[ip] <= 0 {
			delete(H.byIP, ip)
		}
		H.total--
		H.mu.Unlock()
	}()

	for {
		b, err := c.ReadText()
		if err != nil {
			return
		}
		var m inMsg
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		switch m.T {
		case "hi":
			if me != nil {
				continue
			}
			p, ok := join(c, ip, m)
			if !ok {
				c.SendText(mustJSON(map[string]string{"t": "err", "code": "full"}))
				time.Sleep(200 * time.Millisecond)
				return
			}
			me = p
		case "p":
			if me == nil || m.X == nil || m.Z == nil || m.Y == nil {
				continue
			}
			x, z, y := *m.X, *m.Z, *m.Y
			if math.IsNaN(x+z+y) || math.IsInf(x+z+y, 0) {
				continue
			}
			H.mu.Lock()
			now := time.Now()
			if now.Sub(me.posWin) > time.Second {
				me.posWin, me.posN = now, 0
			}
			me.posN++
			if me.posN <= 30 {
				me.x = math.Max(-worldLimit, math.Min(worldLimit, x))
				me.z = math.Max(-worldLimit, math.Min(worldLimit, z))
				me.y = math.Mod(y, 2*math.Pi)
				me.dirty = true
			}
			H.mu.Unlock()
		case "chat":
			if me == nil {
				continue
			}
			text := cleanText(m.Text, maxChatRunes)
			if text == "" {
				continue
			}
			H.mu.Lock()
			now := time.Now()
			me.tokens = math.Min(chatBurst, me.tokens+now.Sub(me.lastTok).Seconds()*chatPerSecond)
			me.lastTok = now
			if me.tokens >= 1 {
				me.tokens--
				me.room.broadcast(mustJSON(map[string]interface{}{"t": "chat", "id": me.id, "name": me.name, "color": me.color, "text": text}), -1)
			} else {
				me.c.SendText(mustJSON(map[string]string{"t": "err", "code": "slow"}))
			}
			H.mu.Unlock()
		}
	}
}

func roomLabel(code string) string {
	if code == publicRoom {
		return "public"
	}
	return "private"
}

func join(c *wsConn, ip string, m inMsg) (*player, bool) {
	code := cleanRoom(m.Room)
	H.mu.Lock()
	defer H.mu.Unlock()
	rm := H.rooms[code]
	if rm == nil {
		rm = &room{code: code, players: map[int]*player{}}
		H.rooms[code] = rm
	}
	if len(rm.players) >= maxPerRoom {
		if len(rm.players) == 0 {
			delete(H.rooms, code)
		}
		return nil, false
	}
	H.nextID++
	name := cleanText(m.Name, maxNameRunes)
	if name == "" {
		name = "مهمان " + strconv.Itoa(H.nextID)
	}
	used := map[int]bool{}
	for _, o := range rm.players {
		used[o.color] = true
	}
	color := H.nextID % colorCount
	for i := 0; i < colorCount; i++ {
		if !used[(H.nextID+i)%colorCount] {
			color = (H.nextID + i) % colorCount
			break
		}
	}
	p := &player{id: H.nextID, name: name, color: color, room: rm, ip: ip, c: c, tokens: chatBurst, lastTok: time.Now(), x: 0, z: 14.5}
	if m.X != nil && m.Z != nil && m.Y != nil && !math.IsNaN(*m.X+*m.Z+*m.Y) && !math.IsInf(*m.X+*m.Z+*m.Y, 0) {
		p.x = math.Max(-worldLimit, math.Min(worldLimit, *m.X))
		p.z = math.Max(-worldLimit, math.Min(worldLimit, *m.Z))
		p.y = math.Mod(*m.Y, 2*math.Pi)
	}
	others := make([]map[string]interface{}, 0, len(rm.players))
	for _, o := range rm.players {
		others = append(others, o.info())
	}
	rm.players[p.id] = p
	shown := code
	if code == publicRoom {
		shown = ""
	}
	c.SendText(mustJSON(map[string]interface{}{"t": "welcome", "id": p.id, "name": p.name, "color": p.color, "room": shown, "players": others, "server": version}))
	rm.broadcast(mustJSON(map[string]interface{}{"t": "join", "p": p.info(), "n": len(rm.players)}), p.id)
	log.Printf("joined: %q (room %s, %d there now, %d online)", p.name, roomLabel(code), len(rm.players), H.total)
	return p, true
}

// Every 100 ms, everyone in a room gets the new positions of those who moved.
func ticker() {
	t := time.NewTicker(tickEvery)
	for range t.C {
		H.mu.Lock()
		for _, rm := range H.rooms {
			var list [][4]float64
			for _, p := range rm.players {
				if p.dirty {
					p.dirty = false
					list = append(list, [4]float64{float64(p.id), round2(p.x), round2(p.z), round2(p.y)})
				}
			}
			if len(list) > 0 {
				rm.broadcast(mustJSON(map[string]interface{}{"t": "ps", "l": list}), -1)
			}
		}
		H.mu.Unlock()
	}
}

/* ---------------- web pages ---------------- */

func statusJSON(w http.ResponseWriter, r *http.Request) {
	H.mu.Lock()
	n, rooms := 0, len(H.rooms)
	for _, rm := range H.rooms {
		n += len(rm.players)
	}
	H.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(mustJSON(map[string]interface{}{"ok": true, "online": n, "rooms": rooms, "version": version}))
}

func statusPage(w http.ResponseWriter, r *http.Request) {
	H.mu.Lock()
	n := 0
	for _, rm := range H.rooms {
		n += len(rm.players)
	}
	H.mu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, `<!DOCTYPE html><html lang="fa" dir="rtl"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>سرور موزه</title>
<body style="margin:0;min-height:100vh;display:grid;place-items:center;background:#14120E;color:#F2EAD8;font:18px/1.8 Tahoma,sans-serif">
<div style="padding:28px 32px;border:1px solid #4A4234;border-top:4px solid #C9A45C;border-radius:16px;background:#1b1813;text-align:center">
<h1 style="margin:0 0 8px;font-size:26px">سرور موزه روشنه ✅</h1><p style="margin:0">الان %d نفر آنلاینن.</p>
<p style="margin:12px 0 0"><a style="color:#C9A45C" href="/">ورود به موزه</a></p>
<p dir="ltr" style="margin:14px 0 0;color:#C9A45C;font-weight:700;font-size:14px">Created By ArshiaRM · server %s</p></div></body></html>`, n, version)
}

func siteHandler(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.Contains(p, "..") || (strings.HasSuffix(p, "/") && p != "/") {
			http.NotFound(w, r)
			return
		}
		if p == "/" || strings.HasSuffix(p, ".html") || strings.HasSuffix(p, ".json") {
			w.Header().Set("Cache-Control", "no-cache")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=86400")
		}
		fs.ServeHTTP(w, r)
	})
}

/* ---------------- start ---------------- */

func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

func setupLog(dir string) {
	path := filepath.Join(dir, "server.log")
	if st, err := os.Stat(path); err == nil && st.Size() > 5<<20 {
		_ = os.Rename(path, path+".old")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(os.Stdout, f))
}

func main() {
	dir := exeDir()
	port := flag.Int("port", 8085, "port to listen on")
	site := flag.String("site", filepath.Join(dir, "site"), "folder with the museum files")
	flag.Parse()
	setupLog(dir)

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", serveWS)
	mux.HandleFunc("/status", statusJSON)
	mux.HandleFunc("/server", statusPage)
	if st, err := os.Stat(filepath.Join(*site, "index.html")); err == nil && !st.IsDir() {
		mux.Handle("/", siteHandler(*site))
		log.Printf("museum website: serving %s", *site)
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			statusPage(w, r)
		})
		log.Printf("museum website: folder %s not found, only the online visit part runs", *site)
	}

	go ticker()
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", *port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	log.Printf("Museum server %s is running on port %d  (Created By ArshiaRM)", version, *port)
	log.Printf("check it in a browser: http://localhost:%d/server", *port)
	if err := srv.ListenAndServe(); err != nil {
		log.Printf("could not start: %v", err)
		fmt.Println("Press Enter to close.")
		_, _ = fmt.Scanln()
		os.Exit(1)
	}
}
