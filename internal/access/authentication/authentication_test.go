package authentication

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"net"
	"net/rpc"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	conf "github.com/calypr/syfon/client/config"
	"github.com/calypr/syfon/client/request"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/plugin"
	hplugin "github.com/hashicorp/go-plugin"
)

func TestBuiltInAuthenticationPlugins(t *testing.T) {
	t.Run("local auth without credentials", func(t *testing.T) {
		out, err := (&localAuthPlugin{}).Authenticate(context.Background(), &plugin.AuthenticationInput{})
		if err != nil || !out.Authenticated {
			t.Fatalf("expected unauthenticated local mode to allow without configured credentials: out=%+v err=%v", out, err)
		}
	})

	t.Run("local basic auth", func(t *testing.T) {
		p := &localAuthPlugin{BasicUser: "alice", BasicPass: "secret"}
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
		p := &gen3AuthPlugin{}
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

	got := loadMockAuthConfigFromEnv()
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
	if runtime.authorization != nil {
		t.Fatalf("expected failed authorization plugin startup to be swallowed")
	}
	if runtime.authentication == nil {
		t.Fatalf("expected local authentication fallback after failed plugin startup")
	}
	if runtime.localAuthzError == nil {
		t.Fatalf("expected local CSV startup error to remain available to request wiring")
	}
}

type recordingAuthenticationPlugin struct {
	input  *plugin.AuthenticationInput
	output *plugin.AuthenticationOutput
	err    error
}

func (p *recordingAuthenticationPlugin) Authenticate(_ context.Context, input *plugin.AuthenticationInput) (*plugin.AuthenticationOutput, error) {
	p.input = input
	if p.output != nil || p.err != nil {
		return p.output, p.err
	}
	return &plugin.AuthenticationOutput{Authenticated: true, Subject: "alice"}, nil
}

type recordingAuthorizationPlugin struct {
	input  *plugin.AuthorizationInput
	output *plugin.AuthorizationOutput
	err    error
}

func (p *recordingAuthorizationPlugin) Authorize(_ context.Context, input *plugin.AuthorizationInput) (*plugin.AuthorizationOutput, error) {
	p.input = input
	if p.output != nil || p.err != nil {
		return p.output, p.err
	}
	return &plugin.AuthorizationOutput{Allow: true}, nil
}

func TestRuntimeEvaluatorPassesRequestIDToPlugins(t *testing.T) {
	authn := &recordingAuthenticationPlugin{}
	authz := &recordingAuthorizationPlugin{}
	runtime := &Runtime{
		logger:         slog.Default(),
		authentication: authn,
		authorization:  authz,
		tokenResolver:  newTokenAuthResolver(slog.Default()),
	}

	result := runtime.Evaluate(EvaluationRequest{
		Context:    context.Background(),
		RequestID:  "request-id",
		Mode:       "gen3",
		AuthHeader: "Bearer malformed",
		Method:     "GET",
		Path:       "/objects/object-id",
	})
	if result.Decision != DecisionContinue {
		t.Fatalf("expected evaluator to continue, got %v", result.Decision)
	}
	if authn.input == nil || authn.input.RequestID != "request-id" {
		t.Fatalf("expected request ID on authentication input: %+v", authn.input)
	}
	if authz.input == nil || authz.input.RequestID != "request-id" {
		t.Fatalf("expected request ID on authorization input: %+v", authz.input)
	}
}

func TestRuntimeEvaluatorLocalDecisions(t *testing.T) {
	t.Run("missing csv is an internal error", func(t *testing.T) {
		runtime := &Runtime{localAuthzError: errors.New("csv failed")}
		result := runtime.Evaluate(EvaluationRequest{Mode: "local"})
		if result.Decision != DecisionInternalError {
			t.Fatalf("expected internal error, got %v", result.Decision)
		}
	})

	t.Run("failed authentication requests basic challenge", func(t *testing.T) {
		runtime := &Runtime{
			authentication: &recordingAuthenticationPlugin{
				output: &plugin.AuthenticationOutput{Authenticated: false},
			},
		}
		result := runtime.Evaluate(EvaluationRequest{Mode: "local"})
		if result.Decision != DecisionUnauthorized || !result.BasicChallenge {
			t.Fatalf("expected challenged unauthorized result, got %+v", result)
		}
	})

	t.Run("csv subject authorization is evaluated", func(t *testing.T) {
		runtime := &Runtime{
			authentication: &recordingAuthenticationPlugin{},
			localAuthzForSubject: func(subject string) ([]string, map[string]map[string]bool, bool) {
				if subject != "alice" {
					t.Fatalf("unexpected subject %q", subject)
				}
				return []string{"/data"}, map[string]map[string]bool{"/data": {"read": true}}, true
			},
		}
		result := runtime.Evaluate(EvaluationRequest{Mode: "local"})
		if result.Decision != DecisionContinue || result.Session.Source != access.SourceLocalCSV || len(result.Session.Resources) != 1 {
			t.Fatalf("expected csv authorization, got %+v", result)
		}
	})

	t.Run("unknown csv subject is forbidden", func(t *testing.T) {
		runtime := &Runtime{
			authentication: &recordingAuthenticationPlugin{},
			localAuthzForSubject: func(string) ([]string, map[string]map[string]bool, bool) {
				return nil, nil, false
			},
		}
		result := runtime.Evaluate(EvaluationRequest{Mode: "local"})
		if result.Decision != DecisionForbidden {
			t.Fatalf("expected forbidden result, got %+v", result)
		}
	})
}

func TestRuntimeEvaluatorMockDecisions(t *testing.T) {
	runtime := &Runtime{mock: mockConfig{
		Enabled:           true,
		RequireAuthHeader: true,
		Resources:         []string{"/data"},
		Methods:           []string{"read"},
	}}
	withoutHeader := runtime.Evaluate(EvaluationRequest{Mode: "gen3"})
	if withoutHeader.Decision != DecisionContinue || len(withoutHeader.Session.Resources) != 0 {
		t.Fatalf("expected unauthenticated mock request to continue without privileges: %+v", withoutHeader)
	}

	runtime.mock.RequireAuthHeader = false
	withMock := runtime.Evaluate(EvaluationRequest{Mode: "gen3"})
	if withMock.Decision != DecisionContinue || withMock.Session.Source != access.SourceGen3Mock || !withMock.Session.Privileges["/data"]["read"] {
		t.Fatalf("expected mock privileges, got %+v", withMock)
	}
}

func TestRuntimeEvaluatorAuthorizationDecisions(t *testing.T) {
	for name, authz := range map[string]*recordingAuthorizationPlugin{
		"authorization error":  {err: errors.New("authorization failed")},
		"authorization denied": {output: &plugin.AuthorizationOutput{Allow: false}},
	} {
		t.Run(name, func(t *testing.T) {
			runtime := &Runtime{
				logger:         slog.Default(),
				authentication: &recordingAuthenticationPlugin{},
				authorization:  authz,
			}
			result := runtime.Evaluate(EvaluationRequest{
				Context:    context.Background(),
				Mode:       "gen3",
				AuthHeader: "Bearer token",
			})
			want := DecisionUnauthorized
			if name == "authorization denied" {
				want = DecisionForbidden
			}
			if result.Decision != want {
				t.Fatalf("expected %v, got %+v", want, result)
			}
		})
	}
}

type nilOutputAuthenticationPlugin struct{}

func (*nilOutputAuthenticationPlugin) Authenticate(context.Context, *plugin.AuthenticationInput) (*plugin.AuthenticationOutput, error) {
	return nil, nil
}

func TestRuntimeNilPluginOutputPreservesPanic(t *testing.T) {
	if os.Getenv("SYFON_NIL_AUTH_OUTPUT_CHILD") == "1" {
		runtime := &Runtime{logger: slog.Default(), authentication: &nilOutputAuthenticationPlugin{}}
		runtime.Evaluate(EvaluationRequest{
			Context:    context.Background(),
			Mode:       "gen3",
			AuthHeader: "Bearer token",
		})
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run", "^TestRuntimeNilPluginOutputPreservesPanic$", "-test.v")
	cmd.Env = append(os.Environ(), "SYFON_NIL_AUTH_OUTPUT_CHILD=1")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected nil plugin output to preserve the existing panic")
	}
	if !bytes.Contains(output, []byte("panic")) {
		t.Fatalf("expected child process panic, got: %s", output)
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

	store, err := loadLocalAuthzCSV(path)
	if err != nil {
		t.Fatalf("load csv: %v", err)
	}
	resources, privileges, ok := store.authzForSubject("alice")
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
	claimResources, claimPrivileges, ok := authorizationFromClaims(out.Claims)
	if !ok || len(claimResources) != 1 || !claimPrivileges[claimResources[0]]["read"] {
		t.Fatalf("expected authorization claims, got resources=%v privileges=%v ok=%v", claimResources, claimPrivileges, ok)
	}
	claimPrivileges[claimResources[0]]["read"] = false
	if !privileges[resources[0]]["read"] {
		t.Fatalf("expected authorization claims to be cloned")
	}

	bobResources, _, ok := store.authzForSubject(" bob ")
	if !ok || len(bobResources) != 1 || bobResources[0] != "/organization/programs/project/project-2" {
		t.Fatalf("unexpected bob authorization: resources=%v ok=%v", bobResources, ok)
	}
	denied, err := store.authenticate("Basic " + base64.StdEncoding.EncodeToString([]byte("alice:wrong")))
	if err != nil || denied.Authenticated {
		t.Fatalf("expected wrong password to be denied: out=%+v err=%v", denied, err)
	}
	if _, _, ok := store.authzForSubject("missing"); ok {
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
			got, err := extractBearerLikeToken(tc.header)
			if err != nil || got != tc.want {
				t.Fatalf("got token=%q err=%v, want %q", got, err, tc.want)
			}
		})
	}
	if _, err := extractBearerLikeToken("Digest token"); err == nil {
		t.Fatalf("expected unsupported authorization scheme to fail")
	}

	resources, privileges := extractPrivileges(map[string]any{
		"/programs/demo": []any{
			map[string]any{"service": "drs", "method": "read"},
			map[string]any{"service": "arborist", "method": "create-descendant"},
		},
	})
	if len(resources) != 1 || !privileges[resources[0]]["read"] || !privileges[resources[0]]["arborist:create-descendant"] {
		t.Fatalf("unexpected extracted privileges: resources=%v privileges=%v", resources, privileges)
	}

	resolver := newTokenAuthResolver(nil)
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

var _ plugin.AuthenticationPlugin = (*authenticationPluginManager)(nil)
var _ plugin.AuthorizationPlugin = (*authorizationPluginManager)(nil)
