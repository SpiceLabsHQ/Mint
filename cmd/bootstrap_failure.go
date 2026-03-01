package cmd

import (
	"fmt"
	"io"
)

// printBootstrapFailureHint prints the enriched recovery block for a bootstrap
// failure. It is shared between the up and recreate output paths so both
// commands produce consistent guidance.
//
// Output format:
//
//	Bootstrap failed — instance is still running for investigation
//	  SSH:     ssh -p 41122 ubuntu@<IP>
//	  Logs:    sudo journalctl -u mint-bootstrap --no-pager
//	  Recover: mint recreate   (rebuild from scratch)
//	           mint destroy    (tear down completely)
//
// When publicIP is empty the SSH line is omitted gracefully.
func printBootstrapFailureHint(w io.Writer, bootstrapErr error, publicIP string) {
	fmt.Fprintf(w, "\nBootstrap failed — instance is still running for investigation\n")
	fmt.Fprintf(w, "  Error:   %v\n", bootstrapErr)
	if publicIP != "" {
		fmt.Fprintf(w, "  SSH:     ssh -p %d %s@%s\n", defaultSSHPort, defaultSSHUser, publicIP)
	}
	fmt.Fprintf(w, "  Logs:    sudo journalctl -u mint-bootstrap --no-pager\n")
	fmt.Fprintf(w, "  Recover: mint recreate   (rebuild from scratch)\n")
	fmt.Fprintf(w, "           mint destroy    (tear down completely)\n")
}
