package domain

import "testing"

func TestKeywordRuleValidateRequiresKeyword(t *testing.T) {
	rule := KeywordRule{Enabled: true, ActionMode: ActionPublicReply, PublicReplyText: "ok"}
	if err := rule.Validate(); err == nil {
		t.Fatal("Validate succeeded, want error")
	}
}

func TestKeywordRuleValidateRequiresPublicText(t *testing.T) {
	rule := KeywordRule{Keyword: "hello", Enabled: true, ActionMode: ActionPublicReply}
	if err := rule.Validate(); err == nil {
		t.Fatal("Validate succeeded, want error")
	}
}

func TestKeywordRuleValidateRequiresPrivateText(t *testing.T) {
	rule := KeywordRule{Keyword: "hello", Enabled: true, ActionMode: ActionPrivateMessage}
	if err := rule.Validate(); err == nil {
		t.Fatal("Validate succeeded, want error")
	}
}

func TestKeywordRuleValidateBothActions(t *testing.T) {
	rule := KeywordRule{
		Keyword:            "hello",
		Enabled:            true,
		ActionMode:         ActionBoth,
		PublicReplyText:    "chat",
		PrivateMessageText: "dm",
	}
	if err := rule.Validate(); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

func TestKeywordRuleMatchesOperatorExpression(t *testing.T) {
	rule := KeywordRule{
		Keyword: "[машина едет] -(ремонт|сломалась)",
		Enabled: true,
	}

	if !rule.Matches("Машина едет сегодня") {
		t.Fatal("ordered expression should match")
	}
	if rule.Matches("Машина после ремонта едет") {
		t.Fatal("excluded alternative should suppress the match")
	}
	if rule.Matches("Едет машина сегодня") {
		t.Fatal("brackets must preserve word order")
	}
}
