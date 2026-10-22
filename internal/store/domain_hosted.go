package store

import (
	"errors"
	"fmt"
	"strings"
)

// DomainHosted reports whether this server hosts domain, i.e. a
// /system/domain/{domain} key exists. It is a property of the domain, not of
// any address: an external address can resolve to a local user through a
// verified send-as identity without its domain being hosted here.
func DomainHosted(ps interface {
	Get(key []byte) ([]byte, error)
}, domain string) (bool, error) {
	_, err := ps.Get([]byte("/system/domain/" + strings.ToLower(domain)))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return false, fmt.Errorf("checking domain %s: %w", domain, err)
}
