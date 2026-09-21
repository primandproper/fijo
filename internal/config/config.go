// Package config assembles the application's configuration.
//
// The configuration has two halves. The platform half is platform-go's
// service.Config, embedded so its fields promote onto Config: the service name,
// the observability pillars, and one pointer sub-config per platform subsystem
// (database, identity, authorization, audit, tokens, WebAuthn, email, the gRPC
// server, ...). A subsystem is composed into the process by being present in
// the config and by nothing else — see platform-go's service package for the
// rule. The Fijo half is the Auth block: the settings that belong to Fijo's own
// sign-in flow rather than to any one platform package.
//
// The configuration is built in Go with zero-dependency defaults so the binary
// boots out of the box: structured slog logging, and tracing, metrics and
// profiling left unconfigured, which primitives-go resolves to noop providers.
// A config that names no database, no identity store and no token issuer is a
// valid config — it is what `fijo version` boots with — and one that serves
// has to name all of them; ValidateForServing is that check.
//
// Two loaders build on those defaults using primitives-go's config package:
//
//   - Load overlays environment variables (prefixed with EnvVarPrefix) on top of
//     the defaults, so any field can be tuned without a config file — the
//     twelve-factor path most deployments start with.
//   - LoadFromFile decodes a complete JSON config file and then overlays the
//     same environment variables, for deployments that graduate to a checked-in
//     or mounted configuration.
//
// Both share envVarOptions, which wires the app's env var prefix and a debug
// hook that logs every value the parser applies.
//
// The set of variables that overlay can read is not written down here. It is
// derived from the `env:` tags reachable from the configurations constraint and
// generated into the envvars subpackage by `make envvars`.
package config

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/primandproper/platform-go/v14/service"
	primitivesconfig "github.com/primandproper/primitives-go/v2/config"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/observability"
	"github.com/primandproper/primitives-go/v2/observability/logging"
	loggingcfg "github.com/primandproper/primitives-go/v2/observability/logging/config"
	metricsnoop "github.com/primandproper/primitives-go/v2/observability/metrics/noop"
	profilingnoop "github.com/primandproper/primitives-go/v2/observability/profiling/noop"
	tracingnoop "github.com/primandproper/primitives-go/v2/observability/tracing/noop"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

// DefaultServiceName is the service name reported by the observability suite
// when the caller does not supply one.
const DefaultServiceName = "fijo"

// EnvVarPrefix is prepended to every environment variable this application
// reads, keeping its configuration in a distinct namespace. For example the
// logging level is read from FIJO_OBSERVABILITY_LOGGING_LEVEL: the prefix
// here, then the nested envPrefix tags on Config and the platform sub-configs.
const EnvVarPrefix = "FIJO_"

// Log level names accepted by Options.LogLevel (case-insensitive).
const (
	LevelDebug = "debug"
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
)

// configurations is the constraint every loadable configuration struct in this
// package is a member of. Today that is Config alone; a second one — a worker
// or a migration tool with its own shape — joins it here.
//
// It is what makes `make envvars` complete by construction. The generator walks
// this constraint's members rather than a list somebody remembered to update,
// so a configuration struct cannot become loadable without its environment
// variables appearing in the generated envvars package.
type configurations interface {
	Config
}

// Config is the application configuration.
//
// service.Config is embedded rather than named so that its fields promote: the
// JSON a deployment mounts has "name", "observability", "database" and "auth"
// as siblings, and the environment reads FIJO_DATABASE_* rather than
// FIJO_PLATFORM_DATABASE_*. The embed carries no env tag on purpose —
// caarlos0/env reads through an anonymous embedded struct and still composes
// the nested envPrefix tags, and encoding/json promotes the fields on its own.
type Config struct {
	service.Config

	// Auth is Fijo's own sign-in configuration: the session record behind every
	// access token, token lifetimes, the password-reset link and the address
	// reset mail comes from.
	Auth AuthConfig `envPrefix:"AUTH_" json:"auth"`
}

var _ validation.ValidatableWithContext = (*Config)(nil)

