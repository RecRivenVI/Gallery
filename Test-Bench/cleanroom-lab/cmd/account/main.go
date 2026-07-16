// P18：账户与多客户端最小闭环(净室)。
// Personal(免登录 loopback+进程 token)/ LAN(Argon2id 登录 + Session Cookie + CSRF)/
// Viewer|Operator|Owner capability / API Token / 分享链接 / WS 会话失效。
// 内建 Web(HTML)与 CLI 两种客户端触达同一后端。启动后由外部脚本 curl 验证,GET /quit 退出。
package main

import (
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

type role int

const (
	viewer role = iota
	operator
	owner
)

func (r role) caps() map[string]bool {
	switch r {
	case owner:
		return map[string]bool{"library.read": true, "media.download": true, "scan.run": true, "rules.write": true, "clients.manage": true, "share.create": true}
	case operator:
		return map[string]bool{"library.read": true, "media.download": true, "scan.run": true, "share.create": true}
	default:
		return map[string]bool{"library.read": true}
	}
}

type user struct {
	name string
	salt []byte
	hash []byte
	role role
}

type session struct {
	user  string
	role  role
	csrf  string
	valid bool
}

type server struct {
	mu        sync.Mutex
	mode      string
	procToken string
	users     map[string]*user
	sessions  map[string]*session
	tokens    map[string]role // API token → role
	shares    map[string]string
}

func hashPw(pw string, salt []byte) []byte {
	return argon2.IDKey([]byte(pw), salt, 1, 64*1024, 4, 32)
}
func randHex(n int) string { b := make([]byte, n); rand.Read(b); return hex.EncodeToString(b) }

func (s *server) addUser(name, pw string, r role) {
	salt := make([]byte, 16)
	rand.Read(salt)
	s.users[name] = &user{name, salt, hashPw(pw, salt), r}
}

func main() {
	addr := flag.String("addr", "127.0.0.1:18096", "listen")
	mode := flag.String("mode", "lan", "personal|lan")
	flag.Parse()

	s := &server{
		mode: *mode, procToken: randHex(16),
		users: map[string]*user{}, sessions: map[string]*session{},
		tokens: map[string]role{}, shares: map[string]string{},
	}
	s.addUser("owner", "ownerpass", owner)
	s.addUser("viewer", "viewpass", viewer)
	s.tokens["tok-operator-123"] = operator
	fmt.Printf("mode=%s  procToken=%s\n", s.mode, s.procToken)

	quit := make(chan struct{})
	mux := http.NewServeMux()

	// 认证解析:Cookie session 或 Bearer token 或 Personal 直通
	authOf := func(r *http.Request) (role, *session, bool) {
		if s.mode == "personal" {
			// 仅 loopback + 进程 token
			if r.Header.Get("X-Proc-Token") == s.procToken {
				return owner, nil, true
			}
			return viewer, nil, true // Personal 下匿名读
		}
		if b := r.Header.Get("Authorization"); len(b) > 7 && b[:7] == "Bearer " {
			if role, ok := s.tokens[b[7:]]; ok {
				return role, nil, true
			}
		}
		if c, err := r.Cookie("sid"); err == nil {
			s.mu.Lock()
			sess := s.sessions[c.Value]
			if sess != nil && sess.valid {
				r := sess.role
				s.mu.Unlock()
				return r, sess, true
			}
			s.mu.Unlock()
		}
		return viewer, nil, false
	}

	require := func(cap string, h func(role, http.ResponseWriter, *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			role, sess, authed := authOf(r)
			if s.mode != "personal" && !authed {
				http.Error(w, `{"code":"unauthenticated"}`, 401)
				return
			}
			if !role.caps()[cap] {
				http.Error(w, `{"code":"forbidden","need":"`+cap+`"}`, 403)
				return
			}
			// CSRF:改写类要求 header 与 session csrf 匹配
			if r.Method != http.MethodGet && sess != nil {
				if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF")), []byte(sess.csrf)) != 1 {
					http.Error(w, `{"code":"csrf"}`, 403)
					return
				}
			}
			h(role, w, r)
		}
	}

	mux.HandleFunc("/api/v1/account/login", func(w http.ResponseWriter, r *http.Request) {
		name, pw := r.FormValue("user"), r.FormValue("pass")
		s.mu.Lock()
		u := s.users[name]
		s.mu.Unlock()
		if u == nil || subtle.ConstantTimeCompare(hashPw(pw, u.salt), u.hash) != 1 {
			http.Error(w, `{"code":"bad-credentials"}`, 401)
			return
		}
		sid, csrf := randHex(16), randHex(16)
		s.mu.Lock()
		s.sessions[sid] = &session{u.name, u.role, csrf, true}
		s.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: sid, HttpOnly: true, SameSite: http.SameSiteLaxMode, Path: "/"})
		json.NewEncoder(w).Encode(map[string]any{"user": u.name, "role": int(u.role), "csrf": csrf, "capabilities": u.role.caps()})
	})

	// bootstrap:前端据此拿 capability,不据角色名推断
	mux.HandleFunc("/api/v1/public/bootstrap", func(w http.ResponseWriter, r *http.Request) {
		role, _, _ := authOf(r)
		json.NewEncoder(w).Encode(map[string]any{"mode": s.mode, "capabilities": role.caps()})
	})

	mux.HandleFunc("/api/v1/public/works", require("library.read", func(role role, w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"items": []string{"work-1", "work-2"}, "role": int(role)})
	}))

	// WebSocket 使用与 HTTP 相同的 Session，并在会话吊销后主动以 4001 关闭。
	// 这是协议闭环原型：只实现握手、ready 事件、心跳和身份失效，不承载业务消息。
	mux.HandleFunc("/ws/v1", func(w http.ResponseWriter, r *http.Request) {
		_, sess, authed := authOf(r)
		if s.mode != "personal" && (!authed || sess == nil) {
			http.Error(w, `{"code":"unauthenticated"}`, http.StatusUnauthorized)
			return
		}
		key := r.Header.Get("Sec-WebSocket-Key")
		if r.Header.Get("Upgrade") != "websocket" || key == "" {
			http.Error(w, `{"code":"websocket-upgrade-required"}`, http.StatusUpgradeRequired)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, `{"code":"hijack-unavailable"}`, http.StatusInternalServerError)
			return
		}
		conn, rw, err := hijacker.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		accept := base64.StdEncoding.EncodeToString(sum[:])
		fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept)
		if err := rw.Flush(); err != nil {
			return
		}
		if err := writeWSFrame(conn, 0x1, []byte(`{"type":"ready","auth":"session"}`)); err != nil {
			return
		}

		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		deadline := time.NewTimer(15 * time.Second)
		defer deadline.Stop()
		for {
			select {
			case <-ticker.C:
				if sess != nil {
					s.mu.Lock()
					valid := sess.valid
					s.mu.Unlock()
					if !valid {
						writeWSClose(conn, 4001, "session_revoked")
						return
					}
				}
				if err := writeWSFrame(conn, 0x9, nil); err != nil {
					return
				}
			case <-deadline.C:
				writeWSClose(conn, 1000, "probe_complete")
				return
			}
		}
	})

	mux.HandleFunc("/api/v1/admin/scan", require("scan.run", func(role role, w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"jobId": randHex(6), "status": "queued"})
	}))

	mux.HandleFunc("/api/v1/account/share", require("share.create", func(role role, w http.ResponseWriter, r *http.Request) {
		code := randHex(4)
		s.mu.Lock()
		s.shares[code] = r.FormValue("work")
		s.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"code": code, "url": "/s/" + code})
	}))

	// 会话失效:owner 吊销某 session(演示 WS 身份失效同理——WS 每帧校验 session.valid)
	mux.HandleFunc("/api/v1/admin/revoke", require("clients.manage", func(role role, w http.ResponseWriter, r *http.Request) {
		sid := r.FormValue("sid")
		s.mu.Lock()
		if sess := s.sessions[sid]; sess != nil {
			sess.valid = false
		}
		s.mu.Unlock()
		w.Write([]byte(`{"revoked":true}`))
	}))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>Gallery account probe</title><body><main><h1>Gallery account probe</h1><pre id="out">loading</pre></main><script>fetch('/api/v1/public/bootstrap').then(r=>r.json()).then(v=>out.textContent=JSON.stringify(v,null,2))</script></body></html>`)
	})

	mux.HandleFunc("/quit", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("bye")); close(quit) })

	srv := &http.Server{Addr: *addr, Handler: mux}
	go func() { <-quit; srv.Close() }()
	fmt.Println("listening on", *addr)
	_ = time.Now
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Println(err)
	}
	fmt.Println("shutdown")
}

func writeWSFrame(conn net.Conn, opcode byte, payload []byte) error {
	if len(payload) > 125 {
		return fmt.Errorf("probe frame too large: %d", len(payload))
	}
	frame := []byte{0x80 | opcode, byte(len(payload))}
	frame = append(frame, payload...)
	_, err := conn.Write(frame)
	return err
}

func writeWSClose(conn net.Conn, code uint16, reason string) {
	payload := []byte{byte(code >> 8), byte(code)}
	payload = append(payload, []byte(reason)...)
	_ = writeWSFrame(conn, 0x8, payload)
}
