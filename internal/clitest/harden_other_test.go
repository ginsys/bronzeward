//go:build !linux

package clitest

// checkHardened reads /proc, which only Linux has.
func (f *fixture) checkHardened() {}
