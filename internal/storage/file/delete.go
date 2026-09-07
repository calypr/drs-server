package file

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/calypr/syfon/internal/storage"
)

func (b *backend) Delete(_ context.Context, targets []storage.PhysicalTarget) error {
	for _, target := range targets {
		if strings.TrimSpace(target.Path) == "" {
			continue
		}
		if err := os.Remove(target.Path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("delete file %s: %w", target.Path, err)
		}
	}
	return nil
}
