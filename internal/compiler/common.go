package compiler

// jobResult tracks the result of a job
type jobResult struct {
	completed bool
	success   bool
	errorMsg  string
}