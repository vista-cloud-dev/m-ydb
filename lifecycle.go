package main

import (
	"context"
	"fmt"
	"time"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
	"github.com/vista-cloud-dev/m-ydb/clikit"
	"github.com/vista-cloud-dev/m-ydb/internal/config"
)

// lifecycleCmd is the lifecycle axis (driver-contract §5.1): manage the engine
// instance. YottaDB is daemonless and file-based, so "the instance" is the
// global directory (.gld) + database (.dat) + environment; up ≈ ensure those and
// clear stale shared memory, down ≈ mupip rundown. M1a wires the health surface
// (status/--probe + wait); the mupip/gde lifecycle (up/down/restart/provision/
// destroy) lands in M1b, and caps grows to advertise it then.
type lifecycleCmd struct {
	Status lifeStatusCmd `cmd:"" help:"Report running/healthy/version; --probe for a terse CI readiness gate."`
	Wait   lifeWaitCmd   `cmd:"" help:"Block until the engine is healthy or --timeout elapses (exit 6 on timeout)."`
}

// The lifecycle status/state payloads are SDK-owned so m-ydb and m-iris emit
// identical JSON m-cli reads.
type lifecycleStatus = mdriver.Status

// probe runs the readiness probe and builds the status snapshot. A transport
// launch failure (e.g. missing binary) is reported as not-running rather than a
// hard error — `meta doctor` diagnoses the cause; status just reports state.
func probe(ctx context.Context, conn *config.Conn) lifecycleStatus {
	s := conn.NewSession()
	st := lifecycleStatus{Transport: conn.Transport}
	start := time.Now()
	h, err := s.Health(ctx)
	st.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		return st // running=healthy=false
	}
	st.Running, st.Healthy = h.Running, h.Healthy
	if h.Healthy {
		if v, verr := s.Version(ctx); verr == nil {
			st.Version = v
		}
	}
	return st
}

func notReady() error {
	return clikit.Fail(clikit.ExitUnreachable, "NOT_READY",
		"engine not ready", "run `m-ydb meta doctor` for the cause")
}

// --- lifecycle status / --probe ---------------------------------------------

type lifeStatusCmd struct {
	Probe bool `help:"Terse readiness gate: {running, healthy, latencyMs}; exit 0 healthy, 6 not ready."`
}

func (c lifeStatusCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	if err := conn.Validate(); err != nil {
		return clikit.Fail(clikit.ExitUsage, "BAD_CONN", err.Error(), "")
	}
	st := probe(context.Background(), conn)
	if c.Probe {
		terse := lifecycleStatus{Transport: st.Transport, Running: st.Running, Healthy: st.Healthy, LatencyMs: st.LatencyMs}
		if rerr := cc.Result(terse, func() {
			cc.KV([2]string{"healthy", fmt.Sprint(terse.Healthy)}, [2]string{"latencyMs", fmt.Sprint(terse.LatencyMs)})
		}); rerr != nil {
			return rerr
		}
		if !st.Healthy {
			return notReady()
		}
		return nil
	}
	return cc.Result(st, func() {
		cc.Title("engine status — " + st.Transport)
		cc.KV(
			[2]string{"running", fmt.Sprint(st.Running)},
			[2]string{"healthy", fmt.Sprint(st.Healthy)},
			[2]string{"version", st.Version},
		)
	})
}

// --- lifecycle wait ----------------------------------------------------------

type lifeWaitCmd struct {
	Timeout int `default:"60" help:"Seconds to wait for readiness before giving up (exit 6)."`
}

func (c *lifeWaitCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	if err := conn.Validate(); err != nil {
		return clikit.Fail(clikit.ExitUsage, "BAD_CONN", err.Error(), "")
	}
	deadline := time.Now().Add(time.Duration(c.Timeout) * time.Second)
	const poll = 100 * time.Millisecond
	var st lifecycleStatus
	for {
		st = probe(context.Background(), conn)
		if st.Healthy {
			return cc.Result(st, func() {
				fmt.Fprintln(cc.Stdout, cc.Success(fmt.Sprintf("healthy in %dms", st.LatencyMs)))
			})
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(poll)
	}
	_ = cc.Result(st, nil)
	return clikit.Fail(clikit.ExitUnreachable, "WAIT_TIMEOUT",
		fmt.Sprintf("engine not healthy after %ds", c.Timeout), "check the engine is up; run `m-ydb meta doctor`")
}
