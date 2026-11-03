package cmd

import (
	"strings"
	"testing"
)

func TestLoginEnvironmentPresets(t *testing.T) {
	for _, test := range []struct {
		name  string
		flags []string
		url   string
	}{
		{"production", nil, "https://dashboard.vortexify.ai/login/oauth"},
		{"dev", []string{"--dev"}, "https://vortexify.dev/login/oauth"},
		{"local", []string{"--dev-local"}, "http://localhost:5173/login/oauth"},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, key := range []string{"VORTEXIFY_LOGIN_URL", "VORTEXIFY_CLIENT_ID", "VORTEXIFY_TOKEN_URL"} {
				t.Setenv(key, "")
			}
			cmd := newLoginCommand()
			if err := cmd.ParseFlags(test.flags); err != nil {
				t.Fatal(err)
			}
			dev, _ := cmd.Flags().GetBool("dev")
			local, _ := cmd.Flags().GetBool("dev-local")
			options := loginOptions(cmd, dev, local)
			if options.LoginURL != test.url {
				t.Fatal(options.LoginURL)
			}
			if dev || local {
				if options.ClientID != devOAuthClientID || options.TokenURL != devOAuthTokenURL {
					t.Fatal("missing dev OAuth defaults")
				}
			} else if options.ClientID != "r0z5faTL2FZJn58I" || options.TokenURL != "https://clerk.vortexify.ai/oauth/token" {
				t.Fatal("missing production OAuth defaults")
			}
		})
	}
}

func TestLoginPresetOverridesAndValidation(t *testing.T) {
	t.Setenv("VORTEXIFY_LOGIN_URL", "https://old.example/login")
	t.Setenv("VORTEXIFY_CLIENT_ID", "old-client")
	t.Setenv("VORTEXIFY_TOKEN_URL", "https://old.example/token")
	cmd := newLoginCommand()
	_ = cmd.ParseFlags([]string{"--dev-local"})
	options := loginOptions(cmd, false, true)
	if options.ClientID != devOAuthClientID || options.LoginURL != "http://localhost:5173/login/oauth" || options.TokenURL != devOAuthTokenURL {
		t.Fatal("preset inherited legacy environment")
	}
	_ = cmd.ParseFlags([]string{"--login-url", "http://localhost:5174/login/oauth"})
	if loginOptions(cmd, false, true).LoginURL != "http://localhost:5174/login/oauth" {
		t.Fatal("explicit override ignored")
	}
	_, err := run(t, "login", "--dev", "--dev-local")
	if err == nil || !strings.Contains(err.Error(), "were all set") {
		t.Fatal(err)
	}
	for _, key := range []string{"VORTEXIFY_LOGIN_URL", "VORTEXIFY_CLIENT_ID", "VORTEXIFY_TOKEN_URL"} {
		t.Setenv(key, "")
	}
	_, err = run(t, "login", "--client-id", "", "--token-url", "")
	if err == nil || !strings.Contains(err.Error(), "OAuth client ID and token URL are required") {
		t.Fatal(err)
	}
}

func TestProductionLoginOverrides(t *testing.T) {
	t.Setenv("VORTEXIFY_LOGIN_URL", "https://custom.example/login")
	t.Setenv("VORTEXIFY_CLIENT_ID", "custom-client")
	t.Setenv("VORTEXIFY_TOKEN_URL", "https://custom.example/token")
	cmd := newLoginCommand()
	options := loginOptions(cmd, false, false)
	if options.LoginURL != "https://custom.example/login" || options.ClientID != "custom-client" || options.TokenURL != "https://custom.example/token" {
		t.Fatal("production environment overrides ignored")
	}
	if err := cmd.ParseFlags([]string{"--client-id", "explicit-client"}); err != nil {
		t.Fatal(err)
	}
	if loginOptions(cmd, false, false).ClientID != "explicit-client" {
		t.Fatal("explicit flag did not override environment")
	}
}
