package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"vortexify-cli/internal/storage"
)

type Credentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
	TokenURL     string `json:"token_url,omitempty"`
	ClientID     string `json:"client_id,omitempty"`
}

func credentialsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".vortexify", "credentials.json"), nil
}
func LoadCredentials() (*Credentials, error) {
	path, err := credentialsPath()
	if err != nil {
		return nil, err
	}
	if err = storage.CheckFilePath(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load credentials (run vortexify login): %w", err)
	}
	var c Credentials
	err = json.Unmarshal(data, &c)
	return &c, err
}
func SaveCredentials(c *Credentials) error {
	if c.AccessToken == "" {
		return fmt.Errorf("empty access token")
	}
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	if err = storage.CheckFilePath(path); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err = os.Chmod(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return storage.WriteJSON(path, c, 0600)
}
func GetAccessToken() (string, error) { return GetAccessTokenContext(context.Background()) }
func GetAccessTokenContext(ctx context.Context) (string, error) {
	if token := os.Getenv("VORTEXIFY_TOKEN"); token != "" {
		return token, nil
	}
	c, err := LoadCredentials()
	if err != nil {
		return "", err
	}
	if c.ExpiresAt > 0 && time.Now().Unix()+30 >= c.ExpiresAt {
		if c.RefreshToken == "" {
			return "", fmt.Errorf("credentials expired; run vortexify login")
		}
		endpoint := c.TokenURL
		clientID := c.ClientID
		if endpoint == "" {
			endpoint = os.Getenv("VORTEXIFY_TOKEN_URL")
		}
		if clientID == "" {
			clientID = os.Getenv("VORTEXIFY_CLIENT_ID")
		}
		if endpoint == "" || clientID == "" {
			return "", fmt.Errorf("refresh requires VORTEXIFY_TOKEN_URL and VORTEXIFY_CLIENT_ID; run vortexify login")
		}
		refreshed, err := exchange(ctx, endpoint, clientID, map[string]string{"grant_type": "refresh_token", "refresh_token": c.RefreshToken})
		if err != nil {
			return "", err
		}
		if refreshed.RefreshToken == "" {
			refreshed.RefreshToken = c.RefreshToken
		}
		refreshed.TokenURL = endpoint
		refreshed.ClientID = clientID
		if err = SaveCredentials(refreshed); err != nil {
			return "", err
		}
		c = refreshed
	}
	if c.AccessToken == "" {
		return "", fmt.Errorf("missing access token; run vortexify login")
	}
	return c.AccessToken, nil
}
