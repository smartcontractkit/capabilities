package ocr

// ReportInfo is the info of a report of an oracle that runs over the node's OCR. KeyBundle names the node's key bundle
// that signs the report, such as evm. Value is the capability's, for its transmitter.
type ReportInfo[T any] struct {
	KeyBundle string
	Value     T
}
