package internal

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func glob(pattern string) []string {
	matches, _ := filepath.Glob(pattern)
	return matches
}

func RepoGuideDir() string {
	return home(".repoguide")
}

// RepoGuideDBPath returns the path to the local SQLite database.
func RepoGuideDBPath() (string, error) {
	dir := RepoGuideDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create repoguide dir: %w", err)
	}
	return filepath.Join(dir, "repoguide.db"), nil
}

func DirSize(path string) (int64, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, false, nil
		}
		return 0, false, err
	}
	if !info.IsDir() {
		return info.Size(), true, nil
	}

	var total int64
	err = filepath.WalkDir(path, func(_ string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		return 0, true, err
	}
	return total, true, nil
}

func FormatBytes(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}

	value := float64(size) / unit
	suffix := "KB"
	for _, next := range []string{"MB", "GB", "TB", "PB"} {
		if value < unit {
			break
		}
		value /= unit
		suffix = next
	}

	if value >= 10 || value == float64(int64(value)) {
		return fmt.Sprintf("%.0f %s", value, suffix)
	}
	return fmt.Sprintf("%.1f %s", value, suffix)
}
