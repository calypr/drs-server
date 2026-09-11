package projectcopy

import (
	"context"
	"fmt"
	"strings"

	syclient "github.com/calypr/syfon/client"
	conf "github.com/calypr/syfon/client/config"
	"github.com/calypr/syfon/cmd/cliauth"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type AuthFlags struct {
	IndividualDID       string
	SourceProfile       string
	SourceToken         string
	SourceBasicUser     string
	SourceBasicPassword string
	TargetServerURL     string
	TargetProfile       string
	TargetToken         string
	TargetBasicUser     string
	TargetBasicPassword string
}

func (f *AuthFlags) RegisterIndividualFlag(fs *pflag.FlagSet) {
	fs.StringVarP(&f.IndividualDID, "individual", "I", "", "Copy only a single DID from the source scope")
}

func (f *AuthFlags) RegisterSourceFlags(fs *pflag.FlagSet) {
	fs.StringVar(&f.SourceProfile, "source-profile", "", "Gen3 profile for source reads; preferred over inherited --profile on this command")
	fs.StringVar(&f.SourceToken, "source-token", "", "Bearer token for source reads; overrides --source-profile")
	fs.StringVar(&f.SourceBasicUser, "source-basic-user", "", "Basic auth username for source Syfon reads")
	fs.StringVar(&f.SourceBasicPassword, "source-basic-password", "", "Basic auth password for source Syfon reads")
}

func (f *AuthFlags) RegisterTargetFlags(fs *pflag.FlagSet) {
	fs.StringVar(&f.TargetServerURL, "target-server", "", "Destination Syfon server base URL")
	fs.StringVar(&f.TargetProfile, "target-profile", "", "Gen3 profile for target writes")
	fs.StringVar(&f.TargetToken, "target-token", "", "Bearer token for target writes; overrides --target-profile")
	fs.StringVar(&f.TargetBasicUser, "target-basic-user", "", "Basic auth username for target Syfon writes")
	fs.StringVar(&f.TargetBasicPassword, "target-basic-password", "", "Basic auth password for target Syfon writes")
}

func (f *AuthFlags) NewSourceClient(ctx context.Context, cmd *cobra.Command) (*syclient.Client, string, error) {
	serverURL, err := f.resolveSourceServerURL(ctx, cmd)
	if err != nil {
		return nil, "", err
	}
	opts, err := f.sourceClientOptions(ctx)
	if err != nil {
		return nil, "", err
	}
	client, err := syclient.New(serverURL, opts...)
	if err != nil {
		return nil, "", err
	}
	return client, serverURL, nil
}

func (f *AuthFlags) NewTargetClient(ctx context.Context, cmd *cobra.Command, allowRootServer, allowRootAuth bool) (*syclient.Client, string, error) {
	serverURL, err := f.resolveTargetServerURL(ctx, cmd, allowRootServer)
	if err != nil {
		return nil, "", err
	}
	opts, err := f.targetClientOptions(ctx, allowRootAuth)
	if err != nil {
		return nil, "", err
	}
	client, err := syclient.New(serverURL, opts...)
	if err != nil {
		return nil, "", err
	}
	return client, serverURL, nil
}

func (f *AuthFlags) sourceClientOptions(ctx context.Context) ([]syclient.Option, error) {
	if !f.hasExplicitSourceAuthInputs() {
		return cliauth.ServerClientOptions()
	}
	return clientOptionsFromInputs(ctx, "source", f.SourceProfile, f.SourceToken, f.SourceBasicUser, f.SourceBasicPassword)
}

func (f *AuthFlags) targetClientOptions(ctx context.Context, allowRootAuth bool) ([]syclient.Option, error) {
	if !f.hasExplicitTargetAuthInputs() && allowRootAuth {
		return cliauth.ServerClientOptions()
	}
	return clientOptionsFromInputs(ctx, "target", f.TargetProfile, f.TargetToken, f.TargetBasicUser, f.TargetBasicPassword)
}

func (f *AuthFlags) hasExplicitSourceAuthInputs() bool {
	return strings.TrimSpace(f.SourceProfile) != "" ||
		strings.TrimSpace(f.SourceToken) != "" ||
		strings.TrimSpace(f.SourceBasicUser) != "" ||
		strings.TrimSpace(f.SourceBasicPassword) != ""
}

func (f *AuthFlags) hasExplicitTargetAuthInputs() bool {
	return strings.TrimSpace(f.TargetProfile) != "" ||
		strings.TrimSpace(f.TargetToken) != "" ||
		strings.TrimSpace(f.TargetBasicUser) != "" ||
		strings.TrimSpace(f.TargetBasicPassword) != ""
}

func (f *AuthFlags) resolveSourceServerURL(ctx context.Context, cmd *cobra.Command) (string, error) {
	if strings.TrimSpace(f.SourceProfile) != "" {
		credential, err := cliauth.LoadProfileCredential(ctx, f.SourceProfile)
		if err != nil {
			return "", err
		}
		serverURL := strings.TrimRight(strings.TrimSpace(credential.APIEndpoint), "/")
		if serverURL == "" {
			return "", fmt.Errorf("source profile %q has no api_endpoint", f.SourceProfile)
		}
		return serverURL, nil
	}
	return cliauth.ResolveServerURL(cmd)
}

func (f *AuthFlags) resolveTargetServerURL(ctx context.Context, cmd *cobra.Command, allowRootServer bool) (string, error) {
	if serverURL := strings.TrimRight(strings.TrimSpace(f.TargetServerURL), "/"); serverURL != "" {
		return serverURL, nil
	}
	if strings.TrimSpace(f.TargetProfile) != "" {
		credential, err := cliauth.LoadProfileCredential(ctx, f.TargetProfile)
		if err != nil {
			return "", err
		}
		serverURL := strings.TrimRight(strings.TrimSpace(credential.APIEndpoint), "/")
		if serverURL == "" {
			return "", fmt.Errorf("target profile %q has no api_endpoint", f.TargetProfile)
		}
		return serverURL, nil
	}
	if allowRootServer {
		return cliauth.ResolveServerURL(cmd)
	}
	return "", fmt.Errorf("--target-server is required unless --target-profile provides the destination api_endpoint")
}

func clientOptionsFromInputs(ctx context.Context, side, profile, token, basicUser, basicPassword string) ([]syclient.Option, error) {
	basicUser = strings.TrimSpace(basicUser)
	basicPassword = strings.TrimSpace(basicPassword)
	token = strings.TrimSpace(token)
	profile = strings.TrimSpace(profile)
	if basicUser != "" || basicPassword != "" {
		if basicUser == "" || basicPassword == "" {
			return nil, fmt.Errorf("--%s-basic-user and --%s-basic-password must be set together", side, side)
		}
		if token != "" {
			return nil, fmt.Errorf("--%s-token cannot be combined with --%s-basic-user/--%s-basic-password", side, side, side)
		}
		if profile != "" {
			return nil, fmt.Errorf("--%s-profile cannot be combined with --%s-basic-user/--%s-basic-password", side, side, side)
		}
		return []syclient.Option{syclient.WithBasicAuth(basicUser, basicPassword)}, nil
	}
	if token != "" {
		if profile != "" {
			return nil, fmt.Errorf("--%s-token cannot be combined with --%s-profile", side, side)
		}
		return []syclient.Option{syclient.WithBearerToken(token)}, nil
	}
	if profile == "" {
		return nil, nil
	}
	credential, err := cliauth.LoadProfileCredential(ctx, profile)
	if err != nil {
		return nil, err
	}
	return optionsFromCredential(profile, credential)
}

func optionsFromCredential(profile string, credential *conf.Credential) ([]syclient.Option, error) {
	if credential == nil {
		return nil, fmt.Errorf("profile %q resolved to nil credential", profile)
	}
	if accessToken := strings.TrimSpace(credential.AccessToken); accessToken != "" {
		return []syclient.Option{syclient.WithBearerToken(accessToken)}, nil
	}
	keyID := strings.TrimSpace(credential.KeyID)
	apiKey := strings.TrimSpace(credential.APIKey)
	if keyID != "" && apiKey != "" {
		return []syclient.Option{syclient.WithBasicAuth(keyID, apiKey)}, nil
	}
	return nil, fmt.Errorf("profile %q has no access_token or key_id/api_key", profile)
}
