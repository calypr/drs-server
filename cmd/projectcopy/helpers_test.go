package projectcopy

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/internalapi"
	syclient "github.com/calypr/syfon/client"
	"github.com/calypr/syfon/client/request"
	"github.com/calypr/syfon/client/services"
	"github.com/golang-jwt/jwt/v5"
	"github.com/spf13/cobra"
)

func TestParseScopeArg(t *testing.T) {
	scope, err := ParseScopeArg("program/project", "source")
	if err != nil {
		t.Fatalf("ParseScopeArg returned error: %v", err)
	}
	if scope.Organization != "program" || scope.Project != "project" {
		t.Fatalf("unexpected scope: %+v", scope)
	}

	if _, err := ParseScopeArg("program", "source"); err == nil || !strings.Contains(err.Error(), "must be in format <organization>/<project-id>") {
		t.Fatalf("expected malformed scope error, got %v", err)
	}
}

func TestClientOptionsFromInputsValidation(t *testing.T) {
	if _, err := clientOptionsFromInputs(context.Background(), "target", "", "tok", "user", "pass"); err == nil || !strings.Contains(err.Error(), "--target-token cannot be combined") {
		t.Fatalf("expected token/basic conflict, got %v", err)
	}
	if _, err := clientOptionsFromInputs(context.Background(), "target", "", "", "user", ""); err == nil || !strings.Contains(err.Error(), "--target-basic-user and --target-basic-password must be set together") {
		t.Fatalf("expected missing password error, got %v", err)
	}

	opts, err := clientOptionsFromInputs(context.Background(), "source", "", "", "alice", "secret")
	if err != nil {
		t.Fatalf("clientOptionsFromInputs returned error: %v", err)
	}
	cfg := syclient.DefaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.BasicAuth == nil || cfg.BasicAuth.Username != "alice" || cfg.BasicAuth.Password != "secret" {
		t.Fatalf("unexpected basic auth config: %+v", cfg.BasicAuth)
	}
}

func TestResolveServerURLs(t *testing.T) {
	validToken := signedToken(t, time.Now().Add(time.Hour))

	t.Run("source falls back to inherited root server", func(t *testing.T) {
		flags := &AuthFlags{}
		cmd := testCommand()
		got, err := flags.resolveSourceServerURL(context.Background(), cmd)
		if err != nil {
			t.Fatalf("resolveSourceServerURL returned error: %v", err)
		}
		if got != "http://127.0.0.1:8080" {
			t.Fatalf("unexpected root server fallback: %q", got)
		}
	})

	t.Run("source profile endpoint", func(t *testing.T) {
		flags := &AuthFlags{SourceProfile: "source"}
		home := t.TempDir()
		t.Setenv("HOME", home)
		writeProfileConfig(t, home, `[source]
access_token = `+validToken+`
api_key = `+validToken+`
api_endpoint = https://source.example/
`)
		cmd := testCommand()
		got, err := flags.resolveSourceServerURL(context.Background(), cmd)
		if err != nil {
			t.Fatalf("resolveSourceServerURL returned error: %v", err)
		}
		if got != "https://source.example" {
			t.Fatalf("unexpected source profile endpoint: %q", got)
		}
	})

	t.Run("target explicit server wins", func(t *testing.T) {
		flags := &AuthFlags{TargetServerURL: "https://explicit.example/"}
		cmd := testCommand()
		got, err := flags.resolveTargetServerURL(context.Background(), cmd, false)
		if err != nil {
			t.Fatalf("resolveTargetServerURL returned error: %v", err)
		}
		if got != "https://explicit.example" {
			t.Fatalf("unexpected explicit target server: %q", got)
		}
	})

	t.Run("target profile endpoint", func(t *testing.T) {
		flags := &AuthFlags{TargetProfile: "target"}
		home := t.TempDir()
		t.Setenv("HOME", home)
		writeProfileConfig(t, home, `[target]
access_token = `+validToken+`
api_key = `+validToken+`
api_endpoint = https://target.example/
`)
		cmd := testCommand()
		got, err := flags.resolveTargetServerURL(context.Background(), cmd, false)
		if err != nil {
			t.Fatalf("resolveTargetServerURL returned error: %v", err)
		}
		if got != "https://target.example" {
			t.Fatalf("unexpected target profile endpoint: %q", got)
		}
	})
}

