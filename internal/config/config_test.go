package config

import "testing"

func TestResolve_FillsFromYDBEnv(t *testing.T) {
	env := map[string]string{
		"ydb_dist":     "/opt/yottadb",
		"ydb_gbldir":   "/data/m.gld",
		"ydb_routines": "/data/r /opt/yottadb",
	}
	c := &Conn{Transport: "local"}
	c.Resolve(func(k string) string { return env[k] })

	if c.Dist != "/opt/yottadb" || c.GblDir != "/data/m.gld" || c.Routines != "/data/r /opt/yottadb" {
		t.Errorf("resolve = %+v, want filled from ydb_* env", c)
	}
}

func TestResolve_DoesNotOverrideExplicit(t *testing.T) {
	env := map[string]string{"ydb_dist": "/from/env", "ydb_gbldir": "/from/env.gld"}
	c := &Conn{Transport: "local", Dist: "/explicit"}
	c.Resolve(func(k string) string { return env[k] })

	if c.Dist != "/explicit" {
		t.Errorf("Dist = %q, explicit flag must win over env", c.Dist)
	}
	if c.GblDir != "/from/env.gld" {
		t.Errorf("GblDir = %q, want filled from env when unset", c.GblDir)
	}
}

func TestResolve_GTMFallback(t *testing.T) {
	// GT.M users (and older YottaDB) export gtm_* — accept them when ydb_* are absent.
	env := map[string]string{"gtm_dist": "/usr/lib/fis-gtm", "gtmgbldir": "/data/g.gld"}
	c := &Conn{Transport: "local"}
	c.Resolve(func(k string) string { return env[k] })

	if c.Dist != "/usr/lib/fis-gtm" || c.GblDir != "/data/g.gld" {
		t.Errorf("resolve = %+v, want gtm_* fallback", c)
	}
}

func TestValidate_DockerNeedsContainer(t *testing.T) {
	if err := (&Conn{Transport: "docker"}).Validate(); err == nil {
		t.Error("docker transport without a container must be a validation error")
	}
	if err := (&Conn{Transport: "docker", Container: "m-test-engine"}).Validate(); err != nil {
		t.Errorf("docker with container should validate: %v", err)
	}
	if err := (&Conn{Transport: "local"}).Validate(); err != nil {
		t.Errorf("local should validate without a container: %v", err)
	}
}

func TestValidate_RemoteNeedsHost(t *testing.T) {
	if err := (&Conn{Transport: "remote"}).Validate(); err == nil {
		t.Error("remote transport without a host must be a validation error")
	}
	if err := (&Conn{Transport: "remote", Host: "vehu.local"}).Validate(); err != nil {
		t.Errorf("remote with a host should validate: %v", err)
	}
}

func TestTransportConfig_Maps(t *testing.T) {
	c := &Conn{Transport: "docker", Container: "m-test-engine", Dist: "/opt/yottadb"}
	tc := c.TransportConfig()
	if tc.Transport != "docker" || tc.Container != "m-test-engine" || tc.Dist != "/opt/yottadb" {
		t.Errorf("TransportConfig = %+v", tc)
	}
}

func TestTransportConfig_MapsRemote(t *testing.T) {
	c := &Conn{Transport: "remote", Host: "h", Port: 2222, User: "u", Identity: "/k", EnvFile: "/env"}
	tc := c.TransportConfig()
	if tc.Transport != "remote" || tc.Host != "h" || tc.Port != 2222 ||
		tc.User != "u" || tc.Identity != "/k" || tc.EnvFile != "/env" {
		t.Errorf("TransportConfig = %+v", tc)
	}
}
