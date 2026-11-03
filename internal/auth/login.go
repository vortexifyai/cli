package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
)

type LoginOptions struct {
	LoginURL    string
	TokenURL    string
	Scopes      string
	ClientID    string
	Output      io.Writer
	OpenBrowser func(string) error
}

func OpenBrowser(link string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", link)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", link)
	default:
		command = exec.Command("xdg-open", link)
	}
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
}
func validateEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Host == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"))) {
		return fmt.Errorf("endpoint must use HTTPS (HTTP allowed on localhost)")
	}
	return nil
}
func exchange(ctx context.Context, endpoint, clientID string, values map[string]string) (*Credentials, error) {
	if err := validateEndpoint(endpoint); err != nil {
		return nil, err
	}
	form := url.Values{"client_id": {clientID}}
	for k, v := range values {
		form.Set(k, v)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("token exchange failed (HTTP %d)", resp.StatusCode)
	}
	var token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		ExpiresAt    int64  `json:"expires_at"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&token); err != nil {
		return nil, err
	}
	if token.AccessToken == "" {
		return nil, fmt.Errorf("token exchange returned no access token")
	}
	expiry := token.ExpiresAt
	if expiry == 0 && token.ExpiresIn > 0 {
		expiry = time.Now().Unix() + token.ExpiresIn
	}
	if expiry == 0 {
		return nil, fmt.Errorf("token exchange returned no expiry")
	}
	return &Credentials{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, ExpiresAt: expiry}, nil
}
func Login(ctx context.Context, opts LoginOptions) (*Credentials, error) {
	if opts.ClientID == "" || opts.TokenURL == "" {
		return nil, fmt.Errorf("configure VORTEXIFY_CLIENT_ID and VORTEXIFY_TOKEN_URL for browser login")
	}
	if err := validateEndpoint(opts.LoginURL); err != nil {
		return nil, err
	}
	if err := validateEndpoint(opts.TokenURL); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	verifier, err := GenerateVerifier()
	if err != nil {
		listener.Close()
		return nil, err
	}
	state, err := GenerateState()
	if err != nil {
		listener.Close()
		return nil, err
	}
	redirect := "http://" + listener.Addr().String() + "/callback"
	type result struct {
		code string
		err  error
	}
	results := make(chan result, 1)
	var callbackTaken atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("state")), []byte(state)) != 1 {
			http.Error(w, "Invalid login state.", http.StatusBadRequest)
			return
		}
		value := result{code: r.URL.Query().Get("code")}
		if r.URL.Query().Get("error") != "" {
			value.err = fmt.Errorf("authorization was denied")
		}
		if value.code == "" && value.err == nil {
			http.Error(w, "Missing authorization code.", 400)
			return
		}
		if !callbackTaken.CompareAndSwap(false, true) {
			http.Error(w, "Callback already received.", http.StatusConflict)
			return
		}
		fmt.Fprintln(w, "You can return to the Vortexify CLI.")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		results <- value

	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			select {
			case results <- result{err: err}:
			default:
			}
		}
	}()
	u, _ := url.Parse(opts.LoginURL)
	q := u.Query()
	q.Set("response_type", "code")
	scopes := opts.Scopes
	if scopes == "" {
		scopes = "profile email offline_access"
	}
	q.Set("scope", scopes)
	q.Set("client_id", opts.ClientID)
	q.Set("redirect_uri", redirect)
	q.Set("state", state)
	q.Set("code_challenge", Challenge(verifier))
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()
	if opts.Output != nil {
		fmt.Fprintln(opts.Output, "Open this URL to log in:", u.String())
	}
	open := opts.OpenBrowser
	if open == nil {
		open = OpenBrowser
	}
	if err = open(u.String()); err != nil && opts.Output != nil {
		fmt.Fprintln(opts.Output, "Browser could not open automatically; use the URL above.")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-results:
		if result.err != nil {
			return nil, result.err
		}
		credentials, err := exchange(ctx, opts.TokenURL, opts.ClientID, map[string]string{"grant_type": "authorization_code", "code": result.code, "redirect_uri": redirect, "code_verifier": verifier})
		if err != nil {
			return nil, err
		}
		credentials.TokenURL = opts.TokenURL
		credentials.ClientID = opts.ClientID
		return credentials, nil
	}
}
