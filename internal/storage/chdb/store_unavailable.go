//go:build !chdb

package chdb

import (
	"kube-insight/internal/storage"
)

func NewStore(opts Options) (storage.Store, error) {
	if _, err := validateOptions(opts); err != nil {
		return nil, err
	}
	return nil, ErrUnavailable
}
