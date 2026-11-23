package fetchtest

import (
	"bytes"
	"io"

	"github.com/emersion/go-imap/v2"
)

type literal struct {
	*bytes.Reader
}

func (l literal) Size() int64 { return l.Reader.Size() }

func bytesLiteral(s string) imap.LiteralReader { return literal{bytes.NewReader([]byte(s))} }

var _ io.Reader = literal{}
