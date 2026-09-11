package download

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/calypr/syfon/client/common"
	"github.com/calypr/syfon/client/transfer/engine"
	"github.com/calypr/syfon/cmd/cliauth"
	"github.com/calypr/syfon/cmd/transferprogress"
	"github.com/spf13/cobra"
)

var (
	downloadDID string
	downloadOut string
)

var Cmd = &cobra.Command{
	Use:   "download",
	Short: "Download an object to a local file",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		did := strings.TrimSpace(downloadDID)
		if did == "" {
			return fmt.Errorf("--did is required")
		}
		c, err := cliauth.NewServerClient(cmd)
		if err != nil {
			return err
		}
		outPath := strings.TrimSpace(downloadOut)
		var expectedSize int64
		if outPath == "" {
			rec, err := c.Index().Get(ctx, did)
			if err != nil {
				return fmt.Errorf("resolve output filename from record: %w", err)
			}
			name := did
			if rec.Name != nil && strings.TrimSpace(*rec.Name) != "" {
				name = strings.TrimSpace(*rec.Name)
			}
			if rec.Size != nil {
				expectedSize = *rec.Size
			}
			outPath = name
		} else {
			rec, err := c.Index().Get(ctx, did)
			if err == nil && rec.Size != nil {
				expectedSize = *rec.Size
			}
		}
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return fmt.Errorf("create output directory: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Downloading %s -> %s", did, outPath)
		if expectedSize > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), " (%s)", common.FormatSize(expectedSize))
		}
		fmt.Fprintln(cmd.OutOrStdout())

		progress := transferprogress.New(cmd.OutOrStdout(), filepath.Base(outPath), expectedSize)
		progress.Start()
		downloadCtx := transferprogress.WithProgress(ctx, did, progress)

		if err := engine.Download(downloadCtx, c.Data(), did, outPath, engine.DownloadOptions{MultipartThreshold: 5 * common.GB}); err != nil {
			progress.Abort()
			return err
		}
		progress.Finish()
		fmt.Fprintf(cmd.OutOrStdout(), "downloaded %s -> %s\n", did, outPath)
		return nil
	},
}

func init() {
	Cmd.Flags().StringVar(&downloadDID, "did", "", "DRS object DID")
	Cmd.Flags().StringVar(&downloadOut, "out", "", "Output file path (defaults to record file name)")
}
