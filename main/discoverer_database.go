package main

import (
	commonocr "github.com/smartcontractkit/chainlink-common/pkg/ocrcommon"
	"github.com/smartcontractkit/chainlink-common/pkg/sqlutil"
)

// DiscovererDatabase is a key-value store for p2p announcements. The
// implementation now lives in chainlink-common so it can be reused; this alias
// preserves the existing core import path.
type DiscovererDatabase = commonocr.DiscovererDatabase

const (
	// ocrDiscovererTable is the name of the table used to store OCR announcements.
	// Must match the CREATE TABLE in migrations/0001_*.sql.
	ocrDiscovererTable = "proxy_ocr_discoverer_announcements"
	// don2donDiscovererTable is the name of the table used to store DON2DON announcements.
)

// NewOCRDiscovererDatabase creates a new DiscovererDatabase for OCR announcements
func NewOCRDiscovererDatabase(ds sqlutil.DataSource, peerID string) *DiscovererDatabase {
	return commonocr.NewDiscovererDatabase(ds, peerID, ocrDiscovererTable)
}
