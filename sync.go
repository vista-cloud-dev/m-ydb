package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/vista-cloud-dev/m-ydb/clikit"
	"github.com/vista-cloud-dev/m-ydb/internal/config"
	"github.com/vista-cloud-dev/m-ydb/internal/manifest"
	"github.com/vista-cloud-dev/m-ydb/internal/mirror"
	"github.com/vista-cloud-dev/m-ydb/internal/source"
)

// syncCmd is the sync axis (driver-contract §5.2) — routine source ↔ instance.
// For YottaDB the source IS the .m file on disk, so sync is filesystem-native
// over $ydb_routines: list/pull/status/verify materialize and track a local
// mirror, and --filter matches the bare (extension-stripped) routine name. The
// write verbs (push/deploy/diff/rm) land in the next M2 slice; caps grows to
// advertise each as it does (caps is honest — advertised == implemented).
type syncCmd struct {
	List   syncListCmd   `cmd:"" name:"list" help:"List routine source names ($ydb_routines) — connectivity + inventory (no writes)."`
	Pull   syncPullCmd   `cmd:"" name:"pull" help:"Materialize routine source → mirror, incremental via the manifest."`
	Status syncStatusCmd `cmd:"" name:"status" help:"Diff source vs. local manifest: new / changed / deleted (exit 3 on drift)."`
	Verify syncVerifyCmd `cmd:"" name:"verify" help:"Re-hash mirror files against the manifest (exit 3 on mismatch)."`
}

// --- list --------------------------------------------------------------------

type syncListCmd struct{}

type syncListResult struct {
	Count    int      `json:"count"`
	Docnames []string `json:"docnames"`
}

func (syncListCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	store, err := conn.SourceStore()
	if err != nil {
		return usageErr(err)
	}
	names, _, err := scopeSource(context.Background(), store, conn.Filter)
	if err != nil {
		return err
	}
	return cc.Result(syncListResult{Count: len(names), Docnames: names}, func() {
		cc.Title(fmt.Sprintf("%d routine(s)", len(names)))
		for _, n := range names {
			fmt.Fprintln(cc.Stdout, "  "+n)
		}
	})
}

// --- pull --------------------------------------------------------------------

type syncPullCmd struct {
	Full bool `help:"Ignore the manifest; re-copy every routine."`
}

type syncPullResult struct {
	Mirror    string `json:"mirror"`
	Pulled    int    `json:"pulled"`
	Unchanged int    `json:"unchanged"`
	Deleted   int    `json:"deleted"`
	Bytes     int    `json:"bytes"`
	DryRun    bool   `json:"dryRun,omitempty"`
}

