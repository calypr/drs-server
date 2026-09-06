package authentication

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/calypr/syfon/plugin"
)

type LocalAuthPlugin struct {
	BasicUser string
	BasicPass string
	Users     *LocalAuthzStore
}

func (p *LocalAuthPlugin) Authenticate(ctx context.Context, in *plugin.AuthenticationInput) (*plugin.AuthenticationOutput, error) {
	if p.Users != nil {
		return p.Users.authenticate(in.AuthHeader)
	}
	if p.BasicUser != "" || p.BasicPass != "" {
		err := validateBasicAuth(in.AuthHeader, p.BasicUser, p.BasicPass)
		if err != nil {
			return &plugin.AuthenticationOutput{Authenticated: false, Reason: err.Error()}, nil
		}
		claims := map[string]interface{}{"username": p.BasicUser}
		if strings.Contains(p.BasicUser, "@") {
			claims["email"] = p.BasicUser
		}
		return &plugin.AuthenticationOutput{Authenticated: true, Subject: p.BasicUser, Claims: claims}, nil
	}
	return &plugin.AuthenticationOutput{Authenticated: true}, nil
}

type Gen3AuthPlugin struct {
	MockConfig MockConfig
}

func (p *Gen3AuthPlugin) Authenticate(_ context.Context, in *plugin.AuthenticationInput) (*plugin.AuthenticationOutput, error) {
	if p.MockConfig.Enabled {
		return &plugin.AuthenticationOutput{Authenticated: true}, nil
	}
	if in.AuthHeader == "" {
		return &plugin.AuthenticationOutput{Authenticated: false, Reason: "missing auth header"}, nil
	}
	return &plugin.AuthenticationOutput{Authenticated: true}, nil
}

func validateBasicAuth(authHeader, expectedUser, expectedPass string) error {
	if authHeader == "" || !strings.HasPrefix(strings.ToLower(authHeader), "basic ") {
		return fmt.Errorf("missing basic auth header")
	}
	payload := authHeader[len("basic "):]
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return fmt.Errorf("decode basic auth header: %w", err)
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 ||
		subtle.ConstantTimeCompare([]byte(parts[0]), []byte(expectedUser)) != 1 ||
		subtle.ConstantTimeCompare([]byte(parts[1]), []byte(expectedPass)) != 1 {
		return fmt.Errorf("invalid basic auth credentials")
	}
	return nil
}
