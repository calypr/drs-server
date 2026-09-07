package config

//go:generate mockgen -destination=../internal/testmocks/config_manager_mock.go -package=testmocks github.com/calypr/syfon/client/config ManagerInterface

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"strings"

	"github.com/calypr/syfon/client/common"
	"gopkg.in/ini.v1"
)

var ErrProfileNotFound = errors.New("profile not found in config file")

type Credential struct {
	Profile            string
	KeyID              string
	APIKey             string
	AccessToken        string
	APIEndpoint        string
	UseShepherd        string
	MinShepherdVersion string
	Bucket             string
	ProjectID          string
}

type CredentialReader interface {
	Current() *Credential
}

type CredentialManager interface {
	CredentialReader
	Export(ctx context.Context, cred *Credential) error
}

type Manager struct {
	Logger *slog.Logger
}

func NewConfigure(logs *slog.Logger) ManagerInterface {
	return &Manager{
		Logger: logs,
	}
}

type ManagerInterface interface {
	// Loads credential from ~/.gen3/ credential file
	Import(filePath, fenceToken string) (*Credential, error)

	// Loads credential from ~/.gen3/config.ini
	Load(profile string) (*Credential, error)
	Save(cred *Credential) error

	EnsureExists() error
	IsCredentialValid(*Credential) (bool, error)
	IsTokenValid(string) (bool, error)
}

func (man *Manager) configPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	configPath := path.Join(
		homeDir +
			common.PathSeparator +
			".gen3" +
			common.PathSeparator +
			"gen3_client_config.ini",
	)
	return configPath, nil
}

func (man *Manager) Load(profile string) (*Credential, error) {
	/*
		Looking profile in config file. The config file is a text file located at ~/.gen3 directory. It can
		contain more than 1 profile. If there is no profile found, the user is asked to run a command to
		create the profile

		The format of config file is described as following

		[profile1]
		key_id=key_id_example_1
		api_key=api_key_example_1
		access_token=access_token_example_1
		api_endpoint=http://localhost:8000
		use_shepherd=true
		min_shepherd_version=2.0.0

		[profile2]
		key_id=key_id_example_2
		api_key=api_key_example_2
		access_token=access_token_example_2
		api_endpoint=http://localhost:8000
		use_shepherd=false
		min_shepherd_version=

		Args:
			profile: the specific profile in config file
		Returns:
			An instance of Credential
	*/

	homeDir, err := os.UserHomeDir()
	if err != nil {
		errs := fmt.Errorf("Error occurred when getting home directory: %s", err.Error())
		man.Logger.Error(errs.Error())
		return nil, errs
	}
	configPath := path.Join(homeDir + common.PathSeparator + ".gen3" + common.PathSeparator + "gen3_client_config.ini")

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("%w Run configure command (with a profile if desired) to set up account credentials \n"+
			"Example: ./data-client configure --profile=<profile-name> --cred=<path-to-credential/cred.json> --apiendpoint=https://data.mycommons.org", ErrProfileNotFound)
	}

	// If profile not in config file, prompt user to set up config first
	cfg, err := ini.Load(configPath)
	if err != nil {
		errs := fmt.Errorf("Error occurred when reading config file: %s", err.Error())
		return nil, errs
	}
	sec, err := cfg.GetSection(profile)
	if err != nil {
		return nil, fmt.Errorf("%w: Need to run \"data-client configure --profile="+profile+" --cred=<path-to-credential/cred.json> --apiendpoint=<api_endpoint_url>\" first", ErrProfileNotFound)
	}

	profileConfig := &Credential{
		Profile:            profile,
		KeyID:              sec.Key("key_id").String(),
		APIKey:             sec.Key("api_key").String(),
		AccessToken:        sec.Key("access_token").String(),
		APIEndpoint:        sec.Key("api_endpoint").String(),
		UseShepherd:        sec.Key("use_shepherd").String(),
		MinShepherdVersion: sec.Key("min_shepherd_version").String(),
		Bucket:             sec.Key("bucket").String(),
		ProjectID:          sec.Key("project_id").String(),
	}

	if profileConfig.KeyID == "" && profileConfig.APIKey == "" && profileConfig.AccessToken == "" {
		errs := fmt.Errorf("key_id, api_key and access_token not found in profile.")
		return nil, errs
	}
	if profileConfig.APIEndpoint == "" {
		errs := fmt.Errorf("api_endpoint not found in profile.")
		return nil, errs
	}

	return profileConfig, nil
}

