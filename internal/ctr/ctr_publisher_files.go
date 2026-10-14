package ctr

import (
	os "os"
	filepath "path/filepath"
	strings "strings"
	time "time"
)

type ctrPublisherFiles struct{ dir string }

func (files ctrPublisherFiles) PublisherFile(name string, _ time.Time) ([]byte, string) {
	base := filepath.Base(name)
	// These names belong to the retained Diablo generators and their aliases.
	// Block old physical outputs too: removing generation alone would leak them.
	switch strings.ToLower(base) {
	case "config.txt", "seasons.txt", "seasons_config.txt", "blacklist.txt", "blacklist_config.txt":
		return nil, ""
	}
	if strings.HasPrefix(strings.ToLower(base), "challengerift_") {
		return nil, ""
	}
	entries, err := os.ReadDir(files.dir)
	if err != nil {
		return nil, ""
	}
	for _, entry := range entries {
		if strings.EqualFold(entry.Name(), base) {
			path := filepath.Join(files.dir, entry.Name())
			if b, err := os.ReadFile(path); err == nil {
				return b, path
			}
		}
	}
	return nil, ""
}