func (c *syncPullCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	store, err := conn.SourceStore()
	if err != nil {
		return usageErr(err)
	}
	layout := conn.Layout()
	ctx := context.Background()

	man, err := manifest.Load(layout.ManifestPath())
	if err != nil {
		return runtimeErr(err)
	}
	if man == nil {
		man = manifest.New()
	}

	srcTS, err := sourceTS(ctx, store, conn.Filter)
	if err != nil {
		return err
	}
	diff := manifest.Compare(srcTS, man)

	toFetch := diff.ToPull()
	if c.Full {
		toFetch = make([]string, 0, len(srcTS))
		for n := range srcTS {
			toFetch = append(toFetch, n)
		}
		sort.Strings(toFetch)
	}
	toDelete := diff.Deleted

	if conn.DryRun {
		return cc.Result(syncPullResult{
			Mirror: layout.Root, Pulled: len(toFetch),
			Unchanged: len(diff.Unchanged), Deleted: len(toDelete), DryRun: true,
		}, func() {
			cc.Title("pull plan (dry run)")
			cc.KV(
				[2]string{"to copy", fmt.Sprint(len(toFetch))},
				[2]string{"unchanged", fmt.Sprint(len(diff.Unchanged))},
				[2]string{"to delete", fmt.Sprint(len(toDelete))},
				[2]string{"mirror", layout.Root},
			)
		})
	}

	totalBytes := 0
	for _, name := range toFetch {
		body, err := store.Read(ctx, name)
		if err != nil {
			return runtimeErr(fmt.Errorf("read %s: %w", name, err))
		}
		wr, err := mirror.WriteRoutine(layout.RoutinePath(name), body)
		if err != nil {
			return runtimeErr(fmt.Errorf("write %s: %w", name, err))
		}
		man.Routines[name] = manifest.Entry{SourceTS: srcTS[name], SHA256: wr.SHA256, Bytes: wr.Bytes}
		totalBytes += wr.Bytes
	}
	for _, name := range toDelete {
		if rmErr := os.Remove(layout.RoutinePath(name)); rmErr != nil && !os.IsNotExist(rmErr) {
			return runtimeErr(rmErr)
		}
		delete(man.Routines, name)
	}
	man.PulledAt = time.Now().UTC().Format(time.RFC3339)
	if err := manifest.Save(layout.ManifestPath(), man); err != nil {
		return runtimeErr(err)
	}

	return cc.Result(syncPullResult{
		Mirror: layout.Root, Pulled: len(toFetch),
		Unchanged: len(diff.Unchanged), Deleted: len(toDelete), Bytes: totalBytes,
	}, func() {
		cc.Title("pull complete")
		cc.KV(
			[2]string{"copied", fmt.Sprint(len(toFetch))},
			[2]string{"unchanged", fmt.Sprint(len(diff.Unchanged))},
			[2]string{"deleted", fmt.Sprint(len(toDelete))},
			[2]string{"bytes", fmt.Sprint(totalBytes)},
			[2]string{"mirror", layout.Root},
		)
		fmt.Fprintln(cc.Stdout, cc.Success("mirror updated"))
	})
}

// --- status ------------------------------------------------------------------

type syncStatusCmd struct{}

type syncStatusResult struct {
	New       []string `json:"new"`
	Changed   []string `json:"changed"`
	Deleted   []string `json:"deleted"`
	Unchanged int      `json:"unchanged"`
	Drift     bool     `json:"drift"`
}

func (syncStatusCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	store, err := conn.SourceStore()
	if err != nil {
		return usageErr(err)
	}
	layout := conn.Layout()
	man, err := manifest.Load(layout.ManifestPath())
	if err != nil {
		return runtimeErr(err)
	}
	srcTS, err := sourceTS(context.Background(), store, conn.Filter)
	if err != nil {
		return err
	}
	d := manifest.Compare(srcTS, man)
	res := syncStatusResult{
		New: nonNil(d.New), Changed: nonNil(d.Changed), Deleted: nonNil(d.Deleted),
		Unchanged: len(d.Unchanged), Drift: d.Drift(),
	}
	return reportDrift(cc, res, func() {
		cc.Title("sync status")
		renderDiff(cc, d)
	}, d.Drift(), "DRIFT",
		fmt.Sprintf("%d new, %d changed, %d deleted — mirror out of sync", len(d.New), len(d.Changed), len(d.Deleted)),
		"run 'm-ydb sync pull' to update the mirror")
}

// --- verify ------------------------------------------------------------------

type syncVerifyCmd struct{}

type syncVerifyResult struct {
	Checked    int      `json:"checked"`
	OK         int      `json:"ok"`
	Mismatches []string `json:"mismatches"`
	Missing    []string `json:"missing"`
}

