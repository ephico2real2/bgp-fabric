package main

import "testing"

func TestParseRoutersAndASNames(t *testing.T) {
	rs := parseRouters(defaultRouters)
	if len(rs) != 4 || rs[0].Name != "edge" || rs[3].URL != "http://10.200.200.12:8080" {
		t.Fatalf("%+v", rs)
	}
	as := parseASNames(defaultASNames)
	if as[65000] != "edge" || as[65021] != "eg-poc1 (kube-vip)" {
		t.Fatalf("%v", as)
	}
}
