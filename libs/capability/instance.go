package capability

// Instance is which instance of the capability this is, and how many the process runs, provided to
// each instance's container. A run is instance 0 of 1; an embed run is --instances of them. A
// capability and its dependencies use it to decide how to run and to tell instances apart -
// service names, capability IDs, schemas.
type Instance struct {
	// ID is this instance's index, from 0.
	ID int
	// Total is how many instances the process runs.
	Total int
}
