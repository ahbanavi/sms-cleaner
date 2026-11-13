package main

import (
	"slices"
	"testing"
)

func TestDNSServers(t *testing.T) {
	got := dnsServers(" 192.168.1.1, fe80::1%wlan0 ,10.0.0.1:5353,")
	want := []string{"192.168.1.1:53", "[fe80::1%wlan0]:53", "10.0.0.1:5353", "1.1.1.1:53", "8.8.8.8:53"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
