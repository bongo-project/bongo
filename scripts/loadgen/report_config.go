package main

import "time"

// reportConfig holds the options of the sample, probe, report and
// report-compare subcommands. Their flags carry the prefixes sample-, probe-,
// report- and report-compare-, because every subcommand shares newFlagSet's
// names with the smtp flags and a duplicate name would panic for all of them.
type reportConfig struct {
	Sample  sampleOptions
	Probe   probeOptions
	Report  reportOptions
	Compare compareOptions
}

// sampleOptions are the sample subcommand's flags.
type sampleOptions struct {
	URL         string
	Interval    time.Duration
	Timeout     time.Duration
	Duration    time.Duration
	Out         string
	Raw         string
	RawMaxMB    int
	Append      bool
	InsecureTLS bool
}

// probeOptions are the probe subcommand's flags.
type probeOptions struct {
	StateFile   string
	Account     int
	IMAPAddr    string
	IMAPTLS     string
	InsecureTLS bool
	Interval    time.Duration
	Duration    time.Duration
	Timeout     time.Duration
	Poll        time.Duration
	Size        int
	Summary     string
}

// reportOptions are the report subcommand's flags.
type reportOptions struct {
	SMTP        []string
	Clients     string
	Sink        string
	Samples     string
	Raw         string
	Probe       string
	Meta        string
	BongoConfig string
	Out         string

	Version, Machine, Disk, FileSystem, Seed string
}

// compareOptions are the report-compare subcommand's flags.
type compareOptions struct {
	Baseline  string
	Candidate string
	Threshold float64
}
