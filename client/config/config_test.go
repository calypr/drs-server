package config

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func testManager() *Manager {
	return &Manager{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func signedToken(t *testing.T, exp, iat time.Time) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"exp": exp.Unix(),
		"iat": iat.Unix(),
	})
	encoded, err := tok.SignedString([]byte("secret"))
	if err != nil {
		t.Fatalf("SignedString returned error: %v", err)
	}
	return encoded
}

func TestManagerEnsureExistsSaveLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	man := testManager()

	if err := man.EnsureExists(); err != nil {
		t.Fatalf("EnsureExists returned error: %v", err)
	}

	configPath := filepath.Join(home, ".gen3", "gen3_client_config.ini")
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("expected config file to exist: %v", err)
	}

	want := &Credential{
		Profile:            "default",
		KeyID:              "kid",
		APIKey:             "apikey",
		AccessToken:        "token",
		APIEndpoint:        "https://example.org",
		UseShepherd:        "true",
		MinShepherdVersion: "2.0.0",
		Bucket:             "bucket-a",
		ProjectID:          "proj-1",
	}
	if err := man.Save(want); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	got, err := man.Load("default")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if *got != *want {
		t.Fatalf("unexpected credential: got %+v want %+v", *got, *want)
	}
}

func TestManagerSaveClearsEmptyCredentialKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	man := testManager()

	if err := man.EnsureExists(); err != nil {
		t.Fatalf("EnsureExists returned error: %v", err)
	}

	initial := &Credential{
		Profile:            "origin",
		KeyID:              "kid",
		APIKey:             "apikey",
		AccessToken:        "token",
		APIEndpoint:        "https://example.org",
		UseShepherd:        "false",
		MinShepherdVersion: "",
	}
	if err := man.Save(initial); err != nil {
		t.Fatalf("initial Save returned error: %v", err)
	}

	tokenOnly := &Credential{
		Profile:            "origin",
		KeyID:              "",
		APIKey:             "",
		AccessToken:        "replacement-token",
		APIEndpoint:        "https://example.org",
		UseShepherd:        "false",
		MinShepherdVersion: "",
	}
	if err := man.Save(tokenOnly); err != nil {
		t.Fatalf("token-only Save returned error: %v", err)
	}

	configPath := filepath.Join(home, ".gen3", "gen3_client_config.ini")
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	text := string(content)
	if strings.Contains(text, "key_id") {
		t.Fatalf("expected key_id to be removed, got config:\n%s", text)
	}
	if strings.Contains(text, "api_key") {
		t.Fatalf("expected api_key to be removed, got config:\n%s", text)
	}
	if !strings.Contains(text, "replacement-token") {
		t.Fatalf("expected replacement access token, got config:\n%s", text)
	}

	got, err := man.Load("origin")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if got.KeyID != "" || got.APIKey != "" {
		t.Fatalf("expected cleared key material after token-only save, got %+v", *got)
	}
	if got.AccessToken != "replacement-token" {
		t.Fatalf("unexpected access token after token-only save: %+v", *got)
	}
}

func TestManagerLoadErrors(t *testing.T) {
	t.Run("missing config file", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		_, err := testManager().Load("missing")
		if err == nil || !strings.Contains(err.Error(), "Run configure command") {
			t.Fatalf("expected helpful missing config error, got %v", err)
		}
	})

	t.Run("missing profile", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		configDir := filepath.Join(home, ".gen3")
		if err := os.MkdirAll(configDir, 0o755); err != nil {
			t.Fatalf("MkdirAll returned error: %v", err)
		}
		configPath := filepath.Join(configDir, "gen3_client_config.ini")
		if err := os.WriteFile(configPath, []byte("[other]\napi_endpoint=https://example.org\nkey_id=k\n"), 0o644); err != nil {
			t.Fatalf("WriteFile returned error: %v", err)
		}

		_, err := testManager().Load("missing")
		if err == nil || !strings.Contains(err.Error(), "Need to run") {
			t.Fatalf("expected missing profile error, got %v", err)
		}
	})

	t.Run("missing credentials", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		configDir := filepath.Join(home, ".gen3")
		if err := os.MkdirAll(configDir, 0o755); err != nil {
			t.Fatalf("MkdirAll returned error: %v", err)
		}
		configPath := filepath.Join(configDir, "gen3_client_config.ini")
		if err := os.WriteFile(configPath, []byte("[default]\napi_endpoint=https://example.org\n"), 0o644); err != nil {
			t.Fatalf("WriteFile returned error: %v", err)
		}

		_, err := testManager().Load("default")
		if err == nil || !strings.Contains(err.Error(), "key_id, api_key and access_token") {
			t.Fatalf("expected credential error, got %v", err)
		}
	})

	t.Run("missing api endpoint", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		configDir := filepath.Join(home, ".gen3")
		if err := os.MkdirAll(configDir, 0o755); err != nil {
			t.Fatalf("MkdirAll returned error: %v", err)
		}
		configPath := filepath.Join(configDir, "gen3_client_config.ini")
		if err := os.WriteFile(configPath, []byte("[default]\nkey_id=k\n"), 0o644); err != nil {
			t.Fatalf("WriteFile returned error: %v", err)
		}

		_, err := testManager().Load("default")
		if err == nil || !strings.Contains(err.Error(), "api_endpoint not found") {
			t.Fatalf("expected api endpoint error, got %v", err)
		}
	})
}

