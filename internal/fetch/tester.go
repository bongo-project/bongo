package fetch

import (
	"context"
	"errors"
)

// errUnknownProtocol is a programming error, not a remote failure; Code maps
// it to internal.
var errUnknownProtocol = errors.New("fetch: unknown protocol")

// Test checks that the account can be reached and signed in to, and that the
// folder exists (IMAP) or UIDL is supported (POP3). It fetches and changes
// nothing: IMAP uses EXAMINE and never selects read-write or fetches, POP3
// never retrieves or deletes. An empty folder means INBOX. The caller supplies
// the deadline in ctx.
func (c *Client) Test(ctx context.Context, protocol, folder string, p Params) error {
	switch protocol {
	case ProtocolIMAP:
		s, err := c.DialIMAP(ctx, p)
		if err != nil {
			return err
		}
		defer s.Close()
		if folder == "" {
			folder = "INBOX"
		}
		_, err = s.Select(folder, true)
		return err
	case ProtocolPOP3:
		s, err := c.DialPOP3(ctx, p)
		if err != nil {
			return err
		}
		defer s.Close()
		if _, err := s.UIDL(); err != nil {
			return err
		}
		return s.Quit()
	}
	return errUnknownProtocol
}
