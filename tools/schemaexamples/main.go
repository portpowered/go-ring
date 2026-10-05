// Package main validates examples against the schema declarations in a repository.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	var roots stringList

	flag.Var(&roots, "root", "schema directory or file to validate (repeatable; defaults to api)")
	flag.Parse()

	if len(roots) == 0 {
		roots = []string{"api"}
	}

	report, err := validateRoots(roots)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	_, err = fmt.Fprintf(
		os.Stdout,
		"validated %d schema documents, %d examples, and %d required sample groups\n",
		report.documents,
		report.examples,
		report.groups,
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type stringList []string

func (s *stringList) String() string {
	return fmt.Sprint([]string(*s))
}

func (s *stringList) Set(value string) error {
	*s = append(*s, value)

	return nil
}
