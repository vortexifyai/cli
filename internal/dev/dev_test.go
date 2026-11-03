package dev

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"vortexify-cli/internal/config"
)

func appFile(t *testing.T, root, name, content string) {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
func TestFindApp(t *testing.T) {
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	cfg := &config.ProjectConfig{ProjectID: "project", Apps: &config.ResourceFolder{Folder: "apps"}}
	app := filepath.Join(root, "apps", "one")
	appFile(t, root, "apps/one/package.json", `{"scripts":{"dev":"vite"}}`)
	appFile(t, root, "apps/one/src/App.tsx", "code")
	for _, selection := range []string{"", "one", "apps/one"} {
		got, err := FindApp(root, cfg, selection, root)
		if err != nil || got != app {
			t.Fatalf("select %q: %q %v", selection, got, err)
		}
	}
	got, err := FindApp(root, cfg, "", filepath.Join(app, "src"))
	if err != nil || got != app {
		t.Fatal("nested detection", got, err)
	}
	appFile(t, root, "apps/two/package.json", `{"scripts":{"dev":"vite"}}`)
	if _, err = FindApp(root, cfg, "", root); err == nil {
		t.Fatal("accepted ambiguous app")
	}
	if _, err = FindApp(root, cfg, "../outside", root); err == nil {
		t.Fatal("accepted external app")
	}
	cfg.Apps = nil
	if _, err = FindApp(root, cfg, "", root); err == nil {
		t.Fatal("accepted unmanaged apps")
	}
}

func newProxy(t *testing.T, apiURL, viteURL string, token TokenSource) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	handler, err := NewHandler(apiURL, viteURL, origin, "project", token)
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = handler
	server.Start()
	t.Cleanup(server.Close)
	return server
}
func TestProxyTokenIsolationPathBodyAndStatus(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer cli-token" {
			t.Error("incorrect API token")
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("forwarded browser credentials")
		}
		if r.URL.EscapedPath() != "/api/v2/projects/project/apis/list_notes" || r.URL.RawQuery != "x=1" || r.Method != "POST" {
			t.Error("incorrect route", r.URL)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"project_id":"project","inputs":{"id":1}}` {
			t.Error("changed body")
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	vite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("token leaked to Vite")
		}
		fmt.Fprint(w, "vite "+r.URL.Path)
	}))
	defer vite.Close()
	server := newProxy(t, upstream.URL+"/api/v2", vite.URL, func(context.Context) (string, error) { return "cli-token", nil })
	req, _ := http.NewRequest("POST", server.URL+ProxyPath+"/api/v2/projects/project/apis/list_notes?x=1", strings.NewReader(`{"project_id":"project","inputs":{"id":1}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer browser-token")
	req.Header.Set("Cookie", "session=browser")
	req.Header.Set("Proxy-Authorization", "Bearer wrong")
	req.Header.Set("Origin", server.URL)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatal(resp.StatusCode)
	}
	for _, path := range []string{"/", "/src/App.tsx", "/__vortexify_other"} {
		req, _ := http.NewRequest("GET", server.URL+path, nil)
		req.Header.Set("Authorization", "Bearer should-not-reach-vite")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatal(resp.StatusCode)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("forwarded non-API request")
	}
}
func TestProxyBlocksForeignSitesAndHosts(t *testing.T) {
	var forwarded atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1) }))
	defer upstream.Close()
	server := newProxy(t, upstream.URL, upstream.URL, func(context.Context) (string, error) { return "secret", nil })
	for _, kind := range []string{"origin", "host", "fetch-site"} {
		req, _ := http.NewRequest("POST", server.URL+ProxyPath+"/api/v2/projects/project/apis/list_notes", nil)
		switch kind {
		case "origin":
			req.Header.Set("Origin", "https://attacker.example")
		case "host":
			req.Host = "attacker.example"
		case "fetch-site":
			req.Header.Set("Sec-Fetch-Site", "cross-site")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 403 {
			t.Fatalf("%s accepted: %d", kind, resp.StatusCode)
		}
	}
	if forwarded.Load() != 0 {
		t.Fatal("forwarded forbidden request")
	}
}
func TestProxyRefreshSourceAndAuthFailure(t *testing.T) {
	var current atomic.Value
	current.Store("one")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, r.Header.Get("Authorization")) }))
	defer upstream.Close()
	var sourceCalls atomic.Int32
	server := newProxy(t, upstream.URL, upstream.URL, func(context.Context) (string, error) {
		sourceCalls.Add(1)
		value := current.Load().(string)
		if value == "error" {
			return "", errors.New("private token-error detail")
		}
		return value, nil
	})
	for _, value := range []string{"one", "two", "error"} {
		current.Store(value)
		resp, err := http.Post(server.URL+ProxyPath+"/api/v2/projects/project/apis/list_notes", "application/json", strings.NewReader(`{"project_id":"project"}`))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if value == "error" {
			if resp.StatusCode != 401 || strings.Contains(string(body), "private") {
				t.Fatal("authentication failure leaked details", string(body))
			}
		} else if string(body) != "Bearer "+value {
			t.Fatal("cached stale token", string(body))
		}
	}
	if sourceCalls.Load() != 3 {
		t.Fatal("token source not checked on each request")
	}
}
func TestProxySerializesTokenRefresh(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()
	var active atomic.Int32
	server := newProxy(t, upstream.URL, upstream.URL, func(context.Context) (string, error) {
		if active.Add(1) != 1 {
			t.Error("concurrent token refresh")
		}
		time.Sleep(time.Millisecond)
		active.Add(-1)
		return "token", nil
	})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Post(server.URL+ProxyPath+"/api/v2/projects/project/apis/list_notes", "application/json", strings.NewReader(`{"project_id":"project"}`))
			if err != nil {
				t.Error(err)
				return
			}
			resp.Body.Close()
		}()
	}
	wg.Wait()
}
func TestProxyDoesNotSendTokenToRedirectDestination(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("CLI token leaked across redirect")
		}
		fmt.Fprint(w, "external")
	}))
	defer destination.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 302) }))
	defer upstream.Close()
	server := newProxy(t, upstream.URL, upstream.URL, func(context.Context) (string, error) { return "cli-secret", nil })
	resp, err := http.Post(server.URL+ProxyPath+"/api/v2/projects/project/apis/list_notes", "application/json", strings.NewReader(`{"project_id":"project"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "external" {
		t.Fatal("redirect did not complete")
	}
}
func TestChildEnvironmentHasNoCLIToken(t *testing.T) {
	env := childEnvironment([]string{"PATH=/bin", "VORTEXIFY_TOKEN=secret", "VORTEXIFY_TOKEN_URL=private", "VITE_VORTEXIFY_DEV_TOKEN=old-secret", "NODE_ENV=production", "VITE_VORTEXIFY_PROJECT_ID=wrong"}, Options{APIURL: "https://api.example", ProjectID: "project", OrgID: "org", AppID: "app"})
	values := map[string]string{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = value
	}
	if _, ok := values["VORTEXIFY_TOKEN"]; ok {
		t.Fatal("CLI token inherited by npm")
	}
	if values["VITE_VORTEXIFY_DEV_TOKEN"] != "" || values["VITE_VORTEXIFY_PROJECT_ID"] != "project" || values["VITE_VORTEXIFY_ORG_ID"] != "org" || values["VITE_VORTEXIFY_DEV_PROXY_PATH"] != ProxyPath || values["NODE_ENV"] != "development" || values["VITE_APP_ID"] != "app" {
		t.Fatal("incorrect development environment", values)
	}
}

func TestRunStartsDevScriptProxiesAndStopsChildren(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX npm integration fixture")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm unavailable")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable")
	}
	app := t.TempDir()
	appFile(t, app, "package.json", `{"type":"module","scripts":{"dev":"node fixture.mjs"}}`)
	appFile(t, app, "fixture.mjs", `import http from "node:http";
const port=Number(process.argv[process.argv.indexOf("--port")+1]);
http.createServer((req,res)=>{if(req.url==="/@vite/client"){res.end("ready");return;}res.setHeader("Content-Type","application/json");res.end(JSON.stringify({port,project:process.env.VITE_VORTEXIFY_PROJECT_ID,proxy:process.env.VITE_VORTEXIFY_DEV_PROXY_PATH,token:process.env.VORTEXIFY_TOKEN??null,legacy:process.env.VITE_VORTEXIFY_DEV_TOKEN}));}).listen(port,"127.0.0.1");`)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer cli-token" {
			t.Error("missing CLI token")
		}
		fmt.Fprint(w, "api works")
	}))
	defer upstream.Close()
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := reserved.Addr().(*net.TCPAddr).Port
	reserved.Close()
	origin := fmt.Sprintf("http://127.0.0.1:%d", port)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	t.Setenv("VORTEXIFY_TOKEN", "must-not-reach-npm")
	go func() {
		done <- Run(ctx, Options{AppPath: app, ProjectID: "project", APIURL: upstream.URL, Port: port, Output: io.Discard, Errors: io.Discard, Token: func(context.Context) (string, error) { return "cli-token", nil }})
	}()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)
	var page struct {
		Port    int
		Project string
		Proxy   string
		Token   *string
		Legacy  string
	}
	ready := false
	for time.Now().Before(deadline) {
		resp, err := client.Get(origin)
		if err == nil {
			if resp.StatusCode == 200 {
				err = json.NewDecoder(resp.Body).Decode(&page)
				ready = err == nil
			}
			resp.Body.Close()
			if ready {
				break
			}
		}
		select {
		case err := <-done:
			t.Fatalf("early dev exit: %v", err)
		default:
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		cancel()
		t.Fatal("dev app did not become ready")
	}
	if page.Project != "project" || page.Proxy != ProxyPath || page.Token != nil || page.Legacy != "" {
		t.Fatal("incorrect Vite environment", page)
	}
	resp, err := client.Post(origin+ProxyPath+"/api/v2/projects/project/apis/list_notes", "application/json", strings.NewReader(`{"project_id":"project"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "api works" {
		t.Fatal("API proxy failed", string(body))
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dev failed to stop")
	}
	if resp, err = client.Get(fmt.Sprintf("http://127.0.0.1:%d", page.Port)); err == nil {
		resp.Body.Close()
		t.Fatal("dev child still listening")
	}
}

func TestProxyStreamsWithoutBuffering(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: token\ndata: {\"text\":\"first\"}\n\n")
		w.(http.Flusher).Flush()
		<-release
		fmt.Fprint(w, "event: tool_call\ndata: {\"callId\":\"one\",\"name\":\"open\",\"target\":\"frontend\"}\n\nevent: done\ndata: {}\n\n")
	}))
	defer upstream.Close()
	server := newProxy(t, upstream.URL, upstream.URL, func(context.Context) (string, error) { return "token", nil })
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Post(server.URL+ProxyPath+"/api/v2/artifacts/agents/run_agent", "application/json", strings.NewReader(`{"project_id":"project"}`))
	if err != nil {
		once.Do(func() { close(release) })
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	if err != nil || line != "event: token\n" {
		once.Do(func() { close(release) })
		t.Fatalf("buffered stream %q %v", line, err)
	}
	once.Do(func() { close(release) })
	rest, err := io.ReadAll(reader)
	if err != nil || !strings.Contains(string(rest), "event: tool_call\n") || !strings.HasSuffix(string(rest), "event: done\ndata: {}\n\n") {
		t.Fatalf("incomplete agent stream: %q %v", rest, err)
	}
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal("lost SSE content type")
	}
}