func (syncVerifyCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	layout := conn.Layout()
	man, err := manifest.Load(layout.ManifestPath())
	if err != nil {
		return runtimeErr(err)
	}
	if man == nil {
		return clikit.Fail(clikit.ExitRuntime, "NO_MANIFEST",
			"no manifest at "+layout.ManifestPath()+"; run 'm-ydb sync pull' first", "")
	}

	names, err := scopeManifest(man, conn.Filter)
	if err != nil {
		return err
	}
	var mismatch, missing []string
	okCount := 0
	for _, name := range names {
		e := man.Routines[name]
		sum, n, hErr := mirror.HashFile(layout.RoutinePath(name))
		switch {
		case os.IsNotExist(hErr):
			missing = append(missing, name)
		case hErr != nil:
			return runtimeErr(hErr)
		case sum != e.SHA256 || n != e.Bytes:
			mismatch = append(mismatch, name)
		default:
			okCount++
		}
	}
	drift := len(mismatch)+len(missing) > 0
	res := syncVerifyResult{
		Checked: len(names), OK: okCount, Mismatches: nonNil(mismatch), Missing: nonNil(missing),
	}
	return reportDrift(cc, res, func() {
		cc.Title("verify mirror")
		for _, n := range missing {
			fmt.Fprintln(cc.Stdout, cc.Failure("missing  "+n))
		}
		for _, n := range mismatch {
			fmt.Fprintln(cc.Stdout, cc.Warning("mismatch "+n))
		}
		if !drift {
			fmt.Fprintln(cc.Stdout, cc.Success(fmt.Sprintf("verified %d routine(s) against the manifest", okCount)))
		}
	}, drift, "MISMATCH",
		fmt.Sprintf("%d mismatched, %d missing — mirror does not match the manifest", len(mismatch), len(missing)),
		"re-run 'm-ydb sync pull' or investigate tampering")
}

// --- shared helpers ----------------------------------------------------------

// scopeSource lists the source routine names passing the bare-name filter,
// sorted, and returns the name→TS map alongside.
func scopeSource(ctx context.Context, store source.Store, glob string) (names []string, ts map[string]string, err error) {
	ts, err = sourceTS(ctx, store, glob)
	if err != nil {
		return nil, nil, err
	}
	names = make([]string, 0, len(ts))
	for n := range ts {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, ts, nil
}

// sourceTS lists the source store and returns a name→TS map of the routines
// passing the bare-name filter.
func sourceTS(ctx context.Context, store source.Store, glob string) (map[string]string, error) {
	rts, err := store.List(ctx)
	if err != nil {
		return nil, runtimeErr(err)
	}
	out := make(map[string]string, len(rts))
	for _, r := range rts {
		ok, err := source.Match(r.Name, glob)
		if err != nil {
			return nil, usageErr(err)
		}
		if ok {
			out[r.Name] = r.TS
		}
	}
	return out, nil
}

// scopeManifest returns the manifest's routine names (sorted) passing the filter.
func scopeManifest(man *manifest.Manifest, glob string) ([]string, error) {
	out := make([]string, 0, len(man.Routines))
	for n := range man.Routines {
		ok, err := source.Match(n, glob)
		if err != nil {
			return nil, usageErr(err)
		}
		if ok {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out, nil
}

// reportDrift emits a command result and signals drift/mismatch via exit 3. The
// full report is always written; CI gates on the exit code.
func reportDrift(cc *clikit.Context, data any, text func(), drift bool, code, summary, hint string) error {
	if err := cc.Result(data, text); err != nil {
		return err
	}
	if drift {
		return clikit.Fail(clikit.ExitCheck, code, summary, hint)
	}
	return nil
}

func renderDiff(cc *clikit.Context, d manifest.Diff) {
	section := func(label string, names []string) {
		if len(names) == 0 {
			return
		}
		cc.Rule(label)
		for _, n := range names {
			fmt.Fprintln(cc.Stdout, "  "+n)
		}
	}
	section("new", d.New)
	section("changed", d.Changed)
	section("deleted", d.Deleted)
	if !d.Drift() {
		fmt.Fprintln(cc.Stdout, cc.Success(fmt.Sprintf("in sync — %d routine(s)", len(d.Unchanged))))
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func runtimeErr(err error) error {
	var e *clikit.Error
	if errors.As(err, &e) {
		return e // already a deterministic clikit error — don't re-wrap
	}
	return clikit.Fail(clikit.ExitRuntime, "RUNTIME", err.Error(), "")
}

func usageErr(err error) error {
	var e *clikit.Error
	if errors.As(err, &e) {
		return e
	}
	return clikit.Fail(clikit.ExitUsage, "BAD_CONFIG", err.Error(), "set flags or M_YDB_* env vars")
}