// Options tune the values that most deployments care about. Empty fields fall
// back to the built-in defaults.
type Options struct {
	// ServiceName labels telemetry emitted by the service.
	ServiceName string
	// LogLevel is one of "debug", "info", "warn", or "error" (case-insensitive).
	LogLevel string
}

// New builds a Config from the given options, filling in defaults. The result
// validates cleanly and is ready to hand to NewPillars.
func New(opts Options) *Config {
	serviceName := strings.TrimSpace(opts.ServiceName)
	if serviceName == "" {
		serviceName = DefaultServiceName
	}

	cfg := &Config{
		Name: serviceName,
		Observability: observability.Config{
			// slog logging to stdout as structured JSON.
			Logging: loggingcfg.Config{
				Provider:    loggingcfg.ProviderSlog,
				ServiceName: serviceName,
				Level:       levelFromString(opts.LogLevel),
			},
			// Tracing, Metrics, and Profiling are left at their zero values.
			// An empty provider is the documented opt-out into a noop, so the
			// binary stays quiet and dependency-free until one is named.
		},
	}

	return cfg
}

// envVarOptions returns the platform config options shared by every loader in
// this package: the application's env var prefix and a debug hook that logs each
// value the parser applies. Because the hook uses the standard-library slog
// default logger, these lines appear only when that logger is at debug level.
func envVarOptions() []primitivesconfig.Option {
	return []primitivesconfig.Option{
		primitivesconfig.WithPrefix(EnvVarPrefix),
		primitivesconfig.WithOnSet(func(tag string, value any, isDefault bool) {
			slog.Debug("config value set from environment",
				slog.String("tag", tag),
				slog.Any("value", value),
				slog.Bool("isDefault", isDefault),
			)
		}),
	}
}

// Load builds a Config from the given options and then overlays environment
// variables on top of it. The options (typically the CLI's flags) seed the
// defaults; any FIJO_-prefixed environment variable that is set wins over
// them. Fields left unset in the environment keep their default value, so the
// binary still boots with structured slog logging and noop telemetry out of the
// box. The result is validated before it is returned, and validation is what
// releases the platform sub-configs that environment parsing allocated and
// nobody filled in.
func Load(ctx context.Context, opts Options) (*Config, error) {
	cfg := New(opts)

	if err := primitivesconfig.ApplyEnvironmentVariables(cfg, envVarOptions()...); err != nil {
		return nil, fmt.Errorf("applying environment variables: %w", err)
	}

	if err := cfg.ValidateWithContext(ctx); err != nil {
		return nil, fmt.Errorf("validating configuration: %w", err)
	}

	return cfg, nil
}

// LoadFromFile decodes a complete JSON configuration file and then overlays
// environment variables (a set FIJO_ variable wins over the file value).
// Unlike Load, it does not start from the built-in defaults: the file is
// expected to fully specify the config, so use it once a deployment has a real
// config file to mount. The result is validated before it is returned. Note
// that validation is permissive about omissions — an empty logging provider is
// the documented opt-out into noop logging, a missing name takes
// DefaultServiceName, and a platform subsystem the file leaves out is a
// subsystem this process is not made of — so a sparse file loads rather than
// failing.
func LoadFromFile(ctx context.Context, path string) (*Config, error) {
	cfg, err := loadFromJSONFile[Config](ctx, path)
	if err != nil {
		return nil, err
	}

	if err = cfg.ValidateWithContext(ctx); err != nil {
		return nil, fmt.Errorf("validating configuration: %w", err)
	}

	return cfg, nil
}

// loadFromJSONFile is the decode every loadable configuration struct goes
// through, and the reason the configurations constraint has teeth: a struct
// this package cannot load through here is a struct whose environment variables
// `make envvars` would not have generated.
func loadFromJSONFile[T configurations](ctx context.Context, path string) (*T, error) {
	cfg, err := primitivesconfig.LoadFromJSONFile[T](ctx, path, envVarOptions()...)
	if err != nil {
		return nil, fmt.Errorf("loading configuration file: %w", err)
	}

	return cfg, nil
}

