package main

import (
	"os"
	"testing"
)

func TestDecodeSummaryActive(t *testing.T) {
	raw, err := os.ReadFile("testdata/bgp-summary-active.json")
	if err != nil {
		t.Fatal(err)
	}
	s, err := decodeSummary(raw)
	if err != nil {
		t.Fatal(err)
	}
	if s.IPv4Unicast == nil {
		t.Fatal("ipv4Unicast missing")
	}
	if s.IPv4Unicast.RouterID != "10.200.255.11" || s.IPv4Unicast.AS != 65101 {
		t.Fatalf("routerId/as = %s/%d", s.IPv4Unicast.RouterID, s.IPv4Unicast.AS)
	}
	p, ok := s.IPv4Unicast.Peers["10.255.0.2"]
	if !ok {
		t.Fatal("peer 10.255.0.2 missing")
	}
	if p.RemoteAS != 65100 || p.State != "Active" || p.PeerUptime != "never" {
		t.Fatalf("peer = %+v", p)
	}
	if p.Hostname != "" {
		t.Fatalf("Active peer hostname should be empty (absent in FRR JSON), got %q", p.Hostname)
	}
}

func TestDecodeIPv4Local(t *testing.T) {
	raw, err := os.ReadFile("testdata/bgp-ipv4-local.json")
	if err != nil {
		t.Fatal(err)
	}
	v, err := decodeIPv4(raw)
	if err != nil {
		t.Fatal(err)
	}
	if v.RouterID != "10.200.255.11" || v.LocalAS != 65101 {
		t.Fatalf("routerId/localAS = %s/%d", v.RouterID, v.LocalAS)
	}
	paths, ok := v.Routes["10.200.255.11/32"]
	if !ok || len(paths) != 1 {
		t.Fatalf("routes = %+v", v.Routes)
	}
	p := paths[0]
	if p.PeerID != "(unspec)" || p.Weight != 32768 || p.Origin != "IGP" {
		t.Fatalf("path = %+v", p)
	}
	if len(p.Nexthops) != 1 || p.Nexthops[0].IP != "0.0.0.0" {
		t.Fatalf("nexthops = %+v", p.Nexthops)
	}
	// local-originated, not selected: valid/bestpath absent in the capture
	if p.Valid || p.Bestpath.Set {
		t.Fatalf("expected valid/bestpath unset on local capture, got valid=%v best=%v", p.Valid, p.Bestpath.Set)
	}
}

func TestDecodeEstablishedHandEdited(t *testing.T) {
	// Hand-edited from the real FRR 10.7.1 shape (bgp-summary-active.json /
	// bgp-ipv4-local.json): state Established, hostname present, a received
	// /32 with valid+bestpath booleans as bgp_route.c emits on the table view.
	raw, err := os.ReadFile("testdata/bgp-summary-established.json")
	if err != nil {
		t.Fatal(err)
	}
	s, err := decodeSummary(raw)
	if err != nil {
		t.Fatal(err)
	}
	p := s.IPv4Unicast.Peers["10.200.1.3"]
	if p.State != "Established" || p.Hostname != "spine" || p.PfxRcd != 4 {
		t.Fatalf("spine peer = %+v", p)
	}
	ext := s.IPv4Unicast.Peers["172.20.0.3"]
	if ext.RemoteAS != 65021 || ext.State != "Established" {
		t.Fatalf("external peer = %+v", ext)
	}

	raw, err = os.ReadFile("testdata/bgp-ipv4-established.json")
	if err != nil {
		t.Fatal(err)
	}
	v, err := decodeIPv4(raw)
	if err != nil {
		t.Fatal(err)
	}
	vip := v.Routes["10.198.0.10/32"][0]
	if !vip.Valid || !vip.Bestpath.Set || vip.PeerID != "172.20.0.3" || vip.Path != "65021" {
		t.Fatalf("vip path = %+v", vip)
	}
	if vip.Nexthops[0].IP != "172.20.0.3" || vip.LocPrf != 100 {
		t.Fatalf("vip nh/locPrf = %+v", vip)
	}
}
