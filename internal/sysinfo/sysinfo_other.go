//go:build !linux

package sysinfo

// Read is only implemented for Linux, where Heimdeck usually runs.
func Read([]string) (Info, error) { return Info{}, ErrUnsupported }
