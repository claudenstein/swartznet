package main

import (
	"net"
	"testing"
)

func listenLoopback(t *testing.T) (net.Listener, error) {
	t.Helper()
	return net.Listen("tcp", "localhost:0")
}
