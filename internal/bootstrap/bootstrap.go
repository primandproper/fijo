// Package bootstrap performs the two acts an empty database has no
// authenticated door for: provisioning a studio with the owner who runs it,
// and granting a user the service_admin role.
//
// Both are a registrar's acts. Identity's Register wants a caller holding
// identity.users.create and SetUserServiceRoles wants
// identity.users.update_service_roles — permissions nobody holds on a
// directory with no users in it. What authorizes the calls here is the
// database credential the caller already had to hold to reach this code, and
// that is the whole of the gate: this package is the ladder over the wall and
// is deliberately not reachable over the wire.
//
// Three callers share it. cmd/tools/bootstrap is the operator's, run once
// against a new deployment. cmd/tools/devseed is the developer's, run against
// a local stack. Container tests call it directly, because a test of anything
// scoped to a studio needs a studio before it can begin.
//
// Every studio after the first goes through the front door: once an operator
// exists, identity's Register RPC is an ordinary authorized call, and the
// studio CRUD the operator needs is the account surface identity already
// mounts.
package bootstrap

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/primandproper/fijo/internal/authz/roles"

	"github.com/primandproper/platform-go/v15/authentication/signin"
	"github.com/primandproper/platform-go/v15/identity"
	"github.com/primandproper/primitives-go/v2/database"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/tenancy"
)

// The defaults a caller may leave unset.
const (
	// DefaultTimeZone is the studio time zone a caller naming none gets. A
	// studio's time zone is a settled fact of the domain rather than a
	// preference, so a seeded one is UTC and obviously wrong rather than
	// US/Central and plausibly wrong.
	DefaultTimeZone = "UTC"

	// DefaultOperatorAccountName names the account identity mints for an
	// operator, who owns one because identity has no user without one.
	DefaultOperatorAccountName = "Operator"
)

// The reasons this package refuses.
var (
	// ErrNilRegistrar is returned by New when it is handed no registrar.
	ErrNilRegistrar = platformerrors.New("nil registrar")

	// ErrNilDirectory is returned by New when it is handed no role setter.
	ErrNilDirectory = platformerrors.New("nil directory")

	// ErrNilLookup is returned by New when it is handed no user lookup.
	ErrNilLookup = platformerrors.New("nil user lookup")

	// ErrNilReader is returned by New when it is handed no read executor.
	ErrNilReader = platformerrors.New("nil read executor")

	// ErrIncomplete is returned when a Studio or an Admin leaves a field unset
	// that this package cannot invent a value for.
	ErrIncomplete = platformerrors.New("incomplete seed")

	// ErrNilStudio is returned by ProvisionStudio when it is handed no studio.
	ErrNilStudio = platformerrors.New("nil studio")

	// ErrNilAdmin is returned by EnsureServiceAdmin when it is handed no admin.
	ErrNilAdmin = platformerrors.New("nil admin")
)

// Registrar is the door a registration goes through.
//
// platform-go's *signin.Service is what the tools hand it: it hashes the
// credential, mints the email verification token and fires the sign-in hooks
// that write the audit entry, none of which this package should reimplement.
// It is an interface so a caller with no token stack to build — a container
// test — can register through identity directly.
type Registrar interface {
	Register(ctx context.Context, scope tenancy.Scope, registration *signin.Registration) (*signin.Registered, error)
}

// RoleSetter grants a user their service roles. *identity.Service is the
// implementation; the interface is here so the promotion can be tested
// without a database.
type RoleSetter interface {
	SetUserServiceRoles(ctx context.Context, scope tenancy.Scope, userID string, serviceRoles []string) (*identity.User, error)
}

// Lookup reads a user back by the handle they sign in with, which is how
// EnsureServiceAdmin decides whether it has anything to do. identity.Store is
// the implementation.
type Lookup interface {
	GetUserByUsername(ctx context.Context, q database.SQLQueryExecutor, scope tenancy.Scope, username string) (*identity.User, error)
}

// Studio names a studio and the owner who runs it.
//
// The studio is an identity account and Fijo's tenant: every row the owner
// goes on to write carries its ID as the scope.
type Studio struct {
	// Username is the handle the owner signs in with.
	Username string

	// EmailAddress is where their password reset goes. It is folded to lower
	// case before it is written.
	EmailAddress string

	// Password is the credential they sign in with. It is hashed by the
	// registrar and never stored as given.
	Password string

	// FirstName and LastName are optional.
	FirstName string
	LastName  string

	// Name is the studio's name, and is required.
	Name string

	// TimeZone is the studio's IANA time zone. Empty means DefaultTimeZone.
	TimeZone string
}

// Admin names the operator of a deployment.
//
// They own an account because identity has no user without one; it is not a
// studio anybody teaches in.
type Admin struct {
	// Username is the handle the operator signs in with, and is what
	// EnsureServiceAdmin looks them up by.
	Username string

	// EmailAddress is where their password reset goes.
	EmailAddress string

	// Password is the credential. It is used only when the operator does not
	// exist yet; promoting one who does never touches it.
	Password string

	// FirstName and LastName are optional.
	FirstName string
	LastName  string

	// AccountName names the account minted for them. Empty means
	// DefaultOperatorAccountName.
	AccountName string

	// TimeZone is that account's time zone. Empty means DefaultTimeZone.
	TimeZone string
}

