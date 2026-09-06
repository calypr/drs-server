package authentication

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"net"
	"net/rpc"
	"os"
	"path/filepath"
	"testing"

	conf "github.com/calypr/syfon/client/config"
	"github.com/calypr/syfon/client/request"
	"github.com/calypr/syfon/plugin"
	hplugin "github.com/hashicorp/go-plugin"
)

func TestBuiltInAuthenticationPlugins(t *testing.T) {
	t.Run("local auth without credentials", func(t *testing.T) {
		out, err := (&LocalAuthPlugin{}).Authenticate(context.Background(), &plugin.AuthenticationInput{})
		if err != nil || !out.Authenticated {
			t.Fatalf("expected unauthenticated local mode to allow without configured credentials: out=%+v err=%v", out, err)
		}
	})

	t.Run("local basic auth", func(t *testing.T) {
		p := &LocalAuthPlugin{BasicUser: "alice", BasicPass: "secret"}
		valid := base64.StdEncoding.EncodeToString([]byte("alice:secret"))
		out, err := p.Authenticate(context.Background(), &plugin.AuthenticationInput{AuthHeader: "Basic " + valid})
		if err != nil || !out.Authenticated || out.Subject != "alice" {
			t.Fatalf("expected valid basic credentials to authenticate: out=%+v err=%v", out, err)
		}
		out, err = p.Authenticate(context.Background(), &plugin.AuthenticationInput{AuthHeader: "Basic invalid"})
		if err != nil || out.Authenticated {
			t.Fatalf("expected invalid basic credentials to be denied: out=%+v err=%v", out, err)
		}
	})

	t.Run("gen3 auth", func(t *testing.T) {
		p := &Gen3AuthPlugin{}
		out, err := p.Authenticate(context.Background(), &plugin.AuthenticationInput{})
		if err != nil || out.Authenticated {
			t.Fatalf("expected gen3 auth without a header to be denied: out=%+v err=%v", out, err)
		}
		out, err = p.Authenticate(context.Background(), &plugin.AuthenticationInput{AuthHeader: "Bearer token"})
		if err != nil || !out.Authenticated {
			t.Fatalf("expected gen3 auth with a header to authenticate: out=%+v err=%v", out, err)
		}
	})
}

func TestLoadMockAuthConfigFromEnv(t *testing.T) {
	t.Setenv("DRS_AUTH_MOCK_ENABLED", "true")
	t.Setenv("DRS_AUTH_MOCK_RESOURCES", " /data_file, /programs/demo ")
	t.Setenv("DRS_AUTH_MOCK_METHODS", "read, create")
	t.Setenv("DRS_AUTH_MOCK_REQUIRE_AUTH_HEADER", "yes")

	got := LoadMockAuthConfigFromEnv()
	if !got.Enabled || !got.RequireAuthHeader {
		t.Fatalf("unexpected mock auth flags: %+v", got)
	}
	if len(got.Resources) != 2 || got.Resources[0] != "/data_file" || got.Resources[1] != "/programs/demo" {
		t.Fatalf("unexpected mock resources: %v", got.Resources)
	}
	if len(got.Methods) != 2 || got.Methods[0] != "read" || got.Methods[1] != "create" {
		t.Fatalf("unexpected mock methods: %v", got.Methods)
	}
}

func TestNewRuntimeSwallowsStartupErrors(t *testing.T) {
	missingPlugin := filepath.Join(t.TempDir(), "missing-plugin")
	missingCSV := filepath.Join(t.TempDir(), "missing-authz.csv")
	t.Setenv("SYFON_AUTHN_PLUGIN_PATH", missingPlugin)
	t.Setenv("SYFON_AUTHZ_PLUGIN_PATH", missingPlugin)
	t.Setenv("DRS_LOCAL_AUTHZ_CSV", missingCSV)
	t.Setenv("DRS_AUTH_MOCK_ENABLED", "false")

	runtime := NewRuntime(slog.Default(), "local", "user", "pass")
	if runtime.Authorization != nil {
		t.Fatalf("expected failed authorization plugin startup to be swallowed")
	}
	if runtime.Authentication == nil {
		t.Fatalf("expected local authentication fallback after failed plugin startup")
	}
	if runtime.LocalAuthzError == nil {
		t.Fatalf("expected local CSV startup error to remain available to request wiring")
	}
}

