//go:build windows

// Museum for Windows: one .exe with the whole museum inside. It serves the museum on this
// computer only (127.0.0.1) and opens it in an app window of Microsoft Edge, so WebGL, sound
// and the online visit work like on the website.  Created By ArshiaRM
package main

import (
	"embed"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

//go:embed all:site
var siteFiles embed.FS

const (
	marker    = "ArshiaRM-museum-desktop"
	firstPort = 47321
	lastPort  = 47330
)

var (
	mu        sync.Mutex
	lastAlive time.Time
	byeAt     time.Time
)

func message(title, text string) {
	user32 := syscall.NewLazyDLL("user32.dll")
	box := user32.NewProc("MessageBoxW")
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(text)
	box.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0x40)
}

// already running? then just open another window to it
func existing(port int) bool {
	c := http.Client{Timeout: 700 * time.Millisecond}
	r, err := c.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/__museum")
	if err != nil {
		return false
	}
	defer r.Body.Close()
	b := make([]byte, 64)
	n, _ := r.Body.Read(b)
	return strings.TrimSpace(string(b[:n])) == marker
}

func browserPath() string {
	local := os.Getenv("LOCALAPPDATA")
	for _, p := range []string{
		filepath.Join(os.Getenv("ProgramFiles(x86)"), `Microsoft\Edge\Application\msedge.exe`),
		filepath.Join(os.Getenv("ProgramFiles"), `Microsoft\Edge\Application\msedge.exe`),
		filepath.Join(local, `Microsoft\Edge\Application\msedge.exe`),
		filepath.Join(os.Getenv("ProgramFiles"), `Google\Chrome\Application\chrome.exe`),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), `Google\Chrome\Application\chrome.exe`),
		filepath.Join(local, `Google\Chrome\Application\chrome.exe`),
	} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func open(url string) *exec.Cmd {
	if b := browserPath(); b != "" {
		data := filepath.Join(os.Getenv("LOCALAPPDATA"), "MuseumArshiaRM", "window")
		_ = os.MkdirAll(data, 0o755)
		cmd := exec.Command(b, "--app="+url, "--user-data-dir="+data, "--start-maximized",
			"--autoplay-policy=no-user-gesture-required", "--no-first-run", "--no-default-browser-check",
			"--disable-features=Translate,msEdgeSidebarV2", "--lang=fa")
		if cmd.Start() == nil {
			return cmd
		}
	}
	// no Edge or Chrome: use the default browser
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	return nil
}

func main() {
	site, err := fs.Sub(siteFiles, "site")
	if err != nil {
		message("موزه معاصر ایران", "فایل‌های موزه داخل برنامه پیدا نشد.")
		return
	}
	for p := firstPort; p <= lastPort; p++ {
		if existing(p) {
			open("http://127.0.0.1:" + strconv.Itoa(p) + "/")
			return
		}
	}
	var ln net.Listener
	port := 0
	for p := firstPort; p <= lastPort; p++ {
		if l, e := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p)); e == nil {
			ln, port = l, p
			break
		}
	}
	if ln == nil {
		message("موزه معاصر ایران", "موزه باز نشد: پورت‌های 47321 تا 47330 روی این کامپیوتر آزاد نیستند.")
		return
	}

	files := http.FileServer(http.FS(site))
	mux := http.NewServeMux()
	mux.HandleFunc("/__museum", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(marker)) })
	mux.HandleFunc("/__alive", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		lastAlive, byeAt = time.Now(), time.Time{}
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/__bye", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		byeAt = time.Now()
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.HasSuffix(p, "/") && p != "/" {
			http.NotFound(w, r)
			return
		}
		if p == "/" || strings.HasSuffix(p, ".html") || strings.HasSuffix(p, ".json") {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
	go func() { _ = http.Serve(ln, mux) }()

	url := "http://127.0.0.1:" + strconv.Itoa(port) + "/"
	started := time.Now()
	cmd := open(url)
	closed := make(chan struct{})
	if cmd != nil {
		go func() { _ = cmd.Wait(); close(closed) }()
	}

	// Stay open while the museum window is open; the page says "alive" every few seconds.
	for {
		select {
		case <-closed:
			closed = nil
			if time.Since(started) < 20*time.Second {
				continue // the browser handed the window to another process; rely on "alive"
			}
			mu.Lock()
			quiet := lastAlive.IsZero() || time.Since(lastAlive) > 20*time.Second
			mu.Unlock()
			if quiet {
				return
			}
		case <-time.After(2 * time.Second):
		}
		mu.Lock()
		la, bye := lastAlive, byeAt
		mu.Unlock()
		switch {
		case !bye.IsZero() && time.Since(bye) > 6*time.Second:
			stop(cmd)
			return
		case la.IsZero() && time.Since(started) > 3*time.Minute:
			return
		case !la.IsZero() && time.Since(la) > 150*time.Second:
			stop(cmd)
			return
		}
	}
}

func stop(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
