package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Access model:
//   - Password set: every API route except healthz, session, login and ingest
//     needs a session cookie (or one of the credentials below). Reads and
//     edits are both gated.
//   - Proxy secret set: the reverse proxy authenticates users and injects
//     X-Gammich-Proxy-Secret; reads stay open to anyone who can reach the
//     port, edits need the secret.
//   - Neither: read-only. Edits need the ingest token or, with TrustLoopback,
//     a loopback peer.
// A loopback peer is not trusted by default: a reverse proxy on the same host
// makes every visitor look like 127.0.0.1.

const (
	sessionCookie = "gammich_session"
	sessionTTL    = 30 * 24 * time.Hour

	loginMaxFailures = 5
	loginWindow      = 10 * time.Minute
)

// authMode is what /api/session reports to the UI.
func (s *Server) authMode() string {
	switch {
	case s.Password != "":
		return "password"
	case s.ProxySecret != "":
		return "proxy"
	default:
		return "none"
	}
}

// trustedCaller is true for credentials that are not a user session: the
// ingest token, the proxy secret and, when enabled, a loopback peer.
func (s *Server) trustedCaller(r *http.Request) bool {
	return s.checkIngestAuth(r) || s.checkProxySecret(r) ||
		(s.TrustLoopback && isLoopbackPeer(r.RemoteAddr))
}

func (s *Server) canRead(r *http.Request) bool {
	return s.Password == "" || s.trustedCaller(r) || s.validSession(r)
}

func (s *Server) canEdit(r *http.Request) bool {
	return s.trustedCaller(r) || (s.Password != "" && s.validSession(r))
}

func (s *Server) requireRead(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.canRead(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireMutateAuth gates mutating routes. Remote-User alone is never enough:
// without the proxy secret any client on the network could forge it.
func (s *Server) requireMutateAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.trustedCaller(r) {
			next.ServeHTTP(w, r)
			return
		}
		if s.Password != "" && s.validSession(r) {
			// SameSite=Lax still sends the cookie from a sibling subdomain
			// (same-site), so a cookie-authenticated edit must be same-origin.
			if !sameOrigin(r) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "cross-origin request"})
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if s.Password != "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
	})
}

// sameOrigin trusts Sec-Fetch-Site when the browser sends it, else compares
// the Origin header with the Host. A request with neither comes from a
// non-browser client, which a cookie-stealing page cannot drive.
func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin" || site == "none"
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

func (s *Server) checkProxySecret(r *http.Request) bool {
	if s.ProxySecret == "" {
		return false
	}
	got := r.Header.Get("X-Gammich-Proxy-Secret")
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.ProxySecret)) == 1
}

func (s *Server) checkIngestAuth(r *http.Request) bool {
	if s.IngestToken == "" {
		return false
	}
	got := r.Header.Get("X-Gammich-Token")
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.IngestToken)) == 1
}

