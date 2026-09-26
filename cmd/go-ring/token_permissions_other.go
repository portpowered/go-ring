//go:build !windows

package main

func restrictTokenFile(string) error { return nil }
