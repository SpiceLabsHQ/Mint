package cmd

import (
	"fmt"
	"io"

	"github.com/SpiceLabsHQ/Mint/internal/hint"
)

// printBootstrapFailureHint prints the enriched recovery block for a bootstrap
// failure. It is shared between the up and recreate output paths so both
// commands produce consistent guidance.
//
// Output format (non-TTY):
//
//	Bootstrap failed — instance is still running for investigation
//	  Error:  <error message>
//	  SSH:  `ssh -p 41122 ubuntu@<IP>`
//	  Logs:  `sudo journalctl -u mint-bootstrap --no-pager`
//	  Recover:  `mint recreate`  (rebuild from scratch)
//	  Cleanup:  `mint destroy`  (tear down completely)
//
// When publicIP is empty the SSH line is omitted gracefully.
func printBootstrapFailureHint(w io.Writer, bootstrapErr error, publicIP string) {
	fmt.Fprintf(w, "\nBootstrap failed — instance is still running for investigation\n")
	fmt.Fprintf(w, "  Error:  %v\n", bootstrapErr)
	if publicIP != "" {
		fmt.Fprintln(w, hint.Suggest("SSH", fmt.Sprintf("ssh -p %d %s@%s", defaultSSHPort, defaultSSHUser, publicIP)))
	}
	fmt.Fprintln(w, hint.Suggest("Logs", "sudo journalctl -u mint-bootstrap --no-pager"))
	fmt.Fprintf(w, "%s  (rebuild from scratch)\n", hint.Suggest("Recover", "mint recreate"))
	fmt.Fprintf(w, "%s  (tear down completely)\n", hint.Suggest("Cleanup", "mint destroy"))
}