func isLoopbackPeer(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// sessionKey derives the cookie signing key from the password, so changing
// the password logs every session out and a restart keeps them.
func (s *Server) sessionKey() []byte {
	sum := sha256.Sum256([]byte("gammich-session-v1\x00" + s.Password))
	return sum[:]
}

func (s *Server) signSession(exp time.Time) string {
	payload := "v1." + strconv.FormatInt(exp.Unix(), 10)
	mac := hmac.New(sha256.New, s.sessionKey())
	mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Server) validSession(r *http.Request) bool {
	if s.Password == "" {
		return false
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	i := strings.LastIndexByte(c.Value, '.')
	if i < 0 {
		return false
	}
	payload, sig := c.Value[:i], c.Value[i+1:]
	ver, expStr, ok := strings.Cut(payload, ".")
	if !ok || ver != "v1" {
		return false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || time.Now().Unix() >= exp {
		return false
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, s.sessionKey())
	mac.Write([]byte(payload))
	return hmac.Equal(got, mac.Sum(nil))
}

func secureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// loginLimiter counts login attempts per client IP over a sliding window.
// An attempt is counted before the password check and forgiven on success,
// so parallel requests cannot all pass a check made before any failure.
type loginLimiter struct {
	mu   sync.Mutex
	fail map[string][]time.Time
}

// reserve records an attempt and reports whether it may proceed.
func (l *loginLimiter) reserve(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.prune(ip, now)
	if len(kept) >= loginMaxFailures {
		return false
	}
	if l.fail == nil {
		l.fail = map[string][]time.Time{}
	}
	l.fail[ip] = append(kept, now)
	return true
}

// forgive removes the attempt made at now after a successful login.
func (l *loginLimiter) forgive(ip string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	list := l.fail[ip]
	for i := len(list) - 1; i >= 0; i-- {
		if list[i].Equal(now) {
			l.fail[ip] = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(l.fail[ip]) == 0 {
		delete(l.fail, ip)
	}
}

func (l *loginLimiter) prune(ip string, now time.Time) []time.Time {
	kept := l.fail[ip][:0]
	for _, t := range l.fail[ip] {
		if now.Sub(t) < loginWindow {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.fail, ip)
		return nil
	}
	l.fail[ip] = kept
	return kept
}

// clientIP is the peer address, or, when the peer is a trusted proxy, the
// right-most X-Forwarded-For entry that is not a trusted proxy. Without
// GAMMICH_TRUSTED_PROXIES every client behind a proxy shares its address.
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !s.trustedProxy(host) {
		return host
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop := strings.TrimSpace(hops[i])
		if net.ParseIP(hop) == nil {
			break
		}
		if !s.trustedProxy(hop) {
			return hop
		}
	}
	return host
}

func (s *Server) trustedProxy(ip string) bool {
	addr := net.ParseIP(ip)
	if addr == nil {
		return false
	}
	for _, n := range s.TrustedProxies {
		if n.Contains(addr) {
			return true
		}
	}
	return false
}

// ParseCIDRs reads a comma-separated list of CIDRs or bare IPs.
func ParseCIDRs(list string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, item := range strings.Split(list, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if !strings.Contains(item, "/") {
			if ip := net.ParseIP(item); ip != nil && ip.To4() != nil {
				item += "/32"
			} else {
				item += "/128"
			}
		}
		_, n, err := net.ParseCIDR(item)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if s.Password == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "password login is disabled"})
		return
	}
	ip, now := s.clientIP(r), time.Now()
	if !s.logins.reserve(ip, now) {
		w.Header().Set("Retry-After", strconv.Itoa(int(loginWindow.Seconds())))
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many attempts"})
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	want := sha256.Sum256([]byte(s.Password))
	got := sha256.Sum256([]byte(body.Password))
	if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
		s.Log.Warn("failed login", "ip", ip)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "wrong password"})
		return
	}
	s.logins.forgive(ip, now)
	exp := now.Add(sessionTTL)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    s.signSession(exp),
		Path:     "/",
		Expires:  exp,
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   secureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

type sessionDTO struct {
	Version       string      `json:"version"`
	Auth          string      `json:"auth"`
	Authenticated bool        `json:"authenticated"`
	CanRead       bool        `json:"can_read"`
	CanEdit       bool        `json:"can_edit"`
	User          string      `json:"user,omitempty"`
	Library       *libraryDTO `json:"library,omitempty"`
}

type libraryDTO struct {
	VideosPath string `json:"videos_path"`
	VideosOK   bool   `json:"videos_ok"`
	Clips      int64  `json:"clips"`
}

// session tells the UI what this caller may do, and whether the library is
// set up, so it can show a login page, hide edit controls or guide setup.
func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	out := sessionDTO{
		Version: s.Version,
		Auth:    s.authMode(),
		CanRead: s.canRead(r),
		CanEdit: s.canEdit(r),
	}
	if out.Version == "" {
		out.Version = "dev"
	}
	switch out.Auth {
	case "password":
		out.Authenticated = s.validSession(r)
	case "proxy":
		out.Authenticated = s.checkProxySecret(r)
		// Remote-* headers are forgeable by direct clients; only trust them
		// when the request provably came through the reverse proxy.
		if out.Authenticated {
			out.User = r.Header.Get("Remote-User")
		}
	}
	if out.CanRead {
		lib := &libraryDTO{VideosPath: s.VideoRoot}
		if fi, err := os.Stat(s.VideoRoot); err == nil && fi.IsDir() {
			lib.VideosOK = true
		}
		_ = s.DB.QueryRowContext(r.Context(),
			"SELECT COUNT(*) FROM clips WHERE deleted_at IS NULL").Scan(&lib.Clips)
		out.Library = lib
	}
	writeJSON(w, http.StatusOK, out)
}
