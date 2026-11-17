// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/airencracken/comfylib/tools"
)

func main() {
	workflowOnly := flag.Bool("workflows", false, "check only shared workflows")
	revision := flag.String("revision", "", "required shared workflow commit")
	flag.Parse()
	roots := flag.Args()
	if len(roots) == 0 {
		root := os.Getenv("COMFYWARE_APP_ROOT")
		if root == "" {
			root = "."
		}
		roots = []string{root}
	}
	for _, root := range roots {
		var err error
		if *workflowOnly {
			err = tools.VerifyWorkflows(root, *revision)
		} else {
			err = tools.VerifyConsumer(root)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if !*workflowOnly {
			fmt.Printf("%s: shared module integration passed\n", filepath.Base(root))
		}
	}
}
