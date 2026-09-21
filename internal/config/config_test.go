package config

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/primandproper/primitives-go/v2/observability/logging"
	loggingcfg "github.com/primandproper/primitives-go/v2/observability/logging/config"
	tracingcfg "github.com/primandproper/primitives-go/v2/observability/tracing/config"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("defaults are valid and build pillars", func(t *testing.T) {
		t.Parallel()

		cfg := New(Options{})

		test.Eq(t, DefaultServiceName, cfg.Observability.Logging.ServiceName)
		test.Eq(t, loggingcfg.ProviderSlog, cfg.Observability.Logging.Provider)
		test.Eq(t, logging.InfoLevel, cfg.Observability.Logging.Level)

		must.NoError(t, cfg.Validate(context.Background()))

		pillars, err := cfg.Observability.NewPillars(context.Background())
		must.NoError(t, err)
		must.NotNil(t, pillars)
		must.NotNil(t, pillars.Logger)
	})

	t.Run("options override defaults", func(t *testing.T) {
		t.Parallel()

		cfg := New(Options{ServiceName: "custom", LogLevel: "debug"})

		test.Eq(t, "custom", cfg.Observability.Logging.ServiceName)
		test.Eq(t, logging.DebugLevel, cfg.Observability.Logging.Level)
		must.NoError(t, cfg.Validate(context.Background()))
	})
}

// These loader tests mutate the process environment via t.Setenv, which is
// incompatible with t.Parallel, so they run serially by design.

func TestLoad(t *testing.T) {
	t.Run("defaults when no environment variables are set", func(t *testing.T) {
		cfg, err := Load(context.Background(), Options{})
		must.NoError(t, err)

		test.Eq(t, DefaultServiceName, cfg.Observability.Logging.ServiceName)
		test.Eq(t, loggingcfg.ProviderSlog, cfg.Observability.Logging.Provider)
		test.Eq(t, logging.InfoLevel, cfg.Observability.Logging.Level)
	})

	t.Run("environment variables overlay the option defaults", func(t *testing.T) {
		t.Setenv(EnvVarPrefix+"OBSERVABILITY_LOGGING_SERVICE_NAME", "from-env")
		t.Setenv(EnvVarPrefix+"OBSERVABILITY_LOGGING_LEVEL", "error")

		cfg, err := Load(context.Background(), Options{ServiceName: "from-opts", LogLevel: "info"})
		must.NoError(t, err)

		// The env var wins over the option-seeded default.
		test.Eq(t, "from-env", cfg.Observability.Logging.ServiceName)
		test.Eq(t, logging.ErrorLevel, cfg.Observability.Logging.Level)
		// A field with no env var keeps the default set by New.
		test.Eq(t, loggingcfg.ProviderSlog, cfg.Observability.Logging.Provider)
	})
}

func TestLoadFromFile(t *testing.T) {
	t.Run("decodes a complete config file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.json")
		must.NoError(t, os.WriteFile(path, []byte(
			`{"observability":{"logging":{"provider":"slog","serviceName":"from-file","level":"warn"}}}`,
		), 0o600))

		cfg, err := LoadFromFile(context.Background(), path)
		must.NoError(t, err)

		test.Eq(t, "from-file", cfg.Observability.Logging.ServiceName)
		test.Eq(t, loggingcfg.ProviderSlog, cfg.Observability.Logging.Provider)
		test.Eq(t, logging.WarnLevel, cfg.Observability.Logging.Level)
	})

	t.Run("environment variables overlay the file", func(t *testing.T) {
		t.Setenv(EnvVarPrefix+"OBSERVABILITY_LOGGING_SERVICE_NAME", "env-wins")

		path := filepath.Join(t.TempDir(), "config.json")
		must.NoError(t, os.WriteFile(path, []byte(
			`{"observability":{"logging":{"provider":"slog","serviceName":"from-file"}}}`,
		), 0o600))

		cfg, err := LoadFromFile(context.Background(), path)
		must.NoError(t, err)

		test.Eq(t, "env-wins", cfg.Observability.Logging.ServiceName)
	})

	t.Run("an invalid file fails validation", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.json")
		must.NoError(t, os.WriteFile(path, []byte(
			`{"observability":{"logging":{"provider":"nonsense"}}}`,
		), 0o600))

		_, err := LoadFromFile(context.Background(), path)
		must.Error(t, err)
	})

	t.Run("a file omitting the service name is valid for a stdout provider", func(t *testing.T) {
		// The platform requires a service name only for the providers that ship
		// telemetry somewhere (otelslog); slog writes to stdout, so a file that
		// omits it loads cleanly.
		path := filepath.Join(t.TempDir(), "config.json")
		must.NoError(t, os.WriteFile(path, []byte(
			`{"observability":{"logging":{"provider":"slog"}}}`,
		), 0o600))

		cfg, err := LoadFromFile(context.Background(), path)
		must.NoError(t, err)
		test.Eq(t, "", cfg.Observability.Logging.ServiceName)
	})

	t.Run("a missing file is an error", func(t *testing.T) {
		_, err := LoadFromFile(context.Background(), filepath.Join(t.TempDir(), "does-not-exist.json"))
		must.Error(t, err)
	})
}

