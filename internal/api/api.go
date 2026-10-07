package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"herdr-space/internal/auth"
	githubProvider "herdr-space/internal/github"
	"herdr-space/internal/model"
	"herdr-space/internal/store"
)

type Config struct {
	DataDir, Origin, HerdrBinary, HerdrConfigDir       string
	AllowInsecureLocal                                 bool
	ProjectRoots                                       []string
	GitHub                                             githubProvider.Provider
	BootstrapIssuer, BootstrapAudience, BootstrapEmail string
	// BootstrapHTTPClient is for controlled integration tests; the CLI never sets it.
	BootstrapHTTPClient *http.Client
	// BodyReadTimeout bounds request-body reads. Zero uses the 10-second default.
	BodyReadTimeout time.Duration
}
type streamRegistration struct {
	cancel          context.CancelFunc
	authInvalidated func()
}
type Server struct {
	C                 Config
	Store             *store.Store
	Auth              *auth.Auth
	Provider          model.Provider
	github            githubProvider.Provider
	gitDiscoverySlots chan struct{}
	assets            fs.FS
	mux               *http.ServeMux
	mu                sync.Mutex
	shuttingDown      bool
	streams           map[string]map[string]streamRegistration
	attempts          map[string][]time.Time
	bootstrapVerify   func(context.Context, *http.Request) (auth.AccessIdentity, error)
	pending           map[[32]byte]setupPending
}