func TestRecordsToCopy(t *testing.T) {
	client := testIndexService()

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	scope := Scope{Organization: "syfon", Project: "e2e"}
	records, err := RecordsToCopy(context.Background(), cmd, client, scope, "")
	if err != nil {
		t.Fatalf("RecordsToCopy returned error: %v", err)
	}
	if len(records) != 2 || records[0].Did != "did-1" || records[1].Did != "did-2" {
		t.Fatalf("unexpected project records: %+v", records)
	}

	out.Reset()
	records, err = RecordsToCopy(context.Background(), cmd, client, scope, "did-2")
	if err != nil {
		t.Fatalf("RecordsToCopy individual returned error: %v", err)
	}
	if len(records) != 1 || records[0].Did != "did-2" {
		t.Fatalf("unexpected individual record result: %+v", records)
	}

	if _, err := RecordsToCopy(context.Background(), cmd, client, scope, "did-other"); err == nil || !strings.Contains(err.Error(), "does not belong to source scope") {
		t.Fatalf("expected out-of-scope individual DID error, got %v", err)
	}
}

func signedToken(t *testing.T, exp time.Time) string {
	t.Helper()

	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iat": time.Now().Add(-time.Minute).Unix(),
		"exp": exp.Unix(),
	})
	encoded, err := tok.SignedString([]byte("secret"))
	if err != nil {
		t.Fatalf("SignedString returned error: %v", err)
	}
	return encoded
}

func testCommand() *cobra.Command {
	root := &cobra.Command{Use: "syfon"}
	root.PersistentFlags().String("server", "http://127.0.0.1:8080", "")
	cmd := &cobra.Command{Use: "child"}
	root.AddCommand(cmd)
	return cmd
}

func writeProfileConfig(t *testing.T, home, content string) {
	t.Helper()

	gen3Dir := filepath.Join(home, ".gen3")
	if err := os.MkdirAll(gen3Dir, 0o700); err != nil {
		t.Fatalf("mkdir .gen3: %v", err)
	}
	configPath := filepath.Join(gen3Dir, "gen3_client_config.ini")
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func testIndexService() *services.IndexService {
	listPages := map[string]internalapi.ListRecordsResponse{
		"": {
			Records: &[]internalapi.InternalRecord{
				{
					Did:              "did-1",
					ControlledAccess: &[]string{"/organization/syfon/project/e2e"},
				},
				{
					Did:              "did-2",
					ControlledAccess: &[]string{"/organization/syfon/project/e2e"},
				},
			},
		},
	}
	recordByID := map[string]internalapi.InternalRecordResponse{
		"did-2": {
			Did:              "did-2",
			ControlledAccess: &[]string{"/organization/syfon/project/e2e"},
		},
		"did-other": {
			Did:              "did-other",
			ControlledAccess: &[]string{"/organization/other/project/nope"},
		},
	}

	return services.NewIndexService(
		stubInternalClient{
			getResponse: func(id string) (*internalapi.InternalGetResponse, error) {
				resp, ok := recordByID[id]
				if !ok {
					return &internalapi.InternalGetResponse{HTTPResponse: &http.Response{StatusCode: http.StatusNotFound}}, nil
				}
				return &internalapi.InternalGetResponse{
					HTTPResponse: &http.Response{StatusCode: http.StatusOK},
					JSON200:      &resp,
				}, nil
			},
		},
		stubRequester{
			listResponse: func(start string) (internalapi.ListRecordsResponse, error) {
				if resp, ok := listPages[start]; ok {
					return resp, nil
				}
				return internalapi.ListRecordsResponse{}, nil
			},
		},
	)
}

type stubInternalClient struct {
	internalapi.ClientWithResponsesInterface
	getResponse func(id string) (*internalapi.InternalGetResponse, error)
}

func (s stubInternalClient) InternalGetWithResponse(ctx context.Context, id string, reqEditors ...internalapi.RequestEditorFn) (*internalapi.InternalGetResponse, error) {
	if s.getResponse == nil {
		return nil, fmt.Errorf("unexpected InternalGetWithResponse call")
	}
	return s.getResponse(id)
}

type stubRequester struct {
	listResponse func(start string) (internalapi.ListRecordsResponse, error)
}

func (s stubRequester) Do(ctx context.Context, method, path string, body, out any, opts ...request.RequestOption) error {
	if method != http.MethodGet || path != "/index" {
		return fmt.Errorf("unexpected request: %s %s", method, path)
	}
	builder := &request.RequestBuilder{Url: path, Headers: map[string]string{}}
	for _, opt := range opts {
		opt(builder)
	}
	start := ""
	if strings.Contains(builder.Url, "?") {
		values, err := url.ParseQuery(strings.TrimPrefix(strings.SplitN(builder.Url, "?", 2)[1], "?"))
		if err != nil {
			return err
		}
		start = values.Get("start")
	}
	resp, err := s.listResponse(start)
	if err != nil {
		return err
	}
	target, ok := out.(*internalapi.ListRecordsResponse)
	if !ok {
		return fmt.Errorf("unexpected response target %T", out)
	}
	*target = resp
	return nil
}
