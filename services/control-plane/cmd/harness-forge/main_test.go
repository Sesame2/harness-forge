package main

import (
	"net"
	"net/http"
	"testing"
)

func TestArtifactListenerBindFailureClosesControlPlaneListener(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	controlAddr := probe.Addr().String()
	probe.Close()
	if err := serveListeners(controlAddr, occupied.Addr().String(), http.NotFoundHandler(), http.NotFoundHandler()); err == nil {
		t.Fatal("artifact bind failure ignored")
	}
	probe, err = net.Listen("tcp", controlAddr)
	if err != nil {
		t.Fatalf("control-plane listener leaked: %v", err)
	}
	probe.Close()
}