func TestProxyPreservesWebSocketUpgrade(t *testing.T) {
	vite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("API token reached HMR")
		}
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.Close()
		fmt.Fprint(connection, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\nhello")
	}))
	defer vite.Close()
	server := newProxy(t, vite.URL, vite.URL, func(context.Context) (string, error) { t.Error("HMR requested an API token"); return "secret", nil })
	connection, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	connection.SetDeadline(time.Now().Add(time.Second))
	fmt.Fprintf(connection, "GET /hmr HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n", server.Listener.Addr().String())
	reader := bufio.NewReader(connection)
	response, err := http.ReadResponse(reader, nil)
	if err != nil || response.StatusCode != 101 {
		t.Fatalf("upgrade: %v %#v", err, response)
	}
	payload := make([]byte, 5)
	if _, err = io.ReadFull(reader, payload); err != nil || string(payload) != "hello" {
		t.Fatalf("upgrade data %q %v", payload, err)
	}
}

// Opt-in test using an installed Vite, without adding npm dependencies to the Go project.
func TestRunWithRealVite(t *testing.T) {
	viteBin := os.Getenv("VORTEXIFY_TEST_VITE_BIN")
	if viteBin == "" {
		t.Skip("set VORTEXIFY_TEST_VITE_BIN to an installed vite/bin/vite.js")
	}
	if runtime.GOOS == "windows" {
		t.Skip("POSIX npm script fixture")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable")
	}
	app := t.TempDir()
	pkg, _ := json.Marshal(map[string]any{"type": "module", "scripts": map[string]string{"dev": "node '" + strings.ReplaceAll(viteBin, "'", "'\\''") + "'"}})
	appFile(t, app, "package.json", string(pkg))
	appFile(t, app, "index.html", `<div id="root"></div><script type="module" src="/src/main.js"></script>`)
	appFile(t, app, "src/main.js", `document.getElementById("root").textContent = import.meta.env.VITE_VORTEXIFY_PROJECT_ID; export const proxy = import.meta.env.VITE_VORTEXIFY_DEV_PROXY_PATH;`)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer cli-token" {
			t.Error("missing API token")
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := reserved.Addr().(*net.TCPAddr).Port
	reserved.Close()
	origin := fmt.Sprintf("http://127.0.0.1:%d", port)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{AppPath: app, ProjectID: "vite-project", APIURL: upstream.URL, Port: port, Output: io.Discard, Errors: io.Discard, Token: func(context.Context) (string, error) { return "cli-token", nil }})
	}()
	client := &http.Client{Timeout: time.Second}
	ready := false
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(origin + "/src/main.js")
		if err == nil {
			data, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 && strings.Contains(string(data), "vite-project") && strings.Contains(string(data), ProxyPath) {
				ready = true
				break
			}
		}
		select {
		case err := <-done:
			t.Fatalf("Vite failed: %v", err)
		default:
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		cancel()
		t.Fatal("Vite did not serve transformed app with CLI settings")
	}
	// The real HMR websocket must connect through the public Go proxy.
	probe := `const origin=process.argv[1]; const client=await (await fetch(origin+"/@vite/client")).text(); const token=client.match(/const wsToken = "([^"]+)"/)[1]; await new Promise((resolve,reject)=>{const ws=new WebSocket(origin.replace("http:","ws:")+"/?token="+token,"vite-hmr"); const timeout=setTimeout(()=>{ws.close();reject(new Error("HMR timed out"));},3000); ws.addEventListener("message",event=>{if(JSON.parse(event.data).type==="connected"){clearTimeout(timeout);ws.close();resolve();}});ws.addEventListener("error",()=>{clearTimeout(timeout);reject(new Error("HMR failed"));});});`
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer probeCancel()
	if output, err := exec.CommandContext(probeCtx, "node", "--input-type=module", "-e", probe, origin).CombinedOutput(); err != nil {
		cancel()
		t.Fatalf("real HMR: %v %s", err, output)
	}
	resp, err := client.Post(origin+ProxyPath+"/api/v2/projects/vite-project/apis/list_notes", "application/json", strings.NewReader(`{"project_id":"vite-project","inputs":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(data) != `{"ok":true}` {
		t.Fatal("real Vite API proxy", string(data))
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Vite did not stop")
	}
}

func TestProxyRejectsOtherProjectsAndUnrelatedRoutes(t *testing.T) {
	var calls atomic.Int32
	var lookups atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer upstream.Close()
	server := newProxy(t, upstream.URL, upstream.URL, func(context.Context) (string, error) { lookups.Add(1); return "token", nil })
	for _, path := range []string{"/other-project/apis/run", "/projects/project/resources", "/project/documents", "/project/apis/../resources", "/project/transforms/run/delete", "/project/apis/run/extra", "/api/v2/artifacts/agents/delete_conversation", "/api/v2/artifacts/agents/run_agent/extra", "/api/v2/artifacts/agents/%72un_agent"} {
		resp, err := http.Post(server.URL+ProxyPath+path, "application/json", strings.NewReader(`{"project_id":"project"}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Fatal("unrelated route accepted", path, resp.StatusCode)
		}
	}
	resp, err := http.Get(server.URL + ProxyPath + "/project/apis/run")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatal("unexpected method accepted")
	}
	if calls.Load() != 0 || lookups.Load() != 0 {
		t.Fatal("unrelated route obtained credentials")
	}
	for _, path := range []string{"/api/v2/artifacts/resources/list_resources", "/api/v2/projects/project/apis/list_notes", "/api/v2/artifacts/functions/start_transform_function", "/api/v2/execution/jobs/get_job", "/api/v2/artifacts/agents/run_agent", "/api/v2/artifacts/agents/respond_to_tool_call", "/api/v2/artifacts/agents/list_conversations", "/api/v2/artifacts/agents/get_conversation", "/api/v2/artifacts/agents/list_messages", "/api/v2/artifacts/agents/create_conversation", "/api/v2/artifacts/agents/edit_conversation"} {
		resp, err := http.Post(server.URL+ProxyPath+path, "application/json", strings.NewReader(`{"project_id":"project"}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatal("SDK route rejected", path)
		}
	}
	if calls.Load() != 11 {
		t.Fatal("SDK routes were not forwarded")
	}
}

func TestProxyValidatesProjectBeforeLoadingCredentials(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("invalid request forwarded") }))
	defer upstream.Close()
	server := newProxy(t, upstream.URL, upstream.URL, func(context.Context) (string, error) { calls.Add(1); return "token", nil })
	cases := []struct{ body, contentType, query string }{
		{`{"project_id":"other"}`, "application/json", ""},
		{`{"inputs":{}}`, "application/json", ""},
		{`{"project_id":3}`, "application/json", ""},
		{`{"project_id":"project"} extra`, "application/json", ""},
		{`[]`, "application/json", ""},
		{`{"project_id":"project","project_id":"other"}`, "application/json", ""},
		{`{"project_id":"project","data":"` + strings.Repeat("x", 8<<20) + `"}`, "application/json", ""},
		{`{"project_id":"project"}`, "text/plain", ""},
		{`{"project_id":"project"}`, "application/json", "?project_id=other"},
		{`project_id=project`, "application/x-www-form-urlencoded", ""},
		{"broken", "multipart/form-data; boundary=example", ""},
	}
	for _, tc := range cases {
		resp, err := http.Post(server.URL+ProxyPath+"/api/v2/artifacts/agents/run_agent"+tc.query, tc.contentType, strings.NewReader(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("accepted invalid request (%s): %d", tc.contentType, resp.StatusCode)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("loaded credentials for invalid request")
	}
}

func TestProxyMultipartPreservesBodyAndValidatesProject(t *testing.T) {
	for _, projects := range [][]string{{"project"}, {"other"}, {"project", "other"}, {}} {
		t.Run(strings.Join(projects, "-"), func(t *testing.T) {
			var body bytes.Buffer
			form := multipart.NewWriter(&body)
			for _, project := range projects {
				if err := form.WriteField("project_id", project); err != nil {
					t.Fatal(err)
				}
			}
			if err := form.WriteField("inputs", `{"ids":[1,2]}`); err != nil {
				t.Fatal(err)
			}
			if err := form.Close(); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				forwarded, _ := io.ReadAll(r.Body)
				if !bytes.Equal(forwarded, body.Bytes()) || r.Header.Get("Content-Type") != form.FormDataContentType() {
					t.Error("multipart payload changed")
				}
			}))
			defer upstream.Close()
			valid := len(projects) == 1 && projects[0] == "project"
			server := newProxy(t, upstream.URL, upstream.URL, func(context.Context) (string, error) {
				if !valid {
					t.Error("invalid multipart loaded credentials")
				}
				return "token", nil
			})
			resp, err := http.Post(server.URL+ProxyPath+"/api/v2/projects/project/apis/list_notes", form.FormDataContentType(), bytes.NewReader(body.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			expected := http.StatusBadRequest
			if valid {
				expected = http.StatusOK
			}
			if resp.StatusCode != expected {
				t.Fatalf("status %d, expected %d", resp.StatusCode, expected)
			}
			if (calls.Load() == 1) != valid {
				t.Fatal("incorrect forwarding")
			}
		})
	}
}

func TestAgentStreamDisconnectCancelsUpstream(t *testing.T) {
	canceled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: token\ndata: {\"text\":\"hello\"}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(canceled)
	}))
	defer upstream.Close()
	server := newProxy(t, upstream.URL, upstream.URL, func(context.Context) (string, error) { return "token", nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", server.URL+ProxyPath+"/api/v2/artifacts/agents/run_agent", strings.NewReader(`{"project_id":"project"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if _, err := bufio.NewReader(resp.Body).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("browser disconnect did not cancel upstream request")
	}
}

func TestDevPortFallsBackOnlyWhenRequested(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	port := occupied.Addr().(*net.TCPAddr).Port
	if _, err := listenDevPort(port, false); err == nil {
		t.Fatal("explicit occupied port must fail")
	}
	available, err := listenDevPort(port, true)
	if err != nil {
		t.Fatal(err)
	}
	defer available.Close()
	if available.Addr().(*net.TCPAddr).Port <= port {
		t.Fatal("did not advance to a higher port")
	}
	random, err := listenDevPort(0, true)
	if err != nil {
		t.Fatal(err)
	}
	random.Close()
}

func TestNamedAPIProxyProjectScope(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/v2/projects/project/apis/list_notes" {
			t.Error(r.URL.Path)
		}
		fmt.Fprint(w, `{}`)
	}))
	defer upstream.Close()
	server := newProxy(t, upstream.URL, upstream.URL, func(context.Context) (string, error) { return "token", nil })
	for _, project := range []string{"project", "other", "project%2Fother"} {
		req, _ := http.NewRequest("POST", server.URL+ProxyPath+"/api/v2/projects/"+project+"/apis/list_notes", strings.NewReader(`{"project_id":"project"}`))
		req.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		expected := 400
		if project == "project" {
			expected = 200
		}
		if response.StatusCode != expected {
			t.Fatalf("%s: got %d", project, response.StatusCode)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls: %d", calls.Load())
	}
}

func TestOldAPIProxyRouteRemoved(t *testing.T) {
	req := httptest.NewRequest("POST", ProxyPath+"/api/v2/artifacts/functions/run_api_function", nil)
	if allowedRoute(req) {
		t.Fatal("old API route is still allowed")
	}
}