func New(c Config, assets fs.FS, p model.Provider) (*Server, error) {
	if c.DataDir == "" {
		return nil, store.ErrInvalid
	}
	u, e := url.Parse(c.Origin)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return nil, store.ErrInvalid
	}
	if c.AllowInsecureLocal {
		if u.Scheme != "http" || !loopback(u.Hostname()) {
			return nil, store.ErrInvalid
		}
	} else if u.Scheme != "https" {
		return nil, store.ErrInvalid
	}
	st, e := store.Open(path.Join(c.DataDir, "space.db"))
	if e != nil {
		return nil, e
	}
	a, e := auth.New(st, path.Join(c.DataDir, "auth.key"))
	if e != nil {
		st.Close()
		return nil, e
	}
	if e := a.VerifyBackupKey(context.Background()); e != nil {
		st.Close()
		return nil, e
	}
	gh := c.GitHub
	if gh == nil {
		gh = githubProvider.NewCLI()
	}
	s := &Server{C: c, Store: st, Auth: a, Provider: p, github: gh, gitDiscoverySlots: make(chan struct{}, gitDiscoveryWorkers), assets: assets, mux: http.NewServeMux(), streams: map[string]map[string]streamRegistration{}, attempts: map[string][]time.Time{}, pending: map[[32]byte]setupPending{}}
	if c.BootstrapIssuer != "" || c.BootstrapAudience != "" || c.BootstrapEmail != "" {
		verifier, err := auth.NewAccessVerifierWithClient(c.BootstrapIssuer, c.BootstrapAudience, c.BootstrapEmail, c.BootstrapHTTPClient)
		if err != nil {
			st.Close()
			return nil, store.ErrInvalid
		}
		s.bootstrapVerify = verifier.VerifyRequest
	}
	s.mux.HandleFunc("/api/v1/", s.api)
	s.mux.HandleFunc("/api/v1", func(w http.ResponseWriter, r *http.Request) { failure(w, 404) })
	s.mux.HandleFunc("/", s.static)
	return s, nil
}
func loopback(h string) bool {
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback() || h == "localhost"
}
func (s *Server) BeginShutdown() {
	s.mu.Lock()
	s.shuttingDown = true
	for _, group := range s.streams {
		for _, stream := range group {
			stream.cancel()
		}
	}
	s.mu.Unlock()
}
func (s *Server) Close() error {
	s.BeginShutdown()
	s.mu.Lock()
	s.pending = map[[32]byte]setupPending{}
	s.mu.Unlock()
	return s.Store.Close()
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Body != nil && r.Body != http.NoBody {
		deadline := s.C.BodyReadTimeout
		if deadline <= 0 {
			deadline = 10 * time.Second
		}
		// Keep the deadline through net/http's post-handler body drain. The
		// server resets it when the connection returns to keep-alive idle.
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(deadline))
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self' wss: ws:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
	s.mux.ServeHTTP(w, r)
}
func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		http.NotFound(w, r)
		return
	}
	if s.assets == nil {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" || name == "." {
		name = "index.html"
	}
	if name == "sw.js" {
		w.Header().Set("Cache-Control", "no-store, no-cache, max-age=0, must-revalidate")
		w.Header().Set("Service-Worker-Allowed", "/")
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	}
	b, e := fs.ReadFile(s.assets, name)
	if e != nil {
		if strings.Contains(path.Base(name), ".") {
			http.NotFound(w, r)
			return
		}
		b, e = fs.ReadFile(s.assets, "index.html")
		if e != nil {
			http.NotFound(w, r)
			return
		}
		name = "index.html"
	}
	if name == "index.html" {
		w.Header().Set("Cache-Control", "no-store")
	}
	http.ServeContent(w, r, name, time.Time{}, strings.NewReader(string(b)))
}
func jsonout(w http.ResponseWriter, status int, x any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(x)
}
func failure(w http.ResponseWriter, status int) {
	message := map[int]string{400: "invalid request", 401: "unauthorized", 403: "forbidden", 404: "not found", 409: "conflict", 429: "rate limited", 503: "unavailable"}[status]
	if message == "" {
		message = "internal error"
	}
	jsonout(w, status, map[string]string{"error": message})
}
func resultErr(w http.ResponseWriter, e error) {
	switch {
	case e == nil:
		return
	case errors.Is(e, store.ErrInvalid):
		failure(w, 400)
	case errors.Is(e, store.ErrNotFound):
		failure(w, 404)
	case errors.Is(e, store.ErrConflict):
		failure(w, 409)
	default:
		failure(w, 503)
	}
}
func launchFailure(w http.ResponseWriter, err error) {
	var launch *model.LaunchError
	if !errors.As(err, &launch) || launch == nil {
		failure(w, 503)
		return
	}
	message := launch.PublicMessage
	if message == "" || len(message) > 240 || strings.ContainsAny(message, "\r\n\x00") {
		message = "Could not start the agent in the created workspace."
	}
	id := launch.WorkspaceID
	if len(id) > 128 {
		id = ""
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == ':') {
			id = ""
			break
		}
	}
	if id != "" {
		message += " Workspace criado: " + id + "."
	}
	jsonout(w, 503, map[string]string{"error": message, "workspace_id": id})
}
func decode(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return store.ErrInvalid
	}
	return nil
}
func (s *Server) originOK(r *http.Request) bool { return r.Header.Get("Origin") == s.C.Origin }
func (s *Server) rateLimit(r *http.Request) bool {
	host, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil {
		host = r.RemoteAddr
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	recent := s.attempts[host][:0]
	for _, v := range s.attempts[host] {
		if now.Sub(v) < time.Minute {
			recent = append(recent, v)
		}
	}
	if len(recent) >= 8 {
		s.attempts[host] = recent
		return false
	}
	s.attempts[host] = append(recent, now)
	if len(s.attempts) > 10000 {
		s.attempts = map[string][]time.Time{host: s.attempts[host]}
	}
	return true
}
func (s *Server) session(r *http.Request) (auth.Session, string, error) {
	c, e := r.Cookie("herdr_session")
	if e != nil {
		return auth.Session{}, "", e
	}
	x, e := s.Auth.Validate(r.Context(), c.Value, time.Now())
	return x, c.Value, e
}
func (s *Server) setSessionCookie(w http.ResponseWriter, token string, expires, now time.Time) {
	remaining := expires.Sub(now)
	if remaining <= 0 {
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "herdr_session", Value: token, Path: "/", HttpOnly: true, Secure: !s.C.AllowInsecureLocal, SameSite: http.SameSiteStrictMode, MaxAge: int(remaining / time.Second), Expires: expires})
}
func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/api/v1/")
	if p == "auth/status" && r.Method == "GET" {
		configured, u, e := s.Auth.Configured(r.Context())
		if e != nil {
			failure(w, 503)
			return
		}
		x := map[string]any{"authenticated": false, "configured": configured}
		x["setup_available"] = !configured && s.setupAvailable(r)
		x["setup_browser_enabled"] = s.bootstrapVerify != nil
		if ses, token, e := s.session(r); e == nil {
			x["authenticated"] = true
			x["username"] = u
			x["csrf_token"] = s.Auth.TokenCSRF(token)
			s.setSessionCookie(w, token, ses.ExpiresAt, time.Now())
		}
		jsonout(w, 200, x)
		return
	}
	if strings.HasPrefix(p, "auth/setup/") {
		s.setup(w, r, p)
		return
	}
	if p == "auth/login" && r.Method == "POST" {
		if !s.originOK(r) {
			failure(w, 403)
			return
		}
		if !s.rateLimit(r) {
			failure(w, 429)
			return
		}
		var in struct{ Username, Password, OTP string }
		if decode(r, &in) != nil {
			failure(w, 400)
			return
		}
		loginAt := time.Now()
		token, csrf, e := s.Auth.Login(r.Context(), in.Username, in.Password, in.OTP, loginAt)
		if e != nil {
			failure(w, 401)
			return
		}
		s.setSessionCookie(w, token, loginAt.Add(auth.SessionLifetime), loginAt)
		jsonout(w, 200, map[string]string{"username": in.Username, "csrf_token": csrf})
		return
	}
	ses, token, e := s.session(r)
	if e != nil {
		failure(w, 401)
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		if !s.originOK(r) || !s.Auth.CSRF(ses, r.Header.Get("X-CSRF-Token")) {
			failure(w, 403)
			return
		}
	}
	if p == "auth/logout" && r.Method == "POST" {
		if e := s.Auth.Logout(r.Context(), token); e != nil {
			s.cancelStreams(token)
			failure(w, 503)
			return
		}
		s.cancelStreams(token)
		http.SetCookie(w, &http.Cookie{Name: "herdr_session", Path: "/", Value: "", MaxAge: -1, HttpOnly: true, Secure: !s.C.AllowInsecureLocal, SameSite: http.SameSiteStrictMode})
		jsonout(w, 200, map[string]bool{"ok": true})
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		if e := s.Auth.Touch(r.Context(), token, time.Now()); e != nil {
			failure(w, 401)
			return
		}
	}
	switch {
	case p == "github/status" && r.Method == "GET":
		s.githubStatus(w, r)
	case p == "github/repositories" && r.Method == "GET":
		s.githubRepositories(w, r)
	case p == "github/issues" && r.Method == "GET":
		s.githubIssueList(w, r)
	case p == "github/issues" && r.Method == "POST":
		s.githubIssueCreate(w, r)
	case strings.HasPrefix(p, "github/issues/") && r.Method == "PATCH":
		s.githubIssueUpdate(w, r, strings.TrimPrefix(p, "github/issues/"))
	case strings.HasPrefix(p, "spaces/") && strings.HasSuffix(p, "/project") && r.Method == "POST":
		s.spaceProject(w, r)
	case p == "projects":
		s.collection(w, r, "projects")
	case strings.HasPrefix(p, "projects/"):
		s.item(w, r, "projects", strings.TrimPrefix(p, "projects/"))
	case p == "tasks":
		s.collection(w, r, "tasks")
	case strings.HasPrefix(p, "tasks/"):
		s.item(w, r, "tasks", strings.TrimPrefix(p, "tasks/"))
	case p == "notes":
		s.collection(w, r, "notes")
	case strings.HasPrefix(p, "notes/"):
		s.item(w, r, "notes", strings.TrimPrefix(p, "notes/"))
	case p == "preferences":
		s.preferences(w, r)
	case p == "sessions" && r.Method == "GET":
		s.inventory(w, r)
	case p == "sessions/start" && r.Method == "POST":
		s.start(w, r)
	case strings.HasPrefix(p, "sessions/"):
		s.sessionAction(w, r, p)
	case p == "events" && r.Method == "GET":
		s.events(w, r, token)
	case strings.HasPrefix(p, "terminals/") && strings.HasSuffix(p, "/stream") && r.Method == "GET":
		s.stream(w, r, token, strings.TrimSuffix(strings.TrimPrefix(p, "terminals/"), "/stream"))
	default:
		failure(w, 404)
	}
}
func (s *Server) collection(w http.ResponseWriter, r *http.Request, kind string) {
	ctx := r.Context()
	if r.Method == "GET" {
		var items any
		var e error
		switch kind {
		case "projects":
			items, e = s.Store.ListProjects(ctx)
		case "tasks":
			items, e = s.Store.ListTasks(ctx)
		case "notes":
			items, e = s.Store.ListNotes(ctx)
		}
		if e != nil {
			resultErr(w, e)
			return
		}
		jsonout(w, 200, map[string]any{"items": items})
		return
	}
	if r.Method != "POST" {
		failure(w, 404)
		return
	}
	switch kind {
	case "projects":
		var x model.Project
		if decode(r, &x) != nil {
			failure(w, 400)
			return
		}
		y, e := s.Store.CreateProject(ctx, x)
		if e != nil {
			resultErr(w, e)
			return
		}
		jsonout(w, 201, map[string]any{"item": y})
	case "tasks":
		var x model.Task
		if decode(r, &x) != nil {
			failure(w, 400)
			return
		}
		y, e := s.Store.CreateTask(ctx, x)
		if e != nil {
			resultErr(w, e)
			return
		}
		jsonout(w, 201, map[string]any{"item": y})
	case "notes":
		var x model.Note
		if decode(r, &x) != nil {
			failure(w, 400)
			return
		}
		y, e := s.Store.CreateNote(ctx, x)
		if e != nil {
			resultErr(w, e)
			return
		}
		jsonout(w, 201, map[string]any{"item": y})
	}
}
func (s *Server) item(w http.ResponseWriter, r *http.Request, kind, id string) {
	if id == "" || strings.Contains(id, "/") {
		failure(w, 404)
		return
	}
	ctx := r.Context()
	if r.Method == "DELETE" {
		var e error
		switch kind {
		case "projects":
			e = s.Store.DeleteProject(ctx, id)
		case "tasks":
			e = s.Store.DeleteTask(ctx, id)
		case "notes":
			e = s.Store.DeleteNote(ctx, id)
		}
		if e != nil {
			resultErr(w, e)
			return
		}
		jsonout(w, 200, map[string]bool{"ok": true})
		return
	}
	if r.Method != "PATCH" {
		failure(w, 404)
		return
	}
	switch kind {
	case "projects":
		old, e := s.Store.GetProject(ctx, id)
		if e != nil {
			resultErr(w, e)
			return
		}
		var x model.Project
		if decode(r, &x) != nil {
			failure(w, 400)
			return
		}
		x.ID = id
		x.CreatedAt = old.CreatedAt
		y, e := s.Store.UpdateProject(ctx, x)
		if e != nil {
			resultErr(w, e)
			return
		}
		jsonout(w, 200, map[string]any{"item": y})
	case "tasks":
		old, e := s.Store.GetTask(ctx, id)
		if e != nil {
			resultErr(w, e)
			return
		}
		var x model.Task
		if decode(r, &x) != nil {
			failure(w, 400)
			return
		}
		x.ID = id
		x.CreatedAt = old.CreatedAt
		y, e := s.Store.UpdateTask(ctx, x)
		if e != nil {
			resultErr(w, e)
			return
		}
		jsonout(w, 200, map[string]any{"item": y})
	case "notes":
		var x model.Note
		if decode(r, &x) != nil {
			failure(w, 400)
			return
		}
		x.ID = id
		y, e := s.Store.UpdateNote(ctx, x)
		if errors.Is(e, store.ErrConflict) {
			jsonout(w, 409, map[string]any{"error": "conflict", "current": y})
			return
		}
		if e != nil {
			resultErr(w, e)
			return
		}
		jsonout(w, 200, map[string]any{"item": y})
	}
}
func (s *Server) preferences(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		x, e := s.Store.Preferences(r.Context())
		if e != nil {
			resultErr(w, e)
			return
		}
		jsonout(w, 200, map[string]any{"item": x})
		return
	}
	if r.Method != "PATCH" {
		failure(w, 404)
		return
	}
	var x model.Preferences
	if decode(r, &x) != nil {
		failure(w, 400)
		return
	}
	y, e := s.Store.SetPreferences(r.Context(), x)
	if e != nil {
		resultErr(w, e)
		return
	}
	jsonout(w, 200, map[string]any{"item": y})
}
func (s *Server) inventory(w http.ResponseWriter, r *http.Request) {
	if s.Provider == nil {
		failure(w, 503)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	x, e := s.Provider.Inventory(ctx)
	if e != nil {
		failure(w, 503)
		return
	}
	if x.Items == nil {
		x.Items = []model.Session{}
	}
	if x.Spaces == nil {
		x.Spaces = []model.Space{}
	}
	s.mapProjects(r.Context(), &x)
	jsonout(w, 200, x)
}
func (s *Server) projectPath(ctx context.Context, id string) (string, error) {
	if id == "" {
		return "", store.ErrInvalid
	}
	p, e := s.Store.GetProject(ctx, id)
	if e != nil {
		return "", e
	}
	return p.Path, nil
}
func (s *Server) start(w http.ResponseWriter, r *http.Request) {
	if s.Provider == nil {
		failure(w, 503)
		return
	}
	var x model.StartRequest
	if decode(r, &x) != nil {
		failure(w, 400)
		return
	}
	project, e := s.projectPath(r.Context(), x.ProjectID)
	if e != nil {
		resultErr(w, e)
		return
	}
	x.ProjectPath = project
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	y, e := s.Provider.Launch(ctx, x)
	if e != nil {
		launchFailure(w, e)
		return
	}
	jsonout(w, 201, map[string]any{"item": y})
}
func (s *Server) sessionAction(w http.ResponseWriter, r *http.Request, p string) {
	if r.Method != "POST" || s.Provider == nil {
		failure(w, 404)
		return
	}
	parts := strings.Split(p, "/")
	if len(parts) != 3 || parts[0] != "sessions" || parts[1] == "" {
		failure(w, 404)
		return
	}
	id, action := parts[1], parts[2]
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	if action == "stop" {
		inv, e := s.Provider.Inventory(ctx)
		if e != nil || inv.Stale {
			failure(w, 503)
			return
		}
		found, allowed := false, false
		for _, item := range inv.Items {
			if item.ID == id {
				found = true
				for _, capability := range item.Capabilities {
					if capability == "stop" && item.Alive {
						allowed = true
						break
					}
				}
				break
			}
		}
		if !found {
			failure(w, 404)
			return
		}
		if !allowed {
			failure(w, 409)
			return
		}
	}
	switch action {
	case "stop":
		var x struct {
			Confirm bool `json:"confirm"`
			Force   bool `json:"force"`
		}
		if decode(r, &x) != nil || !x.Confirm {
			failure(w, 400)
			return
		}
		if e := s.Provider.Stop(ctx, id, x.Force); e != nil {
			failure(w, 503)
			return
		}
		jsonout(w, 200, map[string]bool{"ok": true})
	case "resume":
		var x struct {
			Confirm   bool   `json:"confirm"`
			ProjectID string `json:"project_id"`
		}
		if decode(r, &x) != nil || !x.Confirm {
			failure(w, 400)
			return
		}
		project, e := s.projectPath(ctx, x.ProjectID)
		if e != nil {
			resultErr(w, e)
			return
		}
		y, e := s.Provider.Resume(ctx, id, model.StartRequest{ProjectID: x.ProjectID, ProjectPath: project})
		if e != nil {
			launchFailure(w, e)
			return
		}
		jsonout(w, 200, map[string]any{"item": y})
	default:
		failure(w, 404)
	}
}
func (s *Server) events(w http.ResponseWriter, r *http.Request, token string) {
	if s.Provider == nil {
		failure(w, 503)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		failure(w, 503)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	ctxStream, cancelStream := context.WithCancel(r.Context())
	defer cancelStream()
	streamID := s.addStream(token, cancelStream)
	defer s.removeStream(token, streamID)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		if _, e := s.Auth.Validate(ctxStream, token, time.Now()); e != nil {
			return
		}
		ctx, cancel := context.WithTimeout(ctxStream, 5*time.Second)
		inv, e := s.Provider.Inventory(ctx)
		cancel()
		if e != nil {
			inv = model.Inventory{Items: []model.Session{}, Spaces: []model.Space{}, Stale: true, Warning: "unavailable"}
		}
		if inv.Spaces == nil {
			inv.Spaces = []model.Space{}
		}
		s.mapProjects(ctxStream, &inv)
		b, _ := json.Marshal(inv)
		if _, e = w.Write(append(append([]byte("event: sessions\ndata: "), b...), []byte("\n\n")...)); e != nil {
			return
		}
		flusher.Flush()
		select {
		case <-ctxStream.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *Server) addStream(token string, cancel context.CancelFunc) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shuttingDown {
		cancel()
		return ""
	}
	if s.streams[token] == nil {
		s.streams[token] = map[string]streamRegistration{}
	}
	id := store.ID()
	s.streams[token][id] = streamRegistration{cancel: cancel}
	return id
}
func (s *Server) promoteAuthStream(token, id string, authInvalidated func()) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shuttingDown {
		return false
	}
	stream, ok := s.streams[token][id]
	if !ok {
		return false
	}
	stream.authInvalidated = authInvalidated
	s.streams[token][id] = stream
	return true
}
func (s *Server) removeStream(token, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.streams[token], id)
}
func (s *Server) cancelStreams(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, stream := range s.streams[token] {
		if stream.authInvalidated != nil {
			stream.authInvalidated()
			continue
		}
		stream.cancel()
	}
	delete(s.streams, token)
}
func (s *Server) stream(w http.ResponseWriter, r *http.Request, token, id string) {
	if s.Provider == nil {
		failure(w, 503)
		return
	}
	if !s.originOK(r) {
		failure(w, 403)
		return
	}
	q := r.URL.Query()
	mode := q.Get("mode")
	if mode != "control" && mode != "observe" {
		failure(w, 400)
		return
	}
	cols, _ := strconv.Atoi(q.Get("cols"))
	rows, _ := strconv.Atoi(q.Get("rows"))
	if cols == 0 {
		cols = 120
	}
	if rows == 0 {
		rows = 40
	}
	if cols < 20 || cols > 500 || rows < 5 || rows > 200 {
		failure(w, 400)
		return
	}
	takeover := q.Get("takeover") == "true"
	if q.Get("takeover") != "" && q.Get("takeover") != "true" && q.Get("takeover") != "false" {
		failure(w, 400)
		return
	}
	if takeover && mode != "control" {
		failure(w, 400)
		return
	}
	checkCtx, checkCancel := context.WithTimeout(r.Context(), 5*time.Second)
	inv, err := s.Provider.Inventory(checkCtx)
	checkCancel()
	if err != nil || inv.Stale {
		failure(w, 503)
		return
	}
	found, allowed := false, false
	for _, item := range inv.Items {
		if item.ID != id {
			continue
		}
		found = true
		for _, capability := range item.Capabilities {
			if capability == mode {
				allowed = true
				break
			}
		}
		break
	}
	if !found {
		failure(w, 404)
		return
	}
	if !allowed {
		failure(w, 409)
		return
	}
	conn, e := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{r.Host}})
	if e != nil {
		return
	}
	defer conn.CloseNow()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	// Keep socket operations independent of bridge cancellation so a revoked
	// session can receive a final public error when the peer is still reading.
	wireCtx, wireCancel := context.WithCancel(context.Background())
	defer wireCancel()
	authInvalidated := make(chan struct{})
	var authInvalidatedOnce sync.Once
	signalAuthInvalidation := func() { authInvalidatedOnce.Do(func() { close(authInvalidated); cancel() }) }
	streamID := s.addStream(token, cancel)
	if streamID == "" {
		return
	}
	defer s.removeStream(token, streamID)
	if _, e := s.Auth.Validate(ctx, token, time.Now()); e != nil {
		return
	}
	helloCtx, helloCancel := context.WithTimeout(ctx, 5*time.Second)
	typ, b, e := conn.Read(helloCtx)
	helloCancel()
	if e != nil || typ != websocket.MessageText || len(b) > 4096 {
		return
	}
	var hello struct {
		Type      string `json:"type"`
		CSRFToken string `json:"csrf_token"`
	}
	if json.Unmarshal(b, &hello) != nil || hello.Type != "hello" {
		return
	}
	ses, e := s.Auth.Validate(ctx, token, time.Now())
	if e != nil {
		if ctx.Err() == nil {
			writeAuthStreamError(conn)
		}
		return
	}
	if !s.Auth.CSRF(ses, hello.CSRFToken) {
		return
	}
	stream, e := s.Provider.Attach(ctx, model.StreamRequest{ID: id, Mode: mode, Takeover: takeover, Cols: cols, Rows: rows})
	if e != nil {
		if writeAuthErrorIfInvalidated(conn, authInvalidated) || ctx.Err() != nil {
			return
		}
		writePublicStreamError(conn, "terminal_unavailable", "Could not open the terminal now.")
		return
	}
	defer stream.Close()
	if !s.promoteAuthStream(token, streamID, signalAuthInvalidation) {
		writeAuthErrorIfInvalidated(conn, authInvalidated)
		return
	}
	if _, e := s.Auth.Validate(ctx, token, time.Now()); e != nil {
		if ctx.Err() != nil {
			writeAuthErrorIfInvalidated(conn, authInvalidated)
			return
		}
		signalAuthInvalidation()
		writeAuthStreamError(conn)
		return
	}
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	go func() {
		for {
			typ, b, e := conn.Read(wireCtx)
			if e != nil {
				cancel()
				return
			}
			if typ != websocket.MessageText || len(b) > 8192 {
				cancel()
				return
			}
			var m struct {
				Type  string `json:"type"`
				Data  string `json:"data"`
				Cols  int    `json:"cols"`
				Rows  int    `json:"rows"`
				Delta int    `json:"delta"`
			}
			if json.Unmarshal(b, &m) != nil {
				cancel()
				return
			}
			switch m.Type {
			case "input":
				if mode != "control" || len(m.Data) > 4096 {
					cancel()
					return
				}
				if s.Auth.Touch(ctx, token, time.Now()) != nil {
					if ctx.Err() != nil {
						return
					}
					signalAuthInvalidation()
					return
				}
				if stream.Input([]byte(m.Data)) != nil {
					cancel()
					return
				}
			case "resize":
				if m.Cols < 20 || m.Cols > 500 || m.Rows < 5 || m.Rows > 200 {
					cancel()
					return
				}
				if _, e := s.Auth.Validate(ctx, token, time.Now()); e != nil {
					if ctx.Err() != nil {
						return
					}
					signalAuthInvalidation()
					return
				}
				if stream.Resize(m.Cols, m.Rows) != nil {
					cancel()
					return
				}
			case "scroll":
				if m.Delta < -10000 || m.Delta > 10000 {
					cancel()
					return
				}
				if s.Auth.Touch(ctx, token, time.Now()) != nil {
					if ctx.Err() != nil {
						return
					}
					signalAuthInvalidation()
					return
				}
				if stream.Scroll(m.Delta) != nil {
					cancel()
					return
				}
			case "release":
				cancel()
				return
			default:
				cancel()
				return
			}
		}
	}()
	frames, streamErrors := stream.Frames(), stream.Errors()
	writeFrame := func(frame []byte) bool {
		if writeAuthErrorIfInvalidated(conn, authInvalidated) {
			return false
		}
		if len(frame) > 1<<20 {
			writePublicStreamError(conn, "terminal_unavailable", "The terminal stream was interrupted.")
			return false
		}
		writeCtx, writeCancel := context.WithTimeout(wireCtx, time.Second)
		defer writeCancel()
		return conn.Write(writeCtx, websocket.MessageBinary, frame) == nil
	}
	for frames != nil || streamErrors != nil {
		select {
		case <-ctx.Done():
			writeAuthErrorIfInvalidated(conn, authInvalidated)
			return
		case <-authInvalidated:
			writeAuthStreamError(conn)
			return
		case <-tick.C:
			if _, e := s.Auth.Validate(ctx, token, time.Now()); e != nil {
				if ctx.Err() != nil {
					writeAuthErrorIfInvalidated(conn, authInvalidated)
					return
				}
				signalAuthInvalidation()
				writeAuthStreamError(conn)
				return
			}
		case frame, ok := <-frames:
			if !ok {
				frames = nil
				// Frames closing ends a normal stream even if the producer keeps
				// Errors open until Close. Still inspect any already-buffered error.
				pendingErrors := len(streamErrors)
				for i := 0; streamErrors != nil && i < pendingErrors; i++ {
					select {
					case <-ctx.Done():
						writeAuthErrorIfInvalidated(conn, authInvalidated)
						return
					case <-authInvalidated:
						writeAuthStreamError(conn)
						return
					case streamErr, ok := <-streamErrors:
						if !ok {
							streamErrors = nil
							writeAuthErrorIfInvalidated(conn, authInvalidated)
							return
						}
						if streamErr == nil {
							continue
						}
						code, message := publicStreamFailure(streamErr)
						writePublicStreamError(conn, code, message)
						return
					default:
						writeAuthErrorIfInvalidated(conn, authInvalidated)
						return
					}
				}
				return
			}
			if !writeFrame(frame) {
				return
			}
		case streamErr, ok := <-streamErrors:
			if !ok {
				streamErrors = nil
				continue
			}
			if streamErr == nil {
				continue
			}
			if writeAuthErrorIfInvalidated(conn, authInvalidated) {
				return
			}
			// Frames may already be buffered when the error channel becomes ready.
			// Drain those first so closing both channels cannot discard a final frame.
			pendingFrames := 0
			if frames != nil {
				pendingFrames = len(frames)
			}
			for i := 0; frames != nil && i < pendingFrames; i++ {
				select {
				case <-ctx.Done():
					writeAuthErrorIfInvalidated(conn, authInvalidated)
					return
				case <-authInvalidated:
					writeAuthStreamError(conn)
					return
				case frame, ok := <-frames:
					if !ok {
						frames = nil
						break
					}
					if !writeFrame(frame) {
						return
					}
				}
			}
			code, message := publicStreamFailure(streamErr)
			writePublicStreamError(conn, code, message)
			return
		}
	}
	writeAuthErrorIfInvalidated(conn, authInvalidated)
}

