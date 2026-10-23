package deploy

import (
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestMigrationsHaveUniqueVersions proves that no two migration files claim
// the same version.
//
// The version is the only handle golang-migrate has on a migration, and it
// records a single number: the highest version it has applied. Two files
// that share a version therefore do not both run, and a database that already
// passed that version never runs either of them. A migration that is added
// with a version below the current maximum is skipped the same way, silently,
// and the database keeps the older shape while the code queries the newer one.
//
// Both shapes have shipped as production incidents, so the rule is enforced
// here rather than left to review.
func TestMigrationsHaveUniqueVersions(t *testing.T) {
	entries, err := fs.ReadDir(Migrations, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	seen := make(map[uint64]string)
	var versions []uint64
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		prefix, _, ok := strings.Cut(name, "_")
		if !ok {
			t.Errorf("migration %q is not named <version>_<name>.up.sql", name)
			continue
		}
		version, err := strconv.ParseUint(prefix, 10, 64)
		if err != nil {
			t.Errorf("migration %q does not start with a numeric version: %v", name, err)
			continue
		}
		if previous, exists := seen[version]; exists {
			t.Errorf("migration version %d is claimed by both %q and %q: "+
				"only one of them can ever run, and a database that already passed "+
				"version %d runs neither",
				version, previous, name, version)
			continue
		}
		seen[version] = name
		versions = append(versions, version)
	}
	if len(versions) == 0 {
		t.Fatal("no migrations were found in the embedded file system")
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })

	// The highest version has to be a timestamp. A zero-padded 0xxx version is
	// numerically far below a timestamp version, so a 0xxx migration added
	// after the timestamp series started would be skipped by every database
	// that already applied a timestamp migration.
	highest := versions[len(versions)-1]
	if highest < 20260101000000 {
		t.Errorf("the highest migration version is %d; it must be a timestamp version "+
			"(YYYYMMDDHHMMSS) so that a later migration cannot be numbered below it "+
			"and be skipped by an already-migrated database", highest)
	}
}

// TestMigrationsHaveMatchingDownFiles proves that every up migration has the
// down migration that reverses it.
//
// A missing down file leaves the migration set without a way back, and
// golang-migrate treats a pair it cannot read as a hard error at startup.
func TestMigrationsHaveMatchingDownFiles(t *testing.T) {
	entries, err := fs.ReadDir(Migrations, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	up := make(map[string]struct{})
	down := make(map[string]struct{})
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		switch {
		case strings.HasSuffix(name, ".up.sql"):
			up[strings.TrimSuffix(name, ".up.sql")] = struct{}{}
		case strings.HasSuffix(name, ".down.sql"):
			down[strings.TrimSuffix(name, ".down.sql")] = struct{}{}
		}
	}
	for name := range up {
		if _, ok := down[name]; !ok {
			t.Errorf("migration %q has no matching .down.sql", name)
		}
	}
	for name := range down {
		if _, ok := up[name]; !ok {
			t.Errorf("migration %q has a .down.sql but no .up.sql", name)
		}
	}
}

// TestMigrationVersionsAreParseable proves that every migration file is named
// after a version the migration runner can order.
//
// The runner compares versions as numbers. A file whose name does not start
// with a number is ordered unpredictably, so the whole set can be applied in
// the wrong order.
func TestMigrationVersionsAreParseable(t *testing.T) {
	entries, err := fs.ReadDir(Migrations, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		prefix, _, ok := strings.Cut(name, "_")
		if !ok {
			t.Errorf("migration %q is not named <version>_<name>.up.sql", name)
			continue
		}
		if _, err := strconv.ParseUint(prefix, 10, 64); err != nil {
			t.Errorf("migration %q does not start with a numeric version: %v", name, err)
		}
		if prefix != fmt.Sprintf("%04d", mustParse(t, prefix)) && len(prefix) != 4 {
			t.Errorf("migration %q uses a %d-digit version; keep the zero-padded four-digit "+
				"form so the series reads in order", name, len(prefix))
		}
	}
}

func mustParse(t *testing.T, value string) uint64 {
	t.Helper()
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return parsed
}
