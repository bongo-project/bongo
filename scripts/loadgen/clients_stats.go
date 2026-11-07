package main

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/emersion/go-imap/v2"
)

// Operation names in the summary.
const (
	opConnect      = "connect"
	opLogin        = "login"
	opList         = "list"
	opSelect       = "select"
	opFetchHeaders = "fetch_headers"
	opFetchBody    = "fetch_body"
	opStore        = "store"
	opIdleStart    = "idle_start"
	opIdleHold     = "idle_hold"
	opDAVPropfind  = "dav_propfind"
	opDAVReport    = "dav_report"
	opDAVPut       = "dav_put"
)

var clientOps = []string{
	opConnect, opLogin, opList, opSelect, opFetchHeaders, opFetchBody, opStore,
	opIdleStart, opIdleHold, opDAVPropfind, opDAVReport, opDAVPut,
}

// errClass is the only form an error takes in the summary. Raw error text can
// carry request URLs with account names, or text the server chose to echo, so
// it is classified and dropped.
type errClass int

const (
	classAuth errClass = iota
	classRefusedOrClosed
	classTimeout
	classProtocol
	classHTTP4xx
	classHTTP5xx
	classOther
	numClasses
)

var classNames = [numClasses]string{
	"auth", "refused_or_closed", "timeout", "protocol_no_bad", "http_4xx", "http_5xx", "other",
}

// classifyIMAP maps an error from the IMAP client to a class. login is true
// for the LOGIN command, where any NO is an authentication failure. The error
// text is inspected for classification only and never stored.
func classifyIMAP(err error, login bool) errClass {
	var ie *imap.Error
	switch {
	case errors.Is(err, errIMAPTimeout), errors.Is(err, context.DeadlineExceeded):
		return classTimeout
	case errors.As(err, &ie):
		if ie.Code == imap.ResponseCodeAuthenticationFailed || ie.Code == imap.ResponseCodeAuthorizationFailed ||
			(login && ie.Type == imap.StatusResponseTypeNo) {
			return classAuth
		}
		return classProtocol
	}
	return classifyTransport(err)
}

func classifyTransport(err error) errClass {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return classTimeout
	}
	var oe *net.OpError
	switch {
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, net.ErrClosed),
		errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, syscall.EPIPE),
		errors.As(err, &oe):
		return classRefusedOrClosed
	}
	// imapclient reports a connection closed under it, and a BYE greeting,
	// with plain errors.
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "closed") || strings.Contains(msg, "bye") || strings.Contains(msg, "eof") {
		return classRefusedOrClosed
	}
	return classOther
}

// opStat is one operation's latency histogram and error tally.
type opStat struct {
	h    hist
	errs [numClasses]atomic.Int64
}

// clientStats is shared by every worker. All fields are safe for concurrent
// use.
type clientStats struct {
	ops map[string]*opStat

	idleOpen, idleMaxOpen       atomic.Int64
	idleReconnects, idleRestart atomic.Int64

	activeCycles, activeDone, fetchSkippedEmpty, activeStoppedAuth, idleStoppedAuth atomic.Int64
	davCycles, davWrites, davStoppedAuth                                            atomic.Int64
}

func newClientStats() *clientStats {
	s := &clientStats{ops: make(map[string]*opStat, len(clientOps))}
	for _, n := range clientOps {
		s.ops[n] = &opStat{}
	}
	return s
}

func (s *clientStats) ok(op string, d time.Duration) { s.ops[op].h.add(d) }

func (s *clientStats) fail(op string, c errClass) { s.ops[op].errs[c].Add(1) }

// idleUp and idleDown keep the open gauge and its high-water mark.
func (s *clientStats) idleUp() {
	storeMax(&s.idleMaxOpen, s.idleOpen.Add(1))
}

func (s *clientStats) idleDown() { s.idleOpen.Add(-1) }

// totals returns the successes and errors over every operation.
func (s *clientStats) totals() (okN, errN int64) {
	for _, o := range s.ops {
		okN += o.h.n.Load()
		for c := range o.errs {
			errN += o.errs[c].Load()
		}
	}
	return okN, errN
}
