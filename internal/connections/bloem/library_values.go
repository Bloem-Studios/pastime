package bloem

import "time"

type LibraryCopy struct {
	ID          string            `json:"id"`
	ContentID   string            `json:"content_id"`
	Kind        string            `json:"kind"`
	Title       string            `json:"title"`
	ExternalIDs map[string]string `json:"external_ids"`
	Format      string            `json:"format"`
	Edition     string            `json:"edition"`
	Resolution  string            `json:"resolution"`
	HDR         string            `json:"hdr"`
	Audio       string            `json:"audio"`
	BitrateKbps uint32            `json:"bitrate_kbps"`
	AcquiredOn  string            `json:"acquired_on"`
	PriceCents  int64             `json:"price_cents"`
	Currency    string            `json:"currency"`
	Store       string            `json:"store"`
	Location    string            `json:"location"`
	Notes       string            `json:"notes"`
	Custom      map[string]string `json:"custom"`
	Removed     bool              `json:"removed"`
	Revision    int64             `json:"revision"`
}
type BookState struct {
	ContentID                string            `json:"content_id"`
	Kind                     string            `json:"kind"`
	Title                    string            `json:"title"`
	ExternalIDs              map[string]string `json:"external_ids"`
	EditionID                string            `json:"edition_id"`
	ISBN                     string            `json:"isbn"`
	FileID                   int64             `json:"file_id"`
	AudiobookSeconds         float64           `json:"audiobook_seconds"`
	AudiobookDurationSeconds float64           `json:"audiobook_duration_seconds"`
	EbookLocation            string            `json:"ebook_location"`
	EbookFraction            float64           `json:"ebook_fraction"`
	Completed                bool              `json:"completed"`
	Reset                    bool              `json:"reset"`
	ChangedAt                *time.Time        `json:"changed_at"`
	Revision                 int64             `json:"revision"`
}
