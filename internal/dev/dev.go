package dev

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"vortexify-cli/internal/auth"
)

type Options struct {
	AppPath     string
	ProjectID   string
	OrgID       string
	AppID       string
	APIURL      string
	Port        int
	TryNextPort bool
	Output      io.Writer
	Errors      io.Writer
	Token       TokenSource
}

func listenDevPort(port int, tryNext bool) (net.Listener, error) {
	for {
		listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err == nil {
			return listener, nil
		}
		if !tryNext || port == 0 || port >= 65535 || !errors.Is(err, syscall.EADDRINUSE) {
			return nil, err
		}
		port++
	}
}

func Run(ctx context.Context, opts Options) error {
	if strings.TrimSpace(opts.ProjectID) == "" {
		return fmt.Errorf("project ID is required")
	}
	if opts.Port < 0 || opts.Port > 65535 {
		return fmt.Errorf("port must be between 0 and 65535")
	}
	if _, err := parseAPIURL(opts.APIURL); err != nil {
		return err
	}
	if err := checkApp(opts.AppPath); err != nil {
		return err
	}
	if opts.Output == nil {
		opts.Output = os.Stdout
	}
	if opts.Errors == nil {
		opts.Errors = os.Stderr
	}
	outputMutex := &sync.Mutex{}
	opts.Output = &lockedWriter{writer: opts.Output, mutex: outputMutex}
	opts.Errors = &lockedWriter{writer: opts.Errors, mutex: outputMutex}
	if opts.Token == nil {
		opts.Token = auth.GetAccessTokenContext
	}
	accessToken, err := opts.Token(ctx)
	if err != nil {
		return fmt.Errorf("development authentication: %w", err)
	}
	if strings.TrimSpace(accessToken) == "" {
		return fmt.Errorf("development authentication returned an empty token")
	}
	npm, err := exec.LookPath("npm")
	if err != nil {
		return fmt.Errorf("npm is required to start Vite: %w", err)
	}
	listener, err := listenDevPort(opts.Port, opts.TryNextPort)
	if err != nil {
		return err
	}
	defer listener.Close()
	origin := "http://" + listener.Addr().String()
	private, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	privatePort := private.Addr().(*net.TCPAddr).Port
	private.Close()
	viteURL := "http://127.0.0.1:" + strconv.Itoa(privatePort)
	handler, err := NewHandler(opts.APIURL, viteURL, origin, opts.ProjectID, opts.Token)
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, BaseContext: func(net.Listener) context.Context { return runCtx }}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(listener) }()
	defer func() {
		cancel()
		shutdownCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		_ = server.Shutdown(shutdownCtx)
		_ = server.Close()
	}()
	cmd := npmCommand(npm, privatePort)
	cmd.Dir = opts.AppPath
	cmd.Env = childEnvironment(os.Environ(), opts)
	cmd.Stdout = opts.Output
	cmd.Stderr = opts.Errors
	configureProcess(cmd)
	if err = cmd.Start(); err != nil {
		return fmt.Errorf("start Vite: %w", err)
	}
	childDone := make(chan error, 1)
	go func() { childDone <- cmd.Wait() }()
	exited := false
	defer func() {
		terminateProcess(cmd, false)
		if !exited {
			select {
			case <-childDone:
			case <-time.After(2 * time.Second):
				terminateProcess(cmd, true)
				<-childDone
			}
		}
	}()
	// Wait until Vite is listening before advertising the public URL.
	readyCtx, readyCancel := context.WithTimeout(runCtx, 30*time.Second)
	defer readyCancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	healthClient := &http.Client{Timeout: 500 * time.Millisecond}
ready:
	for {
		select {
		case <-readyCtx.Done():
			if runCtx.Err() != nil {
				return nil
			}
			return fmt.Errorf("waiting for Vite: %w", readyCtx.Err())
		case err = <-childDone:
			exited = true
			if runCtx.Err() != nil {
				return nil
			}
			if err == nil {
				return fmt.Errorf("dev script exited before Vite was ready")
			}
			return fmt.Errorf("Vite exited; check the dev script and run npm install if needed: %w", err)
		case <-ticker.C:
			req, _ := http.NewRequestWithContext(readyCtx, http.MethodGet, viteURL+"/@vite/client", nil)
			resp, requestErr := healthClient.Do(req)
			if requestErr == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					break ready
				}
			}
		}
	}
	ticker.Stop()
	readyCancel()
	fmt.Fprintf(opts.Output, "\nVortexify dev: %s\nApp: %s\nAPI proxy: %s%s → %s\nUse the Vortexify dev URL above. Press Ctrl+C to stop.\n", origin, opts.AppPath, origin, ProxyPath, opts.APIURL)
	select {
	case <-runCtx.Done():
		return nil
	case err = <-childDone:
		exited = true
		if runCtx.Err() != nil {
			return nil
		}
		if err != nil {
			return fmt.Errorf("Vite exited: %w", err)
		}
		return nil
	case err = <-serverDone:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}
func childEnvironment(environ []string, opts Options) []string {
	values := map[string]string{}
	for _, entry := range environ {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if strings.HasPrefix(key, "VORTEXIFY_") {
			continue
		}
		values[key] = value
	}
	values["NODE_ENV"] = "development"
	values["VITE_API_SERVER_URL"] = opts.APIURL
	values["VITE_VORTEXIFY_PROJECT_ID"] = opts.ProjectID
	if opts.OrgID != "" {
		values["VITE_VORTEXIFY_ORG_ID"] = opts.OrgID
	}
	values["VITE_VORTEXIFY_DEV_PROXY_PATH"] = ProxyPath
	values["VITE_VORTEXIFY_DEV_TOKEN"] = ""
	values["VITE_APP_MODE"] = "preview"
	if opts.AppID != "" {
		values["VITE_APP_ID"] = opts.AppID
	}
	keys := []string{}
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

type lockedWriter struct {
	writer io.Writer
	mutex  *sync.Mutex
}

func (w *lockedWriter) Write(data []byte) (int, error) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.writer.Write(data)
}
