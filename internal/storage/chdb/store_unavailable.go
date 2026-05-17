//go:build !chdb

package chdb

import (
	"fmt"
	"strings"

	"kube-insight/internal/storage"
)

func NewStore(opts Options) (storage.Store, error) {
	if strings.TrimSpace(opts.Path) == "" {
		return nil, fmt.Errorf("chdb path is required")
	}
	if strings.TrimSpace(opts.Database) == "" {
		return nil, fmt.Errorf("chdb database is required")
	}
	return nil, ErrUnavailable
}
