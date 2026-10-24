package handlers

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

// mboxReadBufferSize is the bufio window scanMbox reads through. It also bounds
// how far past a message the reader can have consumed when the callback runs.
const mboxReadBufferSize = 64 << 10

var mboxSeparator = []byte("From ")

// scanMbox streams an mbox from r and calls fn once per non-blank message, in
// order. Only one message is held in memory at a time.
//
// A message starts at a line beginning "From " and runs to the byte before the
// newline that precedes the next such line (or to EOF for the last one, trailing
// newline included). Text before the first separator is dropped, and messages
// that are blank after trimming are skipped. Carriage returns are preserved, so
// the bytes handed to fn are the bytes in the file.
//
// A message longer than maxMsg is not buffered: it is discarded up to the next
// separator and fn is called with a nil msg and oversized true, so the caller
// can count it as failed. The msg slice is only valid until fn returns.
//
// A read error is returned after the messages before it have been delivered; a
// message cut short by the error is not delivered.
func scanMbox(r io.Reader, maxMsg int, fn func(msg []byte, oversized bool) error) error {
	br := bufio.NewReaderSize(r, mboxReadBufferSize)
	var (
		buf         bytes.Buffer
		inMessage   bool // a separator has been seen
		oversized   bool
		atLineStart = true
		inSepLine   bool // skipping the remainder of a "From " line
	)

	// flush delivers the message collected so far. final is true at EOF, where
	// the last message keeps its trailing newline.
	flush := func(final bool) error {
		if !inMessage {
			return nil
		}
		if oversized {
			return fn(nil, true)
		}
		msg := buf.Bytes()
		if !final && len(msg) > 0 && msg[len(msg)-1] == '\n' {
			msg = msg[:len(msg)-1]
		}
		if len(bytes.TrimSpace(msg)) == 0 {
			return nil
		}
		return fn(msg, false)
	}

	for {
		chunk, err := br.ReadSlice('\n')
		if len(chunk) > 0 {
			complete := chunk[len(chunk)-1] == '\n'
			switch {
			case atLineStart && bytes.HasPrefix(chunk, mboxSeparator):
				if ferr := flush(false); ferr != nil {
					return ferr
				}
				buf.Reset()
				inMessage, oversized = true, false
				inSepLine = !complete
			case inSepLine:
				inSepLine = !complete
			case inMessage && !oversized:
				if buf.Len()+len(chunk) > maxMsg {
					oversized = true
					buf.Reset()
				} else {
					buf.Write(chunk)
				}
			}
			atLineStart = complete
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err == io.EOF {
			// A separator line with no newline yields no body, and flush skips
			// the empty message.
			return flush(true)
		}
		if err != nil {
			return err
		}
	}
}