func TestLevelFromString(t *testing.T) {
	t.Parallel()

	cases := map[string]logging.Level{
		"debug":     logging.DebugLevel,
		"info":      logging.InfoLevel,
		"warn":      logging.WarnLevel,
		"warning":   logging.WarnLevel,
		"error":     logging.ErrorLevel,
		"ERROR":     logging.ErrorLevel,
		"":          logging.InfoLevel,
		"nonsense":  logging.InfoLevel,
		"  debug  ": logging.DebugLevel,
	}

	for input, want := range cases {
		test.Eq(t, want, levelFromString(input), test.Sprintf("input %q", input))
	}
}

// TestCheckedInConfigsLoad is the guard on `make configs`. The files under
// config/ are a projection of the Go objects in cmd/tools/codegen/configs, and
// nothing at build time re-derives them — so a Config field that was renamed or
// tightened without a regenerate leaves a checked-in file that no longer loads.
// This is where that shows up.
func TestCheckedInConfigsLoad(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"localdev", "production"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg, err := LoadFromFile(t.Context(), filepath.Join("..", "..", "config", name+".json"))
			must.NoError(t, err)
			test.Eq(t, DefaultServiceName, cfg.Observability.Logging.ServiceName)
			test.Eq(t, loggingcfg.ProviderSlog, cfg.Observability.Logging.Provider)
		})
	}
}

// TestNewPillarsIsQuietWhenUnconfigured pins the contract the version
// subcommand depends on: at the default info level a config that names no
// telemetry provider must produce no output at all, because the slog provider
// writes to stdout and that is where `template-go version` prints too.
//
// The aggregate observability constructor does not have this property — the
// tracing sub-config announces "tracing disabled" on its way to the noop — so
// this is what stops a future simplification from delegating to it
// unconditionally.
//
// It replaces os.Stdout for the length of the call, which is process-wide
// state, so it does not call t.Parallel. The slog logger captures the file at
// construction rather than reading it per write, so the swap has to be in place
// before NewPillars runs.
func TestNewPillarsIsQuietWhenUnconfigured(t *testing.T) {
	read, write, err := os.Pipe()
	must.NoError(t, err)

	previous := os.Stdout
	os.Stdout = write

	pillars, err := New(Options{}).NewPillars(t.Context())

	os.Stdout = previous
	must.NoError(t, write.Close())

	must.NoError(t, err)
	must.NotNil(t, pillars)
	must.NotNil(t, pillars.Logger)
	must.NotNil(t, pillars.TracerProvider)
	must.NotNil(t, pillars.MetricsProvider)
	must.NotNil(t, pillars.Profiler)

	written, err := io.ReadAll(read)
	must.NoError(t, err)
	must.NoError(t, read.Close())

	test.Eq(t, "", string(written))
}

// TestNewPillarsHonorsAConfiguredProvider is the other half: naming a provider
// has to reach the aggregate constructor, or every telemetry setting the
// envvars package advertises would be read, validated, and then ignored.
func TestNewPillarsHonorsAConfiguredProvider(t *testing.T) {
	t.Parallel()

	cfg := New(Options{})
	cfg.Observability.Tracing.Provider = tracingcfg.ProviderNoop

	test.True(t, cfg.telemetryConfigured())

	pillars, err := cfg.NewPillars(t.Context())
	must.NoError(t, err)
	must.NotNil(t, pillars.TracerProvider)
}
