package local

import (
	"fmt"
	"strings"
	"testing"
)

func TestWeakNativeTokenIsOptInLocalAndOwnerBound(t *testing.T) {
	owner := ownerFixture()
	weak, err := NewWithWeakToken(owner, true)
	if err != nil {
		t.Fatal(err)
	}
	strict, _ := New(owner)
	for _, name := range []string{"Player", "A.B C!?", "...", "x", "16 printable!?.."} {
		body := envelope(fmt.Sprintf("sacredfire.%s.%016x", name, weak.PID()))
		if _, err := strict.AuthenticateFrom(body, "127.0.0.1:4321"); err != OwnerCapabilityMismatch {
			t.Fatal("weak token enabled by default")
		}
		for _, address := range []string{"127.0.0.1:4321", "127.0.0.2:1234", "[::1]:4321", "[::ffff:127.0.0.1]:4321"} {
			id, err := weak.AuthenticateFrom(body, address)
			if err != nil || id.PID != weak.PID() || id.Username != owner.OnlineID {
				t.Fatal("owner token refused or token name became trusted")
			}
		}
		for _, address := range []string{"", "localhost:4321", "192.0.2.1:4321", "[2001:db8::1]:4321", "127.0.0.1", "127.0.0.1:http", "127.0.0.1:0"} {
			if _, err := weak.AuthenticateFrom(body, address); err != WeakTokenNonLoopback {
				t.Fatal("nonlocal weak token admitted")
			}
		}
		if _, err := weak.Authenticate(body); err != WeakTokenNonLoopback {
			t.Fatal("source-less weak token admitted")
		}
	}
	if _, err := weak.Rename(owner.SteamID, "Current Name"); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{owner.Token, fmt.Sprintf("sacredfire.Old.Name.%016x", weak.PID())} {
		id, err := weak.AuthenticateFrom(envelope(token), "127.0.0.1:1234")
		if err != nil || id.Username != "Current Name" {
			t.Fatal("credential rolled back trusted name")
		}
	}
	if _, err := weak.AuthenticateFrom(envelope(fmt.Sprintf("sacredfire.Any.%016x", weak.PID()+1)), "127.0.0.1:1234"); err != WeakTokenOwnerMismatch {
		t.Fatal("foreign account admitted")
	}
}

func TestWeakNativeTokenRejectsMalformedSuffixAndName(t *testing.T) {
	p, _ := NewWithWeakToken(ownerFixture(), true)
	suffix := fmt.Sprintf("%016x", p.PID())
	for _, token := range []string{
		"sacredfire.." + suffix, "sacredfire." + strings.Repeat("a", 17) + "." + suffix,
		"sacredfire.Bad\nName." + suffix, "sacredfire.Noné." + suffix,
		"sacredfire.Name." + suffix[1:], "sacredfire.Name.0" + suffix,
		"sacredfire.Name." + strings.ToUpper(suffix), "sacredfire.Name.+" + suffix[1:],
		"sacredfire.Name.0x" + suffix[2:], "sacredfire.Name." + suffix + ".",
		"sacredfire.Name.0000000000000000",
	} {
		if _, err := p.AuthenticateFrom(envelope(token), "127.0.0.1:1234"); err != WeakTokenMalformed {
			t.Fatal("malformed native token admitted")
		}
	}
}

func TestLoopbackOwnerTransportAndAppNamespace(t *testing.T) {
	valid := VirtualTransport{Mode: "loopback-v1", Address: "127.64.0.1", Port: 1024, GameAddress: "127.0.0.1", GamePort: 3074}
	o := ownerFixture()
	o.Transport = &valid
	if _, err := New(o); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*VirtualTransport){
		func(v *VirtualTransport) { v.Address = "198.18.1.2" },
		func(v *VirtualTransport) { v.Address = "127.63.255.255" },
		func(v *VirtualTransport) { v.Address = "127.128.0.1" },
		func(v *VirtualTransport) { v.Port = 1023 },
		func(v *VirtualTransport) { v.GameAddress = "127.64.0.2" },
		func(v *VirtualTransport) { v.GameAddress = "0.0.0.0" },
	} {
		v := valid
		change(&v)
		o.Transport = &v
		if _, err := New(o); err == nil {
			t.Fatal("invalid loopback descriptor admitted")
		}
	}
	o = ownerFixture()
	old, _ := New(o)
	defaultApp := uint32(480)
	o.AppID = &defaultApp
	current, _ := New(o)
	if old.TrustID() != current.TrustID() {
		t.Fatal("default app namespace changed")
	}
	otherApp := uint32(481)
	o.AppID = &otherApp
	other, _ := New(o)
	if other.TrustID() == old.TrustID() {
		t.Fatal("different apps share a trust namespace")
	}
	zero := uint32(0)
	o.AppID = &zero
	if _, err := New(o); err == nil {
		t.Fatal("zero app ID accepted")
	}
}