func TestManagerImport(t *testing.T) {
	t.Run("imports credential file with normalized keys", func(t *testing.T) {
		credFile := filepath.Join(t.TempDir(), "cred.json")
		content := `{"key_id":"kid","api_key":"api","AccessToken":"tok","APIEndpoint":"https://example.org"}`
		if err := os.WriteFile(credFile, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile returned error: %v", err)
		}

		cred, err := testManager().Import(credFile, "")
		if err != nil {
			t.Fatalf("Import returned error: %v", err)
		}
		if cred.KeyID != "kid" || cred.APIKey != "api" || cred.AccessToken != "tok" {
			t.Fatalf("unexpected imported credential: %+v", *cred)
		}
	})

	t.Run("imports fence token", func(t *testing.T) {
		cred, err := testManager().Import("", "token-value")
		if err != nil {
			t.Fatalf("Import returned error: %v", err)
		}
		if cred.AccessToken != "token-value" {
			t.Fatalf("unexpected access token: %+v", *cred)
		}
	})

	t.Run("invalid inputs", func(t *testing.T) {
		if _, err := testManager().Import("", ""); err == nil {
			t.Fatal("expected error when neither credential file nor token is provided")
		}

		missing := filepath.Join(t.TempDir(), "missing.json")
		if _, err := testManager().Import(missing, ""); err == nil {
			t.Fatal("expected file read error for missing file")
		}

		bad := filepath.Join(t.TempDir(), "bad.json")
		if err := os.WriteFile(bad, []byte("not-json"), 0o644); err != nil {
			t.Fatalf("WriteFile returned error: %v", err)
		}
		if _, err := testManager().Import(bad, ""); err == nil || !strings.Contains(err.Error(), "cannot parse JSON credential file") {
			t.Fatalf("expected json parse error, got %v", err)
		}
	})
}



func TestManagerTokenAndCredentialValidation(t *testing.T) {

	man := testManager()
	now := time.Now().UTC()
	valid := signedToken(t, now.Add(24*time.Hour), now.Add(-time.Hour))
	soonExpiring := signedToken(t, now.Add(48*time.Hour), now.Add(-time.Hour))
	expired := signedToken(t, now.Add(-time.Hour), now.Add(-2*time.Hour))
	futureIAT := signedToken(t, now.Add(24*time.Hour), now.Add(time.Hour))

	t.Run("token validation branches", func(t *testing.T) {
		cases := []struct {
			name    string
			token   string
			wantOK  bool
			wantErr string
		}{
			{name: "valid", token: valid, wantOK: true},
			{name: "soon expiring still valid", token: soonExpiring, wantOK: true},
			{name: "empty", token: "", wantErr: "token is empty"},
			{name: "bad format", token: "not.a.jwt", wantErr: "invalid token format"},
			{name: "expired", token: expired, wantErr: "token expired"},
			{name: "future iat", token: futureIAT, wantErr: "token not yet valid"},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				ok, err := man.IsTokenValid(tc.token)
				if ok != tc.wantOK {
					t.Fatalf("IsTokenValid(%q) ok=%v want %v", tc.name, ok, tc.wantOK)
				}
				if tc.wantErr == "" {
					if err != nil {
						t.Fatalf("unexpected error: %v", err)
					}
					return
				}
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}
			})
		}
	})

	t.Run("missing exp claim", func(t *testing.T) {
		tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"iat": now.Add(-time.Hour).Unix()})
		encoded, err := tok.SignedString([]byte("secret"))
		if err != nil {
			t.Fatalf("SignedString returned error: %v", err)
		}
		if ok, err := man.IsTokenValid(encoded); ok || err == nil || !strings.Contains(err.Error(), "'exp' claim") {
			t.Fatalf("expected missing exp error, got ok=%v err=%v", ok, err)
		}
	})

	t.Run("missing iat claim", func(t *testing.T) {
		tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"exp": now.Add(time.Hour).Unix()})
		encoded, err := tok.SignedString([]byte("secret"))
		if err != nil {
			t.Fatalf("SignedString returned error: %v", err)
		}
		if ok, err := man.IsTokenValid(encoded); ok || err == nil || !strings.Contains(err.Error(), "'iat' claim") {
			t.Fatalf("expected missing iat error, got ok=%v err=%v", ok, err)
		}
	})

	t.Run("credential validation", func(t *testing.T) {
		ok, err := man.IsCredentialValid(&Credential{AccessToken: valid, APIKey: valid})
		if !ok || err != nil {
			t.Fatalf("expected valid credential, got ok=%v err=%v", ok, err)
		}

		if ok, err := man.IsCredentialValid(nil); ok || err == nil || !strings.Contains(err.Error(), "profileConfig is nil") {
			t.Fatalf("expected nil profile error, got ok=%v err=%v", ok, err)
		}

		if ok, err := man.IsCredentialValid(&Credential{AccessToken: expired, APIKey: valid}); ok || err == nil || !strings.Contains(err.Error(), "access_token is invalid but api_key is valid") {
			t.Fatalf("expected mixed validity error, got ok=%v err=%v", ok, err)
		}

		if ok, err := man.IsCredentialValid(&Credential{AccessToken: expired, APIKey: expired}); ok || err == nil || !strings.Contains(err.Error(), "both access_token and api_key are invalid") {
			t.Fatalf("expected both invalid error, got ok=%v err=%v", ok, err)
		}
	})

	t.Run("legacy IsValid checks api key", func(t *testing.T) {
		ok, err := man.IsValid(&Credential{APIKey: valid})
		if !ok || err != nil {
			t.Fatalf("expected IsValid success, got ok=%v err=%v", ok, err)
		}
	})
}