func publicStreamFailure(err error) (string, string) {
	if errors.Is(err, model.ErrTerminalStreamSlowConsumer) {
		return "terminal_slow_consumer", "The terminal is receiving data too quickly. Reconnect to continue."
	}
	return "terminal_unavailable", "The terminal stream was interrupted."
}

func writeAuthErrorIfInvalidated(conn *websocket.Conn, authInvalidated <-chan struct{}) bool {
	select {
	case <-authInvalidated:
		writeAuthStreamError(conn)
		return true
	default:
		return false
	}
}

func writeAuthStreamError(conn *websocket.Conn) {
	writePublicStreamError(conn, "auth_expired_or_revoked", "Your session expired or was revoked. Sign in again.")
}

func writePublicStreamError(conn *websocket.Conn, code, message string) {
	payload, _ := json.Marshal(struct {
		Type    string `json:"type"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}{Type: "error", Code: code, Message: message})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = conn.Write(ctx, websocket.MessageText, payload)
}

func (s *Server) mapProjects(ctx context.Context, inv *model.Inventory) {
	projects, e := s.Store.ListProjects(ctx)
	if e != nil {
		return
	}
	for i := range inv.Items {
		if inv.Items[i].ProjectID != "" {
			continue
		}
		cwd := filepath.Clean(inv.Items[i].CWD)
		if !filepath.IsAbs(cwd) {
			continue
		}
		longest := 0
		for _, p := range projects {
			project := filepath.Clean(p.Path)
			rel, e := filepath.Rel(project, cwd)
			if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				continue
			}
			if len(project) > longest {
				inv.Items[i].ProjectID = p.ID
				longest = len(project)
			}
		}
	}
}