func (man *Manager) Save(profileConfig *Credential) error {
	/*
		Overwrite the config file with new credential

		Args:
			profileConfig: Credential object represents config of a profile
			configPath: file path to config file
	*/
	configPath, err := man.configPath()
	if err != nil {
		errs := fmt.Errorf("error occurred when getting config path: %s", err.Error())
		man.Logger.Error(errs.Error())
		return errs
	}
	cfg, err := ini.Load(configPath)
	if err != nil {
		errs := fmt.Errorf("error occurred when loading config file: %s", err.Error())
		man.Logger.Error(errs.Error())
		return errs
	}

	section := cfg.Section(profileConfig.Profile)
	if profileConfig.KeyID != "" {
		section.Key("key_id").SetValue(profileConfig.KeyID)
	} else {
		section.DeleteKey("key_id")
	}
	if profileConfig.APIKey != "" {
		section.Key("api_key").SetValue(profileConfig.APIKey)
	} else {
		section.DeleteKey("api_key")
	}
	if profileConfig.AccessToken != "" {
		section.Key("access_token").SetValue(profileConfig.AccessToken)
	} else {
		section.DeleteKey("access_token")
	}
	if profileConfig.APIEndpoint != "" {
		section.Key("api_endpoint").SetValue(profileConfig.APIEndpoint)
	} else {
		section.DeleteKey("api_endpoint")
	}

	section.Key("use_shepherd").SetValue(profileConfig.UseShepherd)
	section.Key("min_shepherd_version").SetValue(profileConfig.MinShepherdVersion)
	section.Key("bucket").SetValue(profileConfig.Bucket)
	section.Key("project_id").SetValue(profileConfig.ProjectID)
	err = cfg.SaveTo(configPath)
	if err != nil {
		errs := fmt.Errorf("error occurred when saving config file: %s", err.Error())
		man.Logger.Error(errs.Error())
		return fmt.Errorf("error occurred when saving config file: %s", err.Error())
	}
	return nil
}

func (man *Manager) EnsureExists() error {
	/*
		Make sure the config exists on start up
	*/
	configPath, err := man.configPath()
	if err != nil {
		return err
	}

	if _, err := os.Stat(path.Dir(configPath)); os.IsNotExist(err) {
		// SECURITY FIX MED-3: Create directory with 0700 (owner-only) instead of 0777
		osErr := os.Mkdir(path.Join(path.Dir(configPath)), os.FileMode(0700))
		if osErr != nil {
			return err
		}
		_, osErr = os.Create(configPath)
		if osErr != nil {
			return err
		}
	}
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		_, osErr := os.Create(configPath)
		if osErr != nil {
			return err
		}
	}
	_, err = ini.Load(configPath)

	return err
}

func (man *Manager) Import(filePath, fenceToken string) (*Credential, error) {
	var cred Credential

	if filePath != "" {
		fullPath, err := common.GetAbsolutePath(filePath)
		if err != nil {
			man.Logger.Error("error parsing credential file path: " + err.Error())
			return nil, err
		}

		content, err := os.ReadFile(fullPath)
		if err != nil {
			if os.IsNotExist(err) {
				man.Logger.Error("File not found: " + fullPath)
			} else {
				man.Logger.Error("error reading file: " + err.Error())
			}
			return nil, err
		}

		jsonStr := strings.ReplaceAll(string(content), "\n", "")
		// Normalize keys from snake_case to CamelCase for unmarshaling
		jsonStr = strings.ReplaceAll(jsonStr, "key_id", "KeyID")
		jsonStr = strings.ReplaceAll(jsonStr, "api_key", "APIKey")

		if err := json.Unmarshal([]byte(jsonStr), &cred); err != nil {
			errMsg := fmt.Errorf("cannot parse JSON credential file: %w", err)
			man.Logger.Error(errMsg.Error())
			return nil, errMsg
		}
	} else if fenceToken != "" {
		cred.AccessToken = fenceToken
	} else {
		return nil, errors.New("either credential file or fence token must be provided")
	}

	return &cred, nil
}
