package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vista-cloud-dev/m-ydb/clikit"
	"github.com/vista-cloud-dev/m-ydb/internal/config"
	"github.com/vista-cloud-dev/m-ydb/internal/manifest"
	"github.com/vista-cloud-dev/m-ydb/internal/mirror"
	"github.com/vista-cloud-dev/m-ydb/internal/source"
	"github.com/vista-cloud-dev/m-ydb/internal/udiff"
)

// syncCmd is the sync axis (driver-contract §5.2) — routine source ↔ instance.
// For YottaDB the source IS the .m file on disk, so sync is filesystem-native
// over $ydb_routines: the read verbs (list/pull/status/verify) materialize and
// track a local mirror; the write verbs (diff/push/deploy/rm) reconcile the
// instance from local source — push is conflict-checked against the manifest
// (exit 4 unless --force), deploy installs a library with a common-prefix prune
// guard. --filter matches the bare (extension-stripped) routine name.
type syncCmd struct {
	List   syncListCmd   `cmd:"" name:"list" help:"List routine source names ($ydb_routines) — connectivity + inventory (no writes)."`
	Pull   syncPullCmd   `cmd:"" name:"pull" help:"Materialize routine source → mirror, incremental via the manifest."`
	Status syncStatusCmd `cmd:"" name:"status" help:"Diff source vs. local manifest: new / changed / deleted (exit 3 on drift)."`
	Verify syncVerifyCmd `cmd:"" name:"verify" help:"Re-hash mirror files against the manifest (exit 3 on mismatch)."`
	Diff   syncDiffCmd   `cmd:"" name:"diff" help:"Unified diff of one routine: instance vs. mirror (or vs. --from)."`
	Push   syncPushCmd   `cmd:"" name:"push" help:"Write routines back to the instance from the mirror or --from (conflict-checked; exit 4 unless --force)."`
	Deploy syncDeployCmd `cmd:"" name:"deploy" help:"Install a routine-source library into the instance; --prune true-syncs under a common-prefix guard."`
	Rm     syncRmCmd     `cmd:"" name:"rm" help:"Remove a routine from the instance (and the mirror/manifest)."`
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

// --- diff --------------------------------------------------------------------

type syncDiffCmd struct {
	Name string `arg:"" help:"Routine to diff (bare name or NAME.m)."`
	From string `help:"Compare the instance against this directory instead of the mirror." placeholder:"DIR"`
}

type syncDiffResult struct {
	Unified string `json:"unified"`
}

func (c *syncDiffCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	store, err := conn.SourceStore()
	if err != nil {
		return usageErr(err)
	}
	name := routineFile(c.Name)
	ctx := context.Background()

	instBytes, ierr := store.Read(ctx, name)
	if ierr != nil && !os.IsNotExist(ierr) {
		return runtimeErr(ierr)
	}
	localPath := conn.Layout().RoutinePath(name)
	bLabel := "mirror/" + name
	if c.From != "" {
		localPath = filepath.Join(c.From, name)
		bLabel = filepath.Join(c.From, name)
	}
	localBytes, lerr := os.ReadFile(localPath)
	if lerr != nil && !os.IsNotExist(lerr) {
		return runtimeErr(lerr)
	}

	// Normalize both sides so line-ending differences don't show as changes.
	a := udiff.SplitLines(string(mirror.Normalize(instBytes)))
	b := udiff.SplitLines(string(mirror.Normalize(localBytes)))
	u := udiff.Unified("instance/"+name, bLabel, a, b)

	return cc.Result(syncDiffResult{Unified: u}, func() {
		if u == "" {
			fmt.Fprintln(cc.Stdout, cc.Success(name+": no differences"))
			return
		}
		fmt.Fprint(cc.Stdout, u)
	})
}

// --- push --------------------------------------------------------------------

type syncPushCmd struct {
	From  string `help:"Push .m files from this directory instead of the mirror." placeholder:"DIR"`
	Force bool   `help:"Override the instance conflict guard (overwrite out-of-band edits)."`
}

type syncPushResult struct {
	Pushed   []string `json:"pushed"`
	Compiled int      `json:"compiled"`
	DryRun   bool     `json:"dryRun,omitempty"`
}

func (c *syncPushCmd) Run(cc *clikit.Context, conn *config.Conn) error {
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

	srcDir := layout.Root
	if c.From != "" {
		srcDir = c.From
	}
	names, err := dirRoutines(srcDir, conn.Filter)
	if err != nil {
		return err
	}

	inst, err := store.List(ctx)
	if err != nil {
		return runtimeErr(err)
	}
	instTS := make(map[string]string, len(inst))
	for _, r := range inst {
		instTS[r.Name] = r.TS
	}

	var conflicts []string
	for _, n := range names {
		ts, exists := instTS[n]
		if cf := manifest.CheckConflict(man, n, ts, exists); cf.Kind != manifest.ConflictNone {
			conflicts = append(conflicts, fmt.Sprintf("%s (%s)", n, cf.Kind))
		}
	}
	if len(conflicts) > 0 && !c.Force {
		return clikit.Fail(clikit.ExitRefused, "CONFLICT",
			fmt.Sprintf("%d routine(s) changed on the instance since pull: %s", len(conflicts), strings.Join(conflicts, ", ")),
			"re-pull and merge, or pass --force to overwrite")
	}

	if conn.DryRun {
		return cc.Result(syncPushResult{Pushed: nonNil(names), DryRun: true}, func() {
			cc.Title("push plan (dry run)")
			cc.KV([2]string{"to push", fmt.Sprint(len(names))}, [2]string{"from", srcDir})
		})
	}

	if man == nil {
		man = manifest.New()
	}
	var pushed []string
	for _, n := range names {
		raw, err := os.ReadFile(filepath.Join(srcDir, n))
		if err != nil {
			return runtimeErr(err)
		}
		canonical := mirror.Normalize(raw)
		// Land it in the mirror too (so the mirror reflects what we pushed and
		// verify stays coherent), then write it to the instance.
		wr, err := mirror.WriteRoutine(layout.RoutinePath(n), canonical)
		if err != nil {
			return runtimeErr(err)
		}
		rt, err := store.Write(ctx, n, canonical)
		if err != nil {
			return runtimeErr(err)
		}
		man.Routines[n] = manifest.Entry{SourceTS: rt.TS, SHA256: wr.SHA256, Bytes: wr.Bytes}
		pushed = append(pushed, n)
	}
	if err := manifest.Save(layout.ManifestPath(), man); err != nil {
		return runtimeErr(err)
	}

	return cc.Result(syncPushResult{Pushed: nonNil(pushed)}, func() {
		cc.Title("push complete")
		cc.KV([2]string{"pushed", fmt.Sprint(len(pushed))}, [2]string{"from", srcDir})
		fmt.Fprintln(cc.Stdout, cc.Success("instance updated (YottaDB compiles on next use)"))
	})
}

// --- deploy ------------------------------------------------------------------

type syncDeployCmd struct {
	Dir   string `arg:"" help:"Directory of .m routine source to install."`
	Prune bool   `help:"Remove instance routines under the library's common name-prefix that are absent from it (true sync)."`
}

type syncDeployResult struct {
	Installed []string `json:"installed"`
	Pruned    []string `json:"pruned"`
	DryRun    bool     `json:"dryRun,omitempty"`
}

func (c *syncDeployCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	store, err := conn.SourceStore()
	if err != nil {
		return usageErr(err)
	}
	ctx := context.Background()

	names, err := dirRoutines(c.Dir, conn.Filter)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return usageErr(fmt.Errorf("no .m routines found in %s", c.Dir))
	}

	var prune []string
	if c.Prune {
		prefix := commonPrefix(bareNames(names))
		if prefix == "" {
			return clikit.Fail(clikit.ExitRefused, "PRUNE_SCOPE",
				"cannot determine a safe prune scope: the library routines share no common name prefix",
				"narrow the library or scope with --filter")
		}
		installed := make(map[string]bool, len(names))
		for _, n := range names {
			installed[n] = true
		}
		inst, err := store.List(ctx)
		if err != nil {
			return runtimeErr(err)
		}
		for _, r := range inst {
			if installed[r.Name] || !strings.HasPrefix(source.BareName(r.Name), prefix) {
				continue
			}
			ok, mErr := source.Match(r.Name, conn.Filter)
			if mErr != nil {
				return usageErr(mErr)
			}
			if ok {
				prune = append(prune, r.Name)
			}
		}
		sort.Strings(prune)
	}

	if conn.DryRun {
		return cc.Result(syncDeployResult{Installed: nonNil(names), Pruned: nonNil(prune), DryRun: true}, func() {
			cc.Title("deploy plan (dry run)")
			cc.KV([2]string{"install", fmt.Sprint(len(names))}, [2]string{"prune", fmt.Sprint(len(prune))})
		})
	}

	for _, n := range names {
		raw, err := os.ReadFile(filepath.Join(c.Dir, n))
		if err != nil {
			return runtimeErr(err)
		}
		if _, err := store.Write(ctx, n, mirror.Normalize(raw)); err != nil {
			return runtimeErr(err)
		}
	}
	for _, n := range prune {
		if err := store.Remove(ctx, n); err != nil {
			return runtimeErr(err)
		}
	}

	return cc.Result(syncDeployResult{Installed: nonNil(names), Pruned: nonNil(prune)}, func() {
		cc.Title("deploy complete")
		cc.KV([2]string{"installed", fmt.Sprint(len(names))}, [2]string{"pruned", fmt.Sprint(len(prune))})
		fmt.Fprintln(cc.Stdout, cc.Success("library installed"))
	})
}

