package observability

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// CommandTimer records the time one command or phase took. It wraps an
// observer created at init, so a hot path pays for the observation only:
//
//	defer observability.IMAPFetch.Since(time.Now())
type CommandTimer struct {
	obs prometheus.Observer
}

// Since observes the seconds elapsed since start.
func (c CommandTimer) Since(start time.Time) {
	c.obs.Observe(time.Since(start).Seconds())
}

// Protocol labels. The set is fixed so a metric never carries a client-chosen
// value. Mail protocols are counted per connection; CalDAV and CardDAV share
// the web listener with the web UI, so they are counted per request.
const (
	ProtocolIMAP       = "imap"
	ProtocolPOP3       = "pop3"
	ProtocolSMTP       = "smtp"
	ProtocolSubmission = "submission"
	ProtocolCalDAV     = "caldav"
	ProtocolCardDAV    = "carddav"
)

// otherCommand is the label for any command outside a protocol's fixed set.
const otherCommand = "other"

var (
	protocolConnections = factory.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "bongo_protocol_connections",
			Help: "Open client connections by protocol (imap, pop3, smtp, submission). imap and pop3 exclude connections refused by the per-IP limit and include their TLS listeners; smtp and submission count every accepted TCP connection, including those later refused with a 421 by the per-IP limit, and submission includes port 465.",
		},
		[]string{"protocol"},
	)
	protocolAccepted = factory.NewCounterVec(
		prometheus.CounterOpts{
			Name: "bongo_protocol_connections_accepted_total",
			Help: "Client connections accepted by protocol; same counting rules as bongo_protocol_connections.",
		},
		[]string{"protocol"},
	)
	davInFlight = factory.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "bongo_dav_requests_in_flight",
			Help: "CalDAV and CardDAV requests currently being served. These are requests, not connections: DAV shares the web listener, and one keep-alive connection can carry web and DAV traffic.",
		},
		[]string{"protocol"},
	)
	davRequests = factory.NewCounterVec(
		prometheus.CounterOpts{
			Name: "bongo_dav_requests_total",
			Help: "CalDAV and CardDAV requests served. These are requests, not connections.",
		},
		[]string{"protocol"},
	)
	protocolCommandSeconds = factory.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "bongo_protocol_command_seconds",
			Help: "Time to handle one protocol command, in seconds. IMAP is timed at the session methods (CAPABILITY, LOGOUT, STARTTLS, ENABLE, IDLE and unknown commands are not timed); " +
				"SMTP data includes the client upload and the delivery pipeline; DAV is the whole request; unrecognised POP3 commands and DAV methods are labelled other.",
			Buckets: []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 120},
		},
		[]string{"protocol", "command"},
	)
)

func timer(protocol, command string) CommandTimer {
	return CommandTimer{protocolCommandSeconds.WithLabelValues(protocol, command)}
}

func commandSet(protocol string, names ...string) map[string]CommandTimer {
	m := make(map[string]CommandTimer, len(names))
	for _, n := range names {
		m[n] = timer(protocol, n)
	}
	return m
}

// ConnCounter is the pair of metrics a counting listener updates for one
// protocol: Open is incremented on accept and decremented once on close.
type ConnCounter struct {
	Open     prometheus.Gauge
	Accepted prometheus.Counter
}

// ProtocolConns returns the connection metrics for one of the mail protocol
// constants. Call it at listener setup, not per connection.
func ProtocolConns(protocol string) ConnCounter {
	return ConnCounter{
		Open:     protocolConnections.WithLabelValues(protocol),
		Accepted: protocolAccepted.WithLabelValues(protocol),
	}
}

