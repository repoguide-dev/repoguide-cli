package ai

import "testing"

func TestParseSelectTopicResponseAcceptsActionableBestMatch(t *testing.T) {
	got, _, err := parseSelectTopicResponse(`{"status":"matched","topic_id":"profiles","confidence":0.62,"candidate_topics":[{"topic_id":"profiles","confidence":0.62}]}`, Usage{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "ok" || got.TopicID != "profiles" {
		t.Fatalf("actionable best match should proceed: %#v", got)
	}
}

func TestParseSelectTopicResponseKeepsCloseMatchesAsSecondaryContext(t *testing.T) {
	got, _, err := parseSelectTopicResponse(`{"status":"matched","topic_id":"users","confidence":0.72,"candidate_topics":[{"topic_id":"users","confidence":0.72},{"topic_id":"oauth","confidence":0.68}]}`, Usage{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "ok" || got.TopicID != "users" || len(got.CandidateTopics) != 2 {
		t.Fatalf("close matches should retain a primary route: %#v", got)
	}
}

func TestParseSelectTopicResponseAcceptsStrongFencedJSON(t *testing.T) {
	raw := "```json\n{\"status\":\"matched\",\"topic_id\":\"api\",\"confidence\":0.86,\"candidate_topics\":[{\"topic_id\":\"api\",\"confidence\":0.86}]}\n```"
	got, _, err := parseSelectTopicResponse(raw, Usage{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "ok" || got.TopicID != "api" || got.Confidence != 0.86 {
		t.Fatalf("unexpected match: %#v", got)
	}
}
