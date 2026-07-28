package mcp

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"github.com/repoguide/repoguide-cli/internal/config"
)

// HoldoutMarker is embedded in every withheld response. The session parser
// looks for it to tell a randomized control session apart from one that simply
// never called RepoGuide — without it the two are indistinguishable after the
// fact, and the experiment produces no usable control group.
const HoldoutMarker = "[repoguide:holdout]"

// isHeldOut decides whether this session is in the control group.
//
// The decision is derived from the session ID rather than drawn at random so
// that repeat calls within one session agree with each other. A session that
// got a briefing for its first call and a holdout for its second would be in
// both arms at once, which is worse than not running the experiment at all.
//
// An empty session ID never gets held out: without a stable key the assignment
// couldn't be kept consistent, and silently withholding is the more harmful of
// the two errors.
func isHeldOut(sessionID string, pct int) bool {
	if pct <= 0 || sessionID == "" {
		return false
	}
	if pct >= 100 {
		return true
	}
	sum := sha256.Sum256([]byte("repoguide-holdout:" + sessionID))
	return binary.BigEndian.Uint64(sum[:8])%100 < uint64(pct)
}

// holdoutResponse is what the agent sees instead of the briefing. It has to
// steer the agent into working unaided without making it retry the call, since
// a retry loop would burn tokens and muddy the very measurement this exists to
// take.
func holdoutResponse(pct int) string {
	return fmt.Sprintf(
		"No repository experience is being served for this session.\n\n"+
			"This session is part of a %d%% measurement holdout, so RepoGuide is "+
			"deliberately staying out of the way. Proceed with your normal workflow: "+
			"search and read the repository as you would without RepoGuide.\n\n"+
			"Do not call repoguide_get_repo_experience again for this task — the "+
			"result will not change, and retrying only distorts the measurement.\n\n"+
			"%s",
		pct, HoldoutMarker)
}

// holdoutForSession returns the withheld response and true when the session
// falls in the control group.
func holdoutForSession(sessionID string) (string, bool) {
	pct := config.HoldoutPct()
	if !isHeldOut(sessionID, pct) {
		return "", false
	}
	return holdoutResponse(pct), true
}