// IMAP command timers, one per session method. Idle is deliberately absent:
// it blocks until the client sends DONE and would swamp the histogram.
var (
	IMAPLogin       = timer(ProtocolIMAP, "login")
	IMAPSelect      = timer(ProtocolIMAP, "select")
	IMAPUnselect    = timer(ProtocolIMAP, "unselect")
	IMAPCreate      = timer(ProtocolIMAP, "create")
	IMAPDelete      = timer(ProtocolIMAP, "delete")
	IMAPRename      = timer(ProtocolIMAP, "rename")
	IMAPSubscribe   = timer(ProtocolIMAP, "subscribe")
	IMAPUnsubscribe = timer(ProtocolIMAP, "unsubscribe")
	IMAPList        = timer(ProtocolIMAP, "list")
	IMAPStatus      = timer(ProtocolIMAP, "status")
	IMAPAppend      = timer(ProtocolIMAP, "append")
	IMAPPoll        = timer(ProtocolIMAP, "poll")
	IMAPExpunge     = timer(ProtocolIMAP, "expunge")
	IMAPSearch      = timer(ProtocolIMAP, "search")
	IMAPFetch       = timer(ProtocolIMAP, "fetch")
	IMAPStore       = timer(ProtocolIMAP, "store")
	IMAPCopy        = timer(ProtocolIMAP, "copy")
	IMAPMove        = timer(ProtocolIMAP, "move")
)

// SMTP command timers, one per session method.
var (
	SMTPAuth  = timer(ProtocolSMTP, "auth")
	SMTPMail  = timer(ProtocolSMTP, "mail")
	SMTPRcpt  = timer(ProtocolSMTP, "rcpt")
	SMTPData  = timer(ProtocolSMTP, "data")
	SMTPReset = timer(ProtocolSMTP, "reset")
)

var (
	pop3Commands = commandSet(ProtocolPOP3,
		"QUIT", "CAPA", "USER", "PASS", "STLS", "STAT", "LIST", "UIDL", "RETR", "TOP", "DELE", "NOOP", "RSET")
	pop3Other = timer(ProtocolPOP3, otherCommand)

	davMethods = []string{"PROPFIND", "PROPPATCH", "REPORT", "GET", "HEAD", "PUT", "DELETE", "MKCOL", "MKCALENDAR", "MOVE", "COPY", "OPTIONS", "POST"}

	dav = map[string]*davProtocol{
		ProtocolCalDAV:  newDAVProtocol(ProtocolCalDAV),
		ProtocolCardDAV: newDAVProtocol(ProtocolCardDAV),
	}
)

// davProtocol holds the ready-made children for one DAV protocol, so a request
// does no label lookup beyond a map read.
type davProtocol struct {
	requests prometheus.Counter
	inFlight prometheus.Gauge
	commands map[string]CommandTimer
	other    CommandTimer
}

func newDAVProtocol(protocol string) *davProtocol {
	return &davProtocol{
		requests: davRequests.WithLabelValues(protocol),
		inFlight: davInFlight.WithLabelValues(protocol),
		commands: commandSet(protocol, davMethods...),
		other:    timer(protocol, otherCommand),
	}
}

// POP3Command returns the timer for an upper-cased POP3 command. Anything
// outside the standard set is labelled "other", so a client cannot create
// series.
func POP3Command(cmd string) CommandTimer {
	if t, ok := pop3Commands[cmd]; ok {
		return t
	}
	return pop3Other
}

// DAVRequest records the start of a CalDAV or CardDAV request (protocol is
// ProtocolCalDAV or ProtocolCardDAV) and returns the timer for its method.
// The method comes from the client, so anything outside the fixed set is
// labelled "other". The caller must call DAVDone when the request ends.
func DAVRequest(protocol, method string) CommandTimer {
	d := dav[protocol]
	d.requests.Inc()
	d.inFlight.Inc()
	if t, ok := d.commands[method]; ok {
		return t
	}
	return d.other
}

// DAVDone ends the in-flight count started by DAVRequest.
func DAVDone(protocol string) {
	dav[protocol].inFlight.Dec()
}

// DAVMiddleware counts and times every request to a CalDAV or CardDAV handler
// (protocol is ProtocolCalDAV or ProtocolCardDAV). The time is the whole
// request, including the client's upload and the response write.
func DAVMiddleware(protocol string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t := DAVRequest(protocol, r.Method)
		defer DAVDone(protocol)
		defer t.Since(time.Now())
		next.ServeHTTP(w, r)
	})
}

func init() {
	// A Vec with no children is not written to /metrics, so create the series
	// up front. Every name then appears from the first scrape.
	for _, p := range []string{ProtocolIMAP, ProtocolPOP3, ProtocolSMTP, ProtocolSubmission} {
		ProtocolConns(p)
	}
}