// Seeder holds the seams the two acts need.
type Seeder struct {
	registrar Registrar
	directory RoleSetter
	users     Lookup
	reader    database.SQLQueryExecutor
}

// New builds a Seeder over the four seams.
func New(registrar Registrar, directory RoleSetter, users Lookup, reader database.SQLQueryExecutor) (*Seeder, error) {
	switch {
	case registrar == nil:
		return nil, ErrNilRegistrar
	case directory == nil:
		return nil, ErrNilDirectory
	case users == nil:
		return nil, ErrNilLookup
	case reader == nil:
		return nil, ErrNilReader
	}

	return &Seeder{registrar: registrar, directory: directory, users: users, reader: reader}, nil
}

// ProvisionStudio registers the owner and mints their studio, on one
// transaction, through the registrar.
//
// It is not idempotent: a username the directory already holds is the
// registrar's refusal, not this package's, because a second call naming an
// existing owner is a mistake rather than a repeat.
func (s *Seeder) ProvisionStudio(ctx context.Context, studio *Studio) (*signin.Registered, error) {
	if studio == nil {
		return nil, ErrNilStudio
	}

	registration, err := studio.registration()
	if err != nil {
		return nil, err
	}

	registered, err := s.registrar.Register(ctx, tenancy.Global(), registration)
	if err != nil {
		return nil, platformerrors.Wrap(err, "provisioning a studio")
	}

	return registered, nil
}

// UserExists reports whether the directory already holds username. It is what
// a caller asks before ProvisionStudio when a repeat is expected rather than a
// mistake, as a local stack seeding itself on every start is.
func (s *Seeder) UserExists(ctx context.Context, username string) (bool, error) {
	_, err := s.users.GetUserByUsername(ctx, s.reader, tenancy.Global(), strings.TrimSpace(username))

	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, identity.ErrUserNotFound):
		return false, nil
	default:
		return false, platformerrors.Wrapf(err, "looking up %q", username)
	}
}

// EnsureServiceAdmin makes the named operator exist and hold service_admin.
//
// It is idempotent in both halves: an operator the directory already holds is
// not registered again, and one who already holds the role is not written
// again. Re-running it against a deployment that has it is a pair of reads.
//
// The role is set rather than added to, as platform-go's own bootstrap does.
// service_admin inherits owner in Fijo's policy, so the roles it replaces are
// roles it already grants.
func (s *Seeder) EnsureServiceAdmin(ctx context.Context, admin *Admin) (*identity.User, error) {
	if admin == nil {
		return nil, ErrNilAdmin
	}

	if strings.TrimSpace(admin.Username) == "" {
		return nil, platformerrors.Wrap(ErrIncomplete, "missing username")
	}

	username := strings.TrimSpace(admin.Username)

	user, err := s.users.GetUserByUsername(ctx, s.reader, tenancy.Global(), username)

	switch {
	case err == nil:
	case errors.Is(err, identity.ErrUserNotFound):
		registered, registerErr := s.ProvisionStudio(ctx, admin.studio())
		if registerErr != nil {
			return nil, registerErr
		}

		user = registered.User
	default:
		return nil, platformerrors.Wrapf(err, "looking up the operator %q", username)
	}

	if slices.Contains(user.ServiceRoles, roles.ServiceAdmin) {
		return user, nil
	}

	promoted, err := s.directory.SetUserServiceRoles(ctx, tenancy.Global(), user.ID, []string{roles.ServiceAdmin})
	if err != nil {
		return nil, platformerrors.Wrapf(err, "promoting %q to %s", username, roles.ServiceAdmin)
	}

	return promoted, nil
}

// registration turns a Studio into what the registrar wants, defaulting what
// it may default and refusing what it may not.
func (s *Studio) registration() (*signin.Registration, error) {
	for field, value := range map[string]string{
		"username":      s.Username,
		"email address": s.EmailAddress,
		"password":      s.Password,
		"studio name":   s.Name,
	} {
		if strings.TrimSpace(value) == "" {
			return nil, platformerrors.Wrapf(ErrIncomplete, "missing %s", field)
		}
	}

	timeZone := strings.TrimSpace(s.TimeZone)
	if timeZone == "" {
		timeZone = DefaultTimeZone
	}

	return &signin.Registration{
		User: &identity.User{
			Username:      strings.TrimSpace(s.Username),
			EmailAddress:  strings.ToLower(strings.TrimSpace(s.EmailAddress)),
			FirstName:     s.FirstName,
			LastName:      s.LastName,
			DisplayName:   s.FirstName,
			AccountStatus: identity.StatusGood,
		},
		Account:    &identity.Account{Name: strings.TrimSpace(s.Name), TimeZone: timeZone},
		Credential: signin.Password(s.Password),
		OwnerRoles: []string{roles.Owner},
	}, nil
}

// studio is the registration an operator who does not exist yet goes through.
func (a *Admin) studio() *Studio {
	accountName := strings.TrimSpace(a.AccountName)
	if accountName == "" {
		accountName = DefaultOperatorAccountName
	}

	return &Studio{
		Username:     a.Username,
		EmailAddress: a.EmailAddress,
		Password:     a.Password,
		FirstName:    a.FirstName,
		LastName:     a.LastName,
		Name:         accountName,
		TimeZone:     a.TimeZone,
	}
}