// --- rm ----------------------------------------------------------------------

type syncRmCmd struct {
	Name string `arg:"" help:"Routine to remove (bare name or NAME.m)."`
}

type syncRmResult struct {
	Removed []string `json:"removed"`
	DryRun  bool     `json:"dryRun,omitempty"`
}

func (c *syncRmCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	store, err := conn.SourceStore()
	if err != nil {
		return usageErr(err)
	}
	name := routineFile(c.Name)
	ctx := context.Background()

	_, rerr := store.Read(ctx, name)
	if rerr != nil && !os.IsNotExist(rerr) {
		return runtimeErr(rerr)
	}
	exists := rerr == nil
	var removed []string
	if exists {
		removed = []string{name}
	}

	if conn.DryRun {
		return cc.Result(syncRmResult{Removed: nonNil(removed), DryRun: true}, func() {
			cc.Title("rm plan (dry run)")
			fmt.Fprintln(cc.Stdout, "  would remove "+strings.Join(nonNil(removed), ", "))
		})
	}

	if exists {
		if err := store.Remove(ctx, name); err != nil {
			return runtimeErr(err)
		}
		layout := conn.Layout()
		if err := os.Remove(layout.RoutinePath(name)); err != nil && !os.IsNotExist(err) {
			return runtimeErr(err)
		}
		man, mErr := manifest.Load(layout.ManifestPath())
		if mErr != nil {
			return runtimeErr(mErr)
		}
		if man != nil {
			if _, ok := man.Routines[name]; ok {
				delete(man.Routines, name)
				if err := manifest.Save(layout.ManifestPath(), man); err != nil {
					return runtimeErr(err)
				}
			}
		}
	}

	return cc.Result(syncRmResult{Removed: nonNil(removed)}, func() {
		if len(removed) == 0 {
			fmt.Fprintln(cc.Stdout, cc.Warning(name+": not present on the instance"))
			return
		}
		fmt.Fprintln(cc.Stdout, cc.Success("removed "+name))
	})
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

// routineFile normalizes a routine argument to its filename: a bare "FOO"
// becomes "FOO.m"; "FOO.m" is left as-is.
func routineFile(name string) string {
	if filepath.Ext(name) != ".m" {
		return name + ".m"
	}
	return name
}

// dirRoutines lists the .m files in dir whose bare name passes the filter,
// sorted. A missing directory is a usage error.
func dirRoutines(dir, glob string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, usageErr(err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".m" {
			continue
		}
		ok, mErr := source.Match(name, glob)
		if mErr != nil {
			return nil, usageErr(mErr)
		}
		if ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// bareNames strips the .m extension from each routine filename.
func bareNames(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = source.BareName(n)
	}
	return out
}

// commonPrefix returns the longest common string prefix of the names (empty if
// they share none) — the safety scope for deploy --prune.
func commonPrefix(names []string) string {
	if len(names) == 0 {
		return ""
	}
	prefix := names[0]
	for _, n := range names[1:] {
		for !strings.HasPrefix(n, prefix) {
			prefix = prefix[:len(prefix)-1]
			if prefix == "" {
				return ""
			}
		}
	}
	return prefix
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
