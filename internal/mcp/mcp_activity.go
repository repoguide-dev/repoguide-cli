package mcp

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type MCPActivityRecord struct {
	CallID    string         `json:"call_id"`
	Timestamp string         `json:"timestamp"`
	Repo      string         `json:"repo"`
	Command   string         `json:"command"`
	Inputs    map[string]any `json:"inputs"`
	Response  map[string]any `json:"response,omitempty"`
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func mcpActivityPath() string {
	return filepath.Join(RepoGuideDir(), "mcp", "activity.jsonl")
}

func AppendMCPActivity(record MCPActivityRecord) error {
	if record.CallID == "" {
		record.CallID = newUUID()
	}
	if strings.TrimSpace(record.Timestamp) == "" {
		record.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if err := os.MkdirAll(filepath.Dir(mcpActivityPath()), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(mcpActivityPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = f.Write(data)
	return err
}

func LoadMCPActivity() ([]MCPActivityRecord, error) {
	f, err := os.Open(mcpActivityPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	records := make([]MCPActivityRecord, 0)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record MCPActivityRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	sort.Slice(records, func(i, j int) bool {
		return records[i].Timestamp > records[j].Timestamp
	})
	return records, nil
}

func topicIDFromMCPRecord(record MCPActivityRecord) string {
	if v, _ := record.Inputs["topic_id"].(string); strings.TrimSpace(v) != "" {
		return v
	}
	if v, _ := record.Response["topic_id"].(string); strings.TrimSpace(v) != "" {
		return v
	}
	return ""
}

func mcpActivityMatchesRepo(record MCPActivityRecord, repoID, repoPath string) bool {
	if strings.TrimSpace(repoPath) != "" && record.Repo == repoPath {
		return true
	}
	if strings.TrimSpace(repoID) != "" {
		if v, _ := record.Inputs["repo_id"].(string); v == repoID {
			return true
		}
	}
	return strings.TrimSpace(repoID) == "" && strings.TrimSpace(repoPath) == ""
}

func LatestUnderstandTaskTopicID(repoID, repoPath string) string {
	records, err := LoadMCPActivity()
	if err != nil {
		return ""
	}
	for _, record := range records {
		if record.Command != "repoguide_get_repo_experience" {
			continue
		}
		if !mcpActivityMatchesRepo(record, repoID, repoPath) {
			continue
		}
		if topicID := topicIDFromMCPRecord(record); topicID != "" {
			return topicID
		}
	}
	return ""
}
