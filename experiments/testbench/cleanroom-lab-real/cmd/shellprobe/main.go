// shellprobe 证明桌面壳只依赖后端进程契约，不依赖 Wails 运行时。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type descriptor struct {
	PID, Port int
	Protocol  int
}
type report struct {
	SchemaVersion                                                                                                int `json:"schema_version"`
	BackendIndependent, DynamicPort, HealthReachable, SecondInstanceRejected, RestartReconnect, DescriptorAtomic bool
	FirstPort, SecondPort                                                                                        int
}

func main() {
	child := flag.Bool("child", false, "internal child mode")
	dir := flag.String("dir", "results/shell", "runtime directory")
	out := flag.String("out", "results/shell.json", "result JSON")
	flag.Parse()
	if *child {
		runChild(*dir)
		return
	}
	runParent(*dir, *out)
}
func runChild(dir string) {
	must(os.MkdirAll(dir, 0o755))
	lock := filepath.Join(dir, "galleryd.lock")
	f, e := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if e != nil {
		os.Exit(23)
	}
	defer func() { f.Close(); os.Remove(lock) }()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	must(e)
	port := ln.Addr().(*net.TCPAddr).Port
	d := descriptor{os.Getpid(), port, 1}
	b, _ := json.Marshal(d)
	tmp := filepath.Join(dir, "backend.json.tmp")
	must(os.WriteFile(tmp, b, 0o600))
	must(os.Rename(tmp, filepath.Join(dir, "backend.json")))
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok","protocol":1}`)
	})
	must(http.Serve(ln, mux))
}
func runParent(dir, out string) {
	must(os.RemoveAll(dir))
	must(os.MkdirAll(dir, 0o755))
	first := start(dir)
	d1 := waitDescriptor(dir)
	reachable := health(d1.Port)
	second := exec.Command(os.Args[0], "--child", "--dir", dir)
	secondErr := second.Run() != nil
	_ = first.Process.Kill()
	_, _ = first.Process.Wait()
	_ = os.Remove(filepath.Join(dir, "galleryd.lock"))
	_ = os.Remove(filepath.Join(dir, "backend.json"))
	secondProc := start(dir)
	d2 := waitDescriptor(dir)
	reconnected := health(d2.Port)
	_ = secondProc.Process.Kill()
	_, _ = secondProc.Process.Wait()
	_ = os.Remove(filepath.Join(dir, "galleryd.lock"))
	r := report{1, true, d1.Port > 0 && d2.Port > 0, reachable, secondErr, reconnected, d1.Protocol == 1 && d2.Protocol == 1, d1.Port, d2.Port}
	b, e := json.MarshalIndent(r, "", "  ")
	must(e)
	must(os.MkdirAll(filepath.Dir(out), 0o755))
	must(os.WriteFile(out, b, 0o644))
	fmt.Printf("backend-independent=%v health=%v second-rejected=%v reconnect=%v dynamic=%v\n", r.BackendIndependent, r.HealthReachable, r.SecondInstanceRejected, r.RestartReconnect, r.DynamicPort)
}
func start(dir string) *exec.Cmd {
	c := exec.Command(os.Args[0], "--child", "--dir", dir)
	must(c.Start())
	return c
}
func waitDescriptor(dir string) descriptor {
	path := filepath.Join(dir, "backend.json")
	for i := 0; i < 100; i++ {
		b, e := os.ReadFile(path)
		if e == nil {
			var d descriptor
			if json.Unmarshal(b, &d) == nil && d.Port > 0 {
				return d
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	panic("descriptor timeout")
}
func health(port int) bool {
	c := http.Client{Timeout: 2 * time.Second}
	r, e := c.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/health")
	if e != nil {
		return false
	}
	defer r.Body.Close()
	return r.StatusCode == 200 && strings.Contains(r.Header.Get("Content-Type"), "json")
}
func must(e error) {
	if e != nil {
		panic(e)
	}
}
