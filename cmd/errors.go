package cmd

// silentExitError is an error that carries no message text. It signals to
// main.go that the command failed (so os.Exit(1) is appropriate) but that
// the error has already been reported to the user (e.g., via structured JSON
// output on stdout). main.go checks err.Error() == "" before printing.
type silentExitError struct{}

func (silentExitError) Error() string { return "" }
