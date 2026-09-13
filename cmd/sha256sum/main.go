package sha256sum

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	syclient "github.com/calypr/syfon/client"
	"github.com/calypr/syfon/client/request"
	"github.com/calypr/syfon/cmd/cliauth"
	"github.com/spf13/cobra"
)

var shaDID string

var Cmd = &cobra.Command{
	Use:   "sha256sum",
	Short: "Compute sha256 for an object and persist it in record metadata",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if strings.TrimSpace(shaDID) == "" {
			return fmt.Errorf("--did is required")
		}

		c, err := cliauth.NewServerClient(cmd)
		if err != nil {
			return err
		}
		did := strings.TrimSpace(shaDID)
		signed, err := c.Data().DownloadURL(ctx, did, 0, false)
		if err != nil {
			return fmt.Errorf("get download url: %w", err)
		}
		downloadURL := ""
		if signed.Url != nil {
			downloadURL = strings.TrimSpace(*signed.Url)
		}
		if downloadURL == "" {
			return fmt.Errorf("empty download url for did %s", did)
		}

		data, err := readURLBytes(ctx, downloadURL, c)
		if err != nil {
			return err
		}
		sumArr := sha256.Sum256(data)
		sum := hex.EncodeToString(sumArr[:])

		if err := c.Index().Upsert(ctx, did, "", "", 0, sum, nil); err != nil {
			return fmt.Errorf("persist sha256: %w", err)
		}

		fmt.Fprintln(cmd.OutOrStdout(), sum)
		return nil
	},
}

func readURLBytes(ctx context.Context, rawURL string, c *syclient.Client) ([]byte, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("parse download url: %w", err)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "", "file":
		srcPath := parsed.Path
		if srcPath == "" {
			srcPath = rawURL
		}
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return nil, fmt.Errorf("read file source: %w", err)
		}
		return data, nil
	case "http", "https":
		var resp *http.Response
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create download request: %w", err)
		}
		request.SkipAuth(req)
		resp, err = c.Do(req)
		if err != nil {
			return nil, fmt.Errorf("download request failed: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
			if err != nil {
				return nil, fmt.Errorf("read error response body: %w", err)
			}
			return nil, fmt.Errorf("download failed status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("read download response: %w", err)
		}
		return data, nil
	default:
		return nil, fmt.Errorf("unsupported download url scheme %q", parsed.Scheme)
	}
}

func init() {
	Cmd.Flags().StringVar(&shaDID, "did", "", "DRS object DID")
}
