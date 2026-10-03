//go:build !fixtureinterrupt

// Package seam is the fixture's interruption point inside an ingestion's run (acceptance plan
// S1: the process killed after each pipeline step). Only a binary built with the fixtureinterrupt
// tag, the fixture's instance C, has one (seam_on.go); in every other build At is empty.
package seam

// At does nothing: this build has no interruption seam.
func At(string) {}
