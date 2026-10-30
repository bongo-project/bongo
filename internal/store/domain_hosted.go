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
func DomainHosted(ps domainGetter, domain string) (bool, error) {
	_, hosted, err := domainState(ps, domain)
	return hosted, err
}

// DomainDisabled reports whether domain is hosted here and an administrator
// has disabled it. A disabled domain receives no mail; see RecipientResolver.
func DomainDisabled(ps domainGetter, domain string) (bool, error) {
	state, _, err := domainState(ps, domain)
	return state == "disabled", err
}

type domainGetter interface {
	Get(key []byte) ([]byte, error)
}

// domainState reads /system/domain/{domain}, whose value is "enabled" or
// "disabled".
func domainState(ps domainGetter, domain string) (state string, hosted bool, err error) {
	data, err := ps.Get([]byte("/system/domain/" + strings.ToLower(domain)))
	if err == nil {
		return string(data), true, nil
	}
	if errors.Is(err, ErrNotFound) {
		return "", false, nil
	}
	return "", false, fmt.Errorf("checking domain %s: %w", domain, err)
}
