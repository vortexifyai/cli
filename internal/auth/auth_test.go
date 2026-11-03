package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPKCE(t *testing.T) {
	if Challenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk") != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatal("RFC challenge mismatch")
	}
	a, _ := GenerateVerifier()
	b, _ := GenerateVerifier()
	if len(a) != 43 || a == b {
		t.Fatal("verifier entropy")
	}
}
func TestCredentialsAndTokenOverride(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := &Credentials{AccessToken: "access", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	if err := SaveCredentials(c); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadCredentials()
	if err != nil || *loaded != *c {
		t.Fatal(err)
	}
	path, _ := credentialsPath()
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("credential permissions")
	}
	dir, _ := os.Stat(filepath.Dir(path))
	if dir.Mode().Perm() != 0700 {
		t.Fatal("directory permissions")
	}
	t.Setenv("VORTEXIFY_TOKEN", "override")
	token, err := GetAccessToken()
	if err != nil || token != "override" {
		t.Fatal("token override")
	}
}
func TestLoginPKCECallback(t *testing.T) {
	var challenge string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("code") != "code" || Challenge(r.Form.Get("code_verifier")) != challenge || r.Form.Get("client_id") != "client" {
			t.Error("bad exchange")
		}
		if r.Form.Has("client_secret") {
			t.Error("public CLI must not send a client secret")
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "refresh_token": "refresh", "expires_in": 3600})
	}))
	defer server.Close()
	credentials, err := Login(context.Background(), LoginOptions{LoginURL: server.URL + "/login", TokenURL: server.URL + "/token", ClientID: "client", OpenBrowser: func(link string) error {
		u, _ := url.Parse(link)
		q := u.Query()
		challenge = q.Get("code_challenge")
		if q.Get("scope") != "profile email offline_access" || q.Get("code_challenge_method") != "S256" {
			return fmt.Errorf("missing refresh scopes or S256 PKCE")
		}
		callback := q.Get("redirect_uri")
		callbackURL, err := url.Parse(callback)
		if err != nil {
			return err
		}
		host, port, err := net.SplitHostPort(callbackURL.Host)
		if err != nil || host != "127.0.0.1" || port == "" || port == "0" || callbackURL.Path != "/callback" {
			return fmt.Errorf("callback must use an assigned loopback port: %s", callback)
		}
		resp, err := http.Get(callback + "?code=wrong&state=wrong")
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != 400 {
			return fmt.Errorf("accepted invalid state")
		}
		resp, err = http.Get(callback + "?code=code&state=" + url.QueryEscape(q.Get("state")))
		if err == nil {
			resp.Body.Close()
		}
		return err
	}})
	if err != nil || credentials.AccessToken != "access" {
		t.Fatalf("login %v %#v", err, credentials)
	}
}
func TestLoginCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Login(ctx, LoginOptions{LoginURL: "http://localhost/login", TokenURL: "http://localhost/token", ClientID: "client", OpenBrowser: func(string) error { return nil }}); err == nil {
		t.Fatal("login ignored cancellation")
	}
}

func TestRefreshUsesSavedClientSettings(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("VORTEXIFY_TOKEN", "")
	t.Setenv("VORTEXIFY_TOKEN_URL", "https://wrong-environment.example/token")
	t.Setenv("VORTEXIFY_CLIENT_ID", "wrong-environment-client")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "refresh" || r.Form.Get("client_id") != "client" {
			t.Error("invalid refresh request")
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": "new-access", "expires_in": 3600})
	}))
	defer server.Close()
	old := &Credentials{AccessToken: "old", RefreshToken: "refresh", ExpiresAt: time.Now().Add(-time.Minute).Unix(), TokenURL: server.URL, ClientID: "client"}
	if err := SaveCredentials(old); err != nil {
		t.Fatal(err)
	}
	token, err := GetAccessToken()
	if err != nil || token != "new-access" {
		t.Fatalf("refresh %q %v", token, err)
	}
	saved, err := LoadCredentials()
	if err != nil || saved.RefreshToken != "refresh" || saved.TokenURL != server.URL || saved.ClientID != "client" {
		t.Fatalf("lost refresh settings %#v %v", saved, err)
	}
}
