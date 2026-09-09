// Command declarative is a minimal driver for the catalog package: apply a spec file (dry run by
// default) or export a slice as YAML. Credentials come from REARM_URI, REARM_APIKEYID, REARM_APIKEY.
//
//	go run ./examples/declarative apply -f catalog.yaml [--commit] [--repo r --path p --sha s]
//	go run ./examples/declarative export catalog [name ...]
//	go run ./examples/declarative export branches <component>
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	rearm "github.com/relizaio/rearm-client-go"
	"github.com/relizaio/rearm-client-go/catalog"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	c, err := rearm.New(os.Getenv("REARM_URI"), os.Getenv("REARM_APIKEYID"), os.Getenv("REARM_APIKEY"))
	if err != nil {
		fail(err)
	}
	ctx := context.Background()
	switch os.Args[1] {
	case "apply":
		fs := flag.NewFlagSet("apply", flag.ExitOnError)
		file := fs.String("f", "", "spec file (kind: Catalog | Branches)")
		commit := fs.Bool("commit", false, "write changes (default is dry run)")
		repo := fs.String("repo", "", "provenance: repository")
		path := fs.String("path", "", "provenance: path in the repository")
		sha := fs.String("sha", "", "provenance: commit")
		_ = fs.Parse(os.Args[2:])
		if *file == "" {
			usage()
		}
		spec, err := catalog.Load(*file)
		if err != nil {
			fail(err)
		}
		var src *catalog.Source
		if *repo != "" || *path != "" || *sha != "" {
			src = &catalog.Source{Repo: nz(*repo), Path: nz(*path), Commit: nz(*sha)}
		}
		res, err := catalog.Apply(ctx, c, spec, !*commit, src)
		if err != nil {
			fail(err)
		}
		fmt.Print(catalog.Format(res))
		if res.Errors > 0 {
			os.Exit(2)
		}
	case "export":
		if len(os.Args) < 3 {
			usage()
		}
		var out any
		switch os.Args[2] {
		case "catalog":
			out, err = catalog.ExportCatalog(ctx, c, os.Args[3:])
		case "branches":
			if len(os.Args) < 4 {
				usage()
			}
			out, err = catalog.ExportBranches(ctx, c, os.Args[3])
		default:
			usage()
		}
		if err != nil {
			fail(err)
		}
		y, err := catalog.ToYAML(out)
		if err != nil {
			fail(err)
		}
		os.Stdout.Write(y)
	default:
		usage()
	}
}

func nz(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: declarative apply -f <file> [--commit] [--repo r --path p --sha s] | export catalog [name ...] | export branches <component>")
	os.Exit(1)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
