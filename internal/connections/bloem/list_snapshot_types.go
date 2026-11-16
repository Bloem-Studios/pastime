package bloem

import "time"

type ListResolveInput struct {
	InstanceID string `json:"instance_id"`
	AccountID  string `json:"account_id"`
	ProfileID  string `json:"profile_id"`
	URL        string `json:"url"`
}
type ListResolution struct {
	SigningKey      string    `json:"signing_key"`
	SourceID        string    `json:"source_id"`
	Name            string    `json:"name"`
	Kind            string    `json:"kind"`
	Count           int       `json:"count"`
	ResolutionToken string    `json:"resolution_token"`
	ExpiresAt       time.Time `json:"expires_at"`
}
type ListSnapshotInput struct {
	InstanceID      string `json:"instance_id"`
	AccountID       string `json:"account_id"`
	ProfileID       string `json:"profile_id"`
	SourceID        string `json:"source_id"`
	ResolutionToken string `json:"resolution_token"`
	Cursor          string `json:"cursor"`
}
type ListSnapshotEntry struct {
	Kind              string            `json:"kind"`
	Title             string            `json:"title"`
	Year              int               `json:"year"`
	ExternalIDs       map[string]string `json:"external_ids"`
	SourceEntryID     string            `json:"source_entry_id,omitempty"`
	UnsupportedReason string            `json:"unsupported_reason,omitempty"`
}
type ListSnapshotPage struct {
	Version           int                 `json:"version"`
	SourceID          string              `json:"source_id"`
	SnapshotID        string              `json:"snapshot_id"`
	Position          int                 `json:"position"`
	Total             int                 `json:"total"`
	Digest            string              `json:"digest"`
	GeneratedAt       time.Time           `json:"generated_at"`
	ExpiresAt         time.Time           `json:"expires_at"`
	NextCursor        string              `json:"next_cursor"`
	Final             bool                `json:"final"`
	Entries           []ListSnapshotEntry `json:"entries"`
	SourceAttestation string              `json:"source_attestation"`
}
type ListAttestationInput struct {
	ListSnapshotInput
	SnapshotID string `json:"snapshot_id"`
}
type ListAttestation struct {
	SnapshotID        string    `json:"snapshot_id"`
	Digest            string    `json:"digest"`
	SourceAttestation string    `json:"source_attestation"`
	ExpiresAt         time.Time `json:"expires_at"`
}
