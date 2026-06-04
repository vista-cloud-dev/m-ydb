package manifest

import "fmt"

// ConflictKind classifies how the instance's current state of a routine relates
// to the state captured in the manifest at pull time. Any kind other than
// ConflictNone means another writer touched the instance since we pulled, so a
// push would clobber that change — push refuses (exit 4) unless --force.
type ConflictKind int

const (
	// ConflictNone — safe to push: the instance matches the manifest (an
	// unchanged update), or the routine is new locally and absent on the
	// instance (a clean create).
	ConflictNone ConflictKind = iota
	// ConflictChanged — the instance source TS differs from the pulled one: an
	// out-of-band edit.
	ConflictChanged
	// ConflictDeleted — recorded at pull, but gone from the instance now.
	ConflictDeleted
	// ConflictExists — a routine with no manifest entry already exists on the
	// instance: pushing would overwrite a routine we never pulled.
	ConflictExists
)

func (k ConflictKind) String() string {
	switch k {
	case ConflictChanged:
		return "changed-on-instance"
	case ConflictDeleted:
		return "deleted-on-instance"
	case ConflictExists:
		return "exists-on-instance"
	default:
		return "none"
	}
}

// Conflict is the result of a conflict-check for one routine.
type Conflict struct {
	Name    string
	Kind    ConflictKind
	Message string
}

// CheckConflict compares the instance's current state of name (its source TS,
// and whether it exists at all) against the manifest entry recorded at the last
// pull. instTS is the live instance timestamp (empty if the routine does not
// exist); exists reports whether the instance currently has the routine.
//
//   - entry present + instance matches  → ConflictNone (safe update)
//   - entry present + instance differs   → ConflictChanged
//   - entry present + instance gone       → ConflictDeleted
//   - no entry      + instance absent     → ConflictNone (clean create)
//   - no entry      + instance present    → ConflictExists
func CheckConflict(m *Manifest, name, instTS string, exists bool) Conflict {
	var entry Entry
	var recorded bool
	if m != nil {
		entry, recorded = m.Routines[name]
	}
	switch {
	case recorded && !exists:
		return Conflict{name, ConflictDeleted,
			fmt.Sprintf("%s was deleted on the instance since pull (had ts %q)", name, entry.SourceTS)}
	case recorded && instTS != entry.SourceTS:
		return Conflict{name, ConflictChanged,
			fmt.Sprintf("%s changed on the instance since pull (pulled ts %q, now %q)", name, entry.SourceTS, instTS)}
	case !recorded && exists:
		return Conflict{name, ConflictExists,
			fmt.Sprintf("%s already exists on the instance but is not in the manifest (never pulled)", name)}
	default:
		return Conflict{name, ConflictNone, ""}
	}
}
