// Command configs renders the application's per-environment configuration files
// from real, typed Go objects. It is the source of truth behind `make configs`:
// each environment's Config is built in Go (see environments.go), validated, and
// written to disk as JSON. Regenerate whenever a builder or the Config struct
// changes, and commit the result so the checked-in JSON never drifts from the
// code.
//
// The rendering itself is primitives-go's: config.RenderJSONFiles validates and
// marshals every environment before it touches the disk, so an invalid config
// fails the whole run rather than landing a broken file beside updated ones. It
// is the exact inverse of the LoadFromJSONFile that internal/config.LoadFromFile
// reads these files back with.
package main

import (
	"context"
	"log"

	"github.com/primandproper/template-go/internal/config"

	primitivesconfig "github.com/primandproper/primitives-go/v2/config"
)

func main() {
	envs := []primitivesconfig.Environment[config.Config]{
		{
			Name:   "localdev",
			Path:   "config/localdev.json",
			Config: buildLocalDevConfig(),
		},
		{
			Name:   "production",
			Path:   "config/production.json",
			Config: buildProductionConfig(),
		},
	}

	if err := primitivesconfig.RenderJSONFiles(context.Background(), envs); err != nil {
		log.Fatalf("rendering configs: %v", err)
	}

	log.Printf("rendered %d config file(s)", len(envs))
}