// EnsureDefaults fills in what a config assembled in code or a sparse file
// leaves out: the service name, and whatever the Auth block defaults.
func (c *Config) EnsureDefaults() {
	if strings.TrimSpace(c.Name) == "" {
		c.Name = DefaultServiceName
	}

	c.Config.EnsureDefaults()
	c.Auth.EnsureDefaults()
}

// ValidateWithContext confirms the assembled configuration is internally
// consistent. The platform half validates itself — which is also what releases
// the sub-configs environment parsing allocated and nobody configured — and
// then the Auth block validates.
func (c *Config) ValidateWithContext(ctx context.Context) error {
	c.EnsureDefaults()

	if err := c.Config.ValidateWithContext(ctx); err != nil {
		return err
	}

	return c.Auth.ValidateWithContext(ctx)
}

// ValidateForServing is the stricter check a process that serves or works
// makes: every platform subsystem the sign-in flow is built from has to be
// present. It is separate from ValidateWithContext because `fijo version` and
// `fijo migrate` are valid programs over a config that names none of them.
func (c *Config) ValidateForServing(ctx context.Context) error {
	if err := c.ValidateWithContext(ctx); err != nil {
		return err
	}

	var missing []string

	for name, present := range map[string]bool{
		"database":      c.Database != nil,
		"identity":      c.Identity != nil,
		"authorization": c.Authorization != nil,
		"audit":         c.Audit != nil,
		"tokens":        c.Tokens != nil,
		"webAuthn":      c.WebAuthn != nil,
		"email":         c.Email != nil,
		"grpcServer":    c.GRPCServer != nil,
	} {
		if !present {
			missing = append(missing, name)
		}
	}

	if len(missing) > 0 {
		slices.Sort(missing)

		return platformerrors.Newf("serving requires these config blocks, which are absent: %s", strings.Join(missing, ", "))
	}

	return nil
}

// NewPillars builds the observability pillars for the CLI process.
//
// Turning on real telemetry is a config change rather than an edit here: name a
// provider in the JSON or in a FIJO_OBSERVABILITY_*_PROVIDER variable and
// the aggregate constructor wires it, exporters and all.
//
// The unconfigured case is built here instead, and the reason is narrower than
// it looks. observability.Config.NewPillars resolves a pillar naming no provider
// to that pillar's noop, which is the same result — but the tracing sub-config
// announces the decision with an unconditional INFO line on its way there, and
// its two siblings do not. Structured logs go to stdout, which is where the
// version subcommand's output goes too, so delegating unconditionally would put
// a log line in front of every `fijo version` at the default level and
// cost the template the machine-parseable output it promises. There is no
// setting that silences it; building the three noops directly is what does.
func (c *Config) NewPillars(ctx context.Context) (*observability.Pillars, error) {
	if c.telemetryConfigured() {
		return c.Observability.NewPillars(ctx)
	}

	logger, err := loggingcfg.NewLogger(ctx, &c.Observability.Logging)
	if err != nil {
		return nil, fmt.Errorf("setting up logger: %w", err)
	}

	return &observability.Pillars{
		Logger:          logger,
		TracerProvider:  tracingnoop.NewTracerProvider(),
		MetricsProvider: metricsnoop.NewMetricsProvider(),
		Profiler:        profilingnoop.NewProvider(),
	}, nil
}

// telemetryConfigured reports whether any of the three exporting pillars names a
// provider. Naming one — including "noop" — is a deliberate choice, so it is
// answered by the aggregate constructor and whatever that has to say about it.
func (c *Config) telemetryConfigured() bool {
	return c.Observability.Tracing.Provider != "" ||
		c.Observability.Metrics.Provider != "" ||
		c.Observability.Profiling.Provider != ""
}

// levelFromString maps a human-friendly level name onto a platform log level,
// defaulting to info for empty or unrecognized input.
func levelFromString(s string) logging.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case LevelDebug:
		return logging.DebugLevel
	case LevelWarn, "warning":
		return logging.WarnLevel
	case LevelError:
		return logging.ErrorLevel
	default:
		return logging.InfoLevel
	}
}
