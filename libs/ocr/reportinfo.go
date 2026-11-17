package ocr

// ReportInfo is what ocr needs of a report's info: the name of the node's key bundle that signs the report, such as
// evm. A capability's own report info implements it, so its oracle keeps the capability's type.
type ReportInfo interface {
	KeyBundle() string
}
