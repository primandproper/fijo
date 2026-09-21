// Command envvars derives the closed set of environment variables that can
// override this application's configuration and writes it out as Go constants.
// It is the source of truth behind `make envvars`. Regenerate whenever the
// Config struct — or any struct it is assembled from — gains, loses, or renames
// a field, and commit the result.
//
// The set is real and finite: every `env:` tag reachable from the loadable
// configuration structs, with the `envPrefix:` tags along the way concatenated
// in front of it. Nothing at runtime knows that set, which is the failure this
// closes — a variable one underscore off its tag is simply not read, the
// default or the file value stands, and the process comes up healthy and wrong.
// Generating constants puts a compiler in front of a deployment manifest.
//
// The walk starts from internal/config's configurations constraint rather than
// a list of type names, so it is complete by construction: a configuration
// struct cannot become loadable without appearing here.
package main

import (
	"context"
	"log"

	"github.com/primandproper/template-go/internal/config"

	"github.com/primandproper/primitives-go/v2/config/envvars"
)

func main() {
	if err := envvars.Generate(context.Background(), envvars.Options{
		Dir:          ".",
		Prefix:       config.EnvVarPrefix,
		UnionKey:     "internal/config.configurations",
		Dependencies: []string{"github.com/primandproper/primitives-go"},
		OutputPath:   "internal/config/envvars/env_vars.go",
	}); err != nil {
		log.Fatalf("generating environment variable constants: %v", err)
	}

	log.Printf("generated internal/config/envvars/env_vars.go")
}
