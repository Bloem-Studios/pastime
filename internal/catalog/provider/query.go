package provider

import "context"

// QueryCapabilities describe options actually forwarded to the public provider.
type QueryCapabilities struct {
	Supported  bool     `json:"supported"`
	Pagination bool     `json:"pagination"`
	Year       bool     `json:"year"`
	Genre      bool     `json:"genre"`
	Sorts      []string `json:"sorts"`
}

type QueryOptions struct {
	Query       string
	Page, Year  int
	Genre, Sort string
}
type RecordPage struct {
	Items    []Record
	NextPage int
}
type Querier interface {
	QueryCapabilities() (search, browse QueryCapabilities)
	Query(ctx context.Context, kind string, options QueryOptions) (RecordPage, error)
}
