package usecase

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseScheduledDMRecipientsAcceptsUsernameForms(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "bare username", input: "alice", want: []string{"alice"}},
		{name: "at username", input: "@Bobby", want: []string{"bobby"}},
		{name: "Telegram URL", input: "https://t.me/CAROL", want: []string{"carol"}},
		{name: "mixed forms", input: "alice\n@Bobby\nhttps://t.me/CAROL", want: []string{"alice", "bobby", "carol"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recipients, err := ParseScheduledDMRecipients(tt.input)
			if err != nil {
				t.Fatalf("ParseScheduledDMRecipients() error = %v", err)
			}
			if !reflect.DeepEqual(recipients, tt.want) {
				t.Fatalf("ParseScheduledDMRecipients() = %#v, want %#v", recipients, tt.want)
			}
		})
	}
}

func TestParseScheduledDMRecipientsDeduplicatesNormalizedUsernames(t *testing.T) {
	recipients, err := ParseScheduledDMRecipients("@Alice\nhttps://t.me/ALICE\nalice")
	if err != nil {
		t.Fatalf("ParseScheduledDMRecipients() error = %v", err)
	}
	if want := []string{"alice"}; !reflect.DeepEqual(recipients, want) {
		t.Fatalf("ParseScheduledDMRecipients() = %#v, want %#v", recipients, want)
	}
}

func TestParseScheduledDMRecipientsRejectsInvalidURLs(t *testing.T) {
	tests := []string{
		"https://telegram.me/alice",
		"http://t.me/alice",
		"https://t.me/alice/extra",
		"https://t.me/alice?start=1",
		"https://t.me/",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			if _, err := ParseScheduledDMRecipients(input); err == nil {
				t.Fatalf("ParseScheduledDMRecipients(%q) error = nil, want error", input)
			}
		})
	}
}

func TestParseScheduledDMRecipientsRequiresOneContactPerLineAndTelegramUsernameLength(t *testing.T) {
	tests := []string{
		"alice bobby",
		"@four",
		strings.Repeat("a", 33),
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			if _, err := ParseScheduledDMRecipients(input); err != ErrScheduledDMRecipientInvalid {
				t.Fatalf("ParseScheduledDMRecipients(%q) error = %v, want %v", input, err, ErrScheduledDMRecipientInvalid)
			}
		})
	}
}

func TestParseScheduledDMRecipientsEnforcesUniqueRecipientLimit(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "empty", input: " \n\t", wantErr: true},
		{name: "one", input: "alice", wantErr: false},
		{name: "three", input: "alice\nbobby\ncarol", wantErr: false},
		{name: "four", input: "alice\nbobby\ncarol\ndorothy", wantErr: true},
		{name: "duplicates reduce to three", input: "alice\nbobby\ncarol\n@ALICE", wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseScheduledDMRecipients(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseScheduledDMRecipients(%q) error = %v, want error = %t", tt.input, err, tt.wantErr)
			}
		})
	}
}
