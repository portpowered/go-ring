package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./tools/restmodelsplit <bundle|split>")
		os.Exit(2)
	}

	var err error

	switch os.Args[1] {
	case "bundle":
		err = bundleOpenAPI("api/openapi.base.yaml", "api/schemas", "api/openapi.yaml")
	case "split":
		err = splitModels("api/openapi.yaml", "pkg/dependencymodels/rest/models.gen.go", "pkg/dependencymodels/rest")
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q; want bundle or split\n", os.Args[1])
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
