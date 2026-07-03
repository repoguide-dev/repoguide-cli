package sessionimport

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

// Newer OpenCode builds persist sessions to a SQLite database
// (opencode.db, or a channel-specific opencode-<channel>.db) instead of the
// loose JSON files under storage/. This materializes DB rows into the same
// storage/session|message/part/*.json layout readOpencodeSession and
// buildOpencodeSessionEvents already read, under our own cache dir so we
// never touch OpenCode's real data directory.
// ponytail: read-only mirror of the "dev" branch schema (packages/core/src/session/sql.ts);
// if a shipped release renames these columns, materializeOpencodeDB just finds nothing.

func opencodeDataDirs() []string {
	if raw := strings.TrimSpace(os.Getenv("OPENCODE_DATA_DIR")); raw != "" {
		var dirs []string
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if info, err := os.Stat(part); err == nil && info.IsDir() {
				dirs = append(dirs, part)
			}
		}
		return dirs
	}
	dir := home(".local", "share", "opencode")
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return []string{dir}
	}
	return nil
}

func opencodeDBPath(dataDir string) string {
	defaultPath := filepath.Join(dataDir, "opencode.db")
	if info, err := os.Stat(defaultPath); err == nil && !info.IsDir() {
		return defaultPath
	}
	matches := glob(filepath.Join(dataDir, "opencode-*.db"))
	if len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	return matches[0]
}

func opencodeDBCacheRoot() string {
	return filepath.Join(RepoGuideDir(), "cache", "opencode-db", "storage")
}

// materializeOpencodeDB mirrors any opencode.db content into our cache dir
// as session/message/part JSON files, so the existing file-based parser
// picks them up via its normal glob patterns.
func materializeOpencodeDB() {
	for _, dataDir := range opencodeDataDirs() {
		dbPath := opencodeDBPath(dataDir)
		if dbPath == "" {
			continue
		}
		materializeOpencodeDBFile(dbPath)
	}
}

func materializeOpencodeDBFile(dbPath string) {
	db, err := sql.Open("sqlite", dbPath+"?_busy_timeout=3000")
	if err != nil {
		return
	}
	defer db.Close()

	cacheRoot := opencodeDBCacheRoot()
	materializeOpencodeSessionsTable(db, cacheRoot)
	materializeOpencodeMessagesTable(db, cacheRoot)
	materializeOpencodePartsTable(db, cacheRoot)
}

func materializeOpencodeSessionsTable(db *sql.DB, cacheRoot string) {
	rows, err := db.Query(`SELECT id, directory, title, cost, model, time_created, time_updated FROM session`)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id, directory, title     string
			cost                     sql.NullFloat64
			modelJSON                sql.NullString
			timeCreated, timeUpdated sql.NullInt64
		)
		if err := rows.Scan(&id, &directory, &title, &cost, &modelJSON, &timeCreated, &timeUpdated); err != nil {
			continue
		}
		if id == "" {
			continue
		}

		info := opencodeSessionInfo{
			ID:        id,
			Title:     title,
			Directory: directory,
			Cost:      cost.Float64,
		}
		info.Time.Created = timeCreated.Int64
		info.Time.Updated = timeUpdated.Int64
		if modelJSON.Valid {
			var model struct {
				ID         string `json:"id"`
				ProviderID string `json:"providerID"`
			}
			if json.Unmarshal([]byte(modelJSON.String), &model) == nil && model.ID != "" {
				info.Model = &struct {
					ID         string `json:"id"`
					ProviderID string `json:"providerID"`
				}{ID: model.ID, ProviderID: model.ProviderID}
			}
		}

		path := filepath.Join(cacheRoot, "session", "db", id+".json")
		writeOpencodeCacheFile(path, info)
	}
}

func materializeOpencodeMessagesTable(db *sql.DB, cacheRoot string) {
	rows, err := db.Query(`SELECT id, session_id, data FROM message`)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id, sessionID, data string
		if err := rows.Scan(&id, &sessionID, &data); err != nil {
			continue
		}
		if id == "" || sessionID == "" {
			continue
		}
		var msg opencodeMessage
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			continue
		}
		msg.ID = id
		path := filepath.Join(cacheRoot, "message", sessionID, id+".json")
		writeOpencodeCacheFile(path, msg)
	}
}

func materializeOpencodePartsTable(db *sql.DB, cacheRoot string) {
	rows, err := db.Query(`SELECT id, message_id, data FROM part`)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id, messageID, data string
		if err := rows.Scan(&id, &messageID, &data); err != nil {
			continue
		}
		if id == "" || messageID == "" {
			continue
		}
		var part opencodePart
		if err := json.Unmarshal([]byte(data), &part); err != nil {
			continue
		}
		path := filepath.Join(cacheRoot, "part", messageID, id+".json")
		writeOpencodeCacheFile(path, part)
	}
}

func writeOpencodeCacheFile(path string, value any) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}
