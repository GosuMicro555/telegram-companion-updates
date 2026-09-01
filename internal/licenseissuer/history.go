package licenseissuer

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"telegram-companion/internal/license"
)

const historySchema = 1

// ErrInvalidHistory is a safe classification for malformed or unsupported
// issuance-history data.
var ErrInvalidHistory = errors.New("licenseissuer: invalid history")

// HistoryEntry is the complete whitelist of metadata permitted in issuance
// history. It intentionally has no token or key fields.
type HistoryEntry struct {
	Schema    int    `json:"schema"`
	LicenseID string `json:"license_id"`
	Product   string `json:"product"`
	Channel   string `json:"channel"`
	MachineID string `json:"machine_id"`
	Owner     string `json:"owner"`
	Comment   string `json:"comment,omitempty"`
	IssuedAt  string `json:"issued_at"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

type historyDocument struct {
	Schema  int            `json:"schema"`
	Entries []HistoryEntry `json:"entries"`
}

// NewHistoryEntry validates payload and copies only non-secret issuance
// metadata into a history record.
func NewHistoryEntry(payload license.Payload) (HistoryEntry, error) {
	if _, err := license.CanonicalPayload(payload); err != nil {
		return HistoryEntry{}, err
	}
	return HistoryEntry{
		Schema:    payload.Schema,
		LicenseID: payload.LicenseID,
		Product:   payload.Product,
		Channel:   payload.Channel,
		MachineID: payload.MachineID,
		Owner:     payload.Owner,
		Comment:   payload.Comment,
		IssuedAt:  payload.IssuedAt,
		ExpiresAt: payload.ExpiresAt,
	}, nil
}

func (entry HistoryEntry) payload() license.Payload {
	return license.Payload{
		Schema:    entry.Schema,
		LicenseID: entry.LicenseID,
		Product:   entry.Product,
		Channel:   entry.Channel,
		MachineID: entry.MachineID,
		Owner:     entry.Owner,
		Comment:   entry.Comment,
		IssuedAt:  entry.IssuedAt,
		ExpiresAt: entry.ExpiresAt,
	}
}

// MarshalHistory returns versioned JSON containing only whitelisted issuance
// metadata.
func MarshalHistory(entries []HistoryEntry) ([]byte, error) {
	if entries == nil {
		entries = []HistoryEntry{}
	}
	for _, entry := range entries {
		if _, err := license.CanonicalPayload(entry.payload()); err != nil {
			return nil, ErrInvalidHistory
		}
	}
	encoded, err := json.Marshal(historyDocument{Schema: historySchema, Entries: entries})
	if err != nil {
		return nil, ErrInvalidHistory
	}
	return encoded, nil
}

// UnmarshalHistory parses versioned history JSON and rejects all fields outside
// the non-secret whitelist.
func UnmarshalHistory(encoded []byte) ([]HistoryEntry, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()

	var document historyDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, ErrInvalidHistory
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, ErrInvalidHistory
	}
	if document.Schema != historySchema || document.Entries == nil {
		return nil, ErrInvalidHistory
	}
	for _, entry := range document.Entries {
		if _, err := license.CanonicalPayload(entry.payload()); err != nil {
			return nil, ErrInvalidHistory
		}
	}

	return append([]HistoryEntry(nil), document.Entries...), nil
}