func TestLocalAuthzCSVAndClaims(t *testing.T) {
	path := t.TempDir() + "/authz.csv"
	contents := "username,password,methods,resource,organization,project\n" +
		"# ignored,,,,,\n" +
		"alice,secret,read|write,/programs/demo/projects/p1,,\n" +
		"bob,pw,read,,programs,project-2\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write csv: %v", err)
	}

	store, err := LoadLocalAuthzCSV(path)
	if err != nil {
		t.Fatalf("load csv: %v", err)
	}
	resources, privileges, ok := store.AuthzForSubject("alice")
	if !ok || len(resources) != 1 || resources[0] != "/organization/demo/project/p1" {
		t.Fatalf("unexpected alice authorization: resources=%v ok=%v", resources, ok)
	}
	for _, method := range []string{"read", "file_upload", "create", "update", "delete"} {
		if !privileges[resources[0]][method] {
			t.Fatalf("expected alice method %q in %+v", method, privileges)
		}
	}

	authHeader := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:secret"))
	out, err := store.authenticate(authHeader)
	if err != nil || !out.Authenticated || out.Subject != "alice" {
		t.Fatalf("expected alice authentication: out=%+v err=%v", out, err)
	}
	claimResources, claimPrivileges, ok := AuthorizationFromClaims(out.Claims)
	if !ok || len(claimResources) != 1 || !claimPrivileges[claimResources[0]]["read"] {
		t.Fatalf("expected authorization claims, got resources=%v privileges=%v ok=%v", claimResources, claimPrivileges, ok)
	}
	claimPrivileges[claimResources[0]]["read"] = false
	if !privileges[resources[0]]["read"] {
		t.Fatalf("expected authorization claims to be cloned")
	}

	bobResources, _, ok := store.AuthzForSubject(" bob ")
	if !ok || len(bobResources) != 1 || bobResources[0] != "/organization/programs/project/project-2" {
		t.Fatalf("unexpected bob authorization: resources=%v ok=%v", bobResources, ok)
	}
	denied, err := store.authenticate("Basic " + base64.StdEncoding.EncodeToString([]byte("alice:wrong")))
	if err != nil || denied.Authenticated {
		t.Fatalf("expected wrong password to be denied: out=%+v err=%v", denied, err)
	}
	if _, _, ok := store.AuthzForSubject("missing"); ok {
		t.Fatalf("did not expect unknown subject to have authorization")
	}
}

func TestTokenHelpersAndResolver(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("user:token"))
	for _, tc := range []struct {
		name   string
		header string
		want   string
	}{
		{name: "bearer", header: "Bearer abc", want: "abc"},
		{name: "basic", header: "Basic " + encoded, want: "token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractBearerLikeToken(tc.header)
			if err != nil || got != tc.want {
				t.Fatalf("got token=%q err=%v, want %q", got, err, tc.want)
			}
		})
	}
	if _, err := ExtractBearerLikeToken("Digest token"); err == nil {
		t.Fatalf("expected unsupported authorization scheme to fail")
	}

	resources, privileges := ExtractPrivileges(map[string]any{
		"/programs/demo": []any{
			map[string]any{"service": "drs", "method": "read"},
			map[string]any{"service": "arborist", "method": "create-descendant"},
		},
	})
	if len(resources) != 1 || !privileges[resources[0]]["read"] || !privileges[resources[0]]["arborist:create-descendant"] {
		t.Fatalf("unexpected extracted privileges: resources=%v privileges=%v", resources, privileges)
	}

	resolver := NewTokenAuthResolver(nil)
	if result := resolver.Resolve(context.Background(), "invalid"); !result.Negative {
		t.Fatalf("expected invalid token resolution to be negative: %+v", result)
	}

	fake := &fakeRequester{response: map[string]any{"authz": map[string]any{"/data": []any{map[string]any{"service": "drs", "method": "read"}}}}}
	got, err := fetchPrivileges(context.Background(), fake, &conf.Credential{})
	if err != nil || got["/data"] == nil {
		t.Fatalf("expected fetched privileges: got=%v err=%v", got, err)
	}
	fake.err = errors.New("request failed")
	if _, err := fetchPrivileges(context.Background(), fake, &conf.Credential{}); err == nil {
		t.Fatalf("expected requester error")
	}
}

type fakeRequester struct {
	response map[string]any
	err      error
}

func (f *fakeRequester) Do(_ context.Context, _ string, _ string, _ any, out any, _ ...request.RequestOption) error {
	if f.err != nil {
		return f.err
	}
	*out.(*map[string]any) = f.response
	return nil
}

type rpcTestService struct{}

func (*rpcTestService) Authenticate(in *plugin.AuthenticationInput, out *plugin.AuthenticationOutput) error {
	out.Authenticated = true
	out.Subject = in.RequestID
	return nil
}

func (*rpcTestService) Authorize(in *plugin.AuthorizationInput, out *plugin.AuthorizationOutput) error {
	out.Allow = in.Subject == "alice"
	return nil
}

func TestPluginRPCDelegation(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	server := rpc.NewServer()
	if err := server.RegisterName("Plugin", &rpcTestService{}); err != nil {
		t.Fatalf("register rpc service: %v", err)
	}
	go server.ServeConn(serverConn)
	client := rpc.NewClient(clientConn)
	t.Cleanup(func() {
		_ = client.Close()
		_ = serverConn.Close()
	})

	authnOut, err := (&authnRPC{client: client}).Authenticate(context.Background(), &plugin.AuthenticationInput{RequestID: "rid"})
	if err != nil || !authnOut.Authenticated || authnOut.Subject != "rid" {
		t.Fatalf("unexpected authn rpc output: out=%+v err=%v", authnOut, err)
	}
	authzOut, err := (&authzRPC{client: client}).Authorize(context.Background(), &plugin.AuthorizationInput{Subject: "alice"})
	if err != nil || !authzOut.Allow {
		t.Fatalf("unexpected authz rpc output: out=%+v err=%v", authzOut, err)
	}

	var authnPlugin hplugin.Plugin = &authnPluginRPC{}
	var authzPlugin hplugin.Plugin = &authzPluginRPC{}
	if authnPlugin == nil || authzPlugin == nil {
		t.Fatal("expected RPC adapters to implement go-plugin Plugin")
	}
}

var _ plugin.AuthenticationPlugin = (*AuthenticationPluginManager)(nil)
var _ plugin.AuthorizationPlugin = (*AuthorizationPluginManager)(nil)
