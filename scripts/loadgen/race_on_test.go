//go:build race

package main

// raceEnabled lets the rate test use a floor that fits the race detector, which
// slows Bleve text analysis about tenfold.
const raceEnabled = true
