package netengine

import "context"

// LinkSelector is implemented by the Stage 5 SD-WAN link manager. The Stage 2
// egress manager consults it (when configured) to pick a physical link.
type LinkSelector interface {
	SelectLink(ctx context.Context, key string) (Link, error)
	ReportResult(linkID string, rttMs float64, ok bool)
}

type Link struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Interface string  `json:"interface"`
	Gateway   string  `json:"gateway"`
	Weight    int     `json:"weight"`
	Cost      int     `json:"cost"`
	Healthy   bool    `json:"healthy"`
	RTTMs     float64 `json:"rtt_ms"`
	LossPct   float64 `json:"loss_pct"`
}
