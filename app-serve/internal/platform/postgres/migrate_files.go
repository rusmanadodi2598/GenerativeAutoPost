// Package postgres runs numbered SQL migrations and journals each one.
//
// @file      internal/platform/postgres/migrate_files.go
// @for       Migration directory scanning and version ordering.
// @uses      os, sort, strings
// @reason    Keeps the runner file under the line limit while the scan stays testable.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     repository
// @stability stable
// @since     2026-10-16
package postgres

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// migrationFiles maps version to filename for the given direction.
func migrationFiles(dir, direction string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("postgres: read migrations dir: %w", err)
	}
	suffix := "." + direction + ".sql"
	files := make(map[string]string)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, suffix) {
			continue
		}
		files[strings.TrimSuffix(name, suffix)] = name
	}
	return files, nil
}

// sortedVersions orders versions by filename, which carries the number prefix.
func sortedVersions(files map[string]string) []string {
	versions := make([]string, 0, len(files))
	for version := range files {
		versions = append(versions, version)
	}
	sort.Strings(versions)
	return versions
}
