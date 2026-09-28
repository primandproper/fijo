package bootstrap

import (
	"context"
	"errors"
	"testing"

	"github.com/primandproper/fijo/internal/authz/roles"

	"github.com/primandproper/platform-go/v14/authentication/signin"
	"github.com/primandproper/platform-go/v14/identity"
	"github.com/primandproper/primitives-go/v2/database"
	databasemock "github.com/primandproper/primitives-go/v2/database/mock"
	"github.com/primandproper/primitives-go/v2/tenancy"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

// registrarFunc is a Registrar that is whatever the test says it is.
type registrarFunc func(ctx context.Context, scope tenancy.Scope, registration *signin.Registration) (*signin.Registered, error)

func (f registrarFunc) Register(ctx context.Context, scope tenancy.Scope, registration *signin.Registration) (*signin.Registered, error) {
	return f(ctx, scope, registration)
}

// roleSetterFunc is a RoleSetter that counts its calls.
type roleSetterFunc struct {
	fn    func(userID string, serviceRoles []string) (*identity.User, error)
	calls int
}

func (r *roleSetterFunc) SetUserServiceRoles(_ context.Context, _ tenancy.Scope, userID string, serviceRoles []string) (*identity.User, error) {
	r.calls++

	return r.fn(userID, serviceRoles)
}

// lookupFunc is a Lookup that is whatever the test says it is.
type lookupFunc func(username string) (*identity.User, error)

func (f lookupFunc) GetUserByUsername(_ context.Context, _ database.SQLQueryExecutor, _ tenancy.Scope, username string) (*identity.User, error) {
	return f(username)
}

func reader() database.SQLQueryExecutor { return &databasemock.SQLQueryExecutorMock{} }

func refusing(t *testing.T) registrarFunc {
	t.Helper()

	return func(context.Context, tenancy.Scope, *signin.Registration) (*signin.Registered, error) {
		t.Error("the registrar was called when it should not have been")

		return nil, errors.New("unexpected registration")
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	registrar := registrarFunc(func(context.Context, tenancy.Scope, *signin.Registration) (*signin.Registered, error) {
		return nil, nil
	})
	setter := &roleSetterFunc{}
	lookup := lookupFunc(func(string) (*identity.User, error) { return nil, nil })

	t.Run("builds over four seams", func(t *testing.T) {
		t.Parallel()

		seeder, err := New(registrar, setter, lookup, reader())
		must.NoError(t, err)
		must.NotNil(t, seeder)
	})

	t.Run("names the seam it was not given", func(t *testing.T) {
		t.Parallel()

		for name, build := range map[string]func() (*Seeder, error){
			"registrar": func() (*Seeder, error) { return New(nil, setter, lookup, reader()) },
			"directory": func() (*Seeder, error) { return New(registrar, nil, lookup, reader()) },
			"lookup":    func() (*Seeder, error) { return New(registrar, setter, nil, reader()) },
			"reader":    func() (*Seeder, error) { return New(registrar, setter, lookup, nil) },
		} {
			seeder, err := build()
			test.Error(t, err, test.Sprintf("a nil %s must be refused", name))
			test.Nil(t, seeder)
		}
	})
}

func TestSeeder_ProvisionStudio(t *testing.T) {
	t.Parallel()

	complete := &Studio{
		Username:     "  maria ",
		EmailAddress: "  Maria@Example.COM ",
		Password:     "a good long password",
		FirstName:    "Maria",
		Name:         " Casa de Musica ",
		TimeZone:     "US/Central",
	}

	t.Run("registers the owner as the studio's owner", func(t *testing.T) {
		t.Parallel()

		var seen *signin.Registration

		seeder, err := New(registrarFunc(func(_ context.Context, scope tenancy.Scope, registration *signin.Registration) (*signin.Registered, error) {
			seen = registration

			test.Eq(t, tenancy.Global(), scope)

			return &signin.Registered{User: &identity.User{ID: "user"}, Account: &identity.Account{ID: "studio"}}, nil
		}), &roleSetterFunc{}, lookupFunc(func(string) (*identity.User, error) { return nil, nil }), reader())
		must.NoError(t, err)

		registered, err := seeder.ProvisionStudio(t.Context(), complete)
		must.NoError(t, err)
		test.Eq(t, "studio", registered.Account.ID)

		must.NotNil(t, seen)
		test.Eq(t, "maria", seen.User.Username)
		test.Eq(t, "maria@example.com", seen.User.EmailAddress)
		test.Eq(t, identity.StatusGood, seen.User.AccountStatus)
		test.Eq(t, "Casa de Musica", seen.Account.Name)
		test.Eq(t, "US/Central", seen.Account.TimeZone)
		test.Eq(t, []string{roles.Owner}, seen.OwnerRoles)
	})

	t.Run("defaults the time zone rather than guessing one", func(t *testing.T) {
		t.Parallel()

		var seen *signin.Registration

		withoutZone := *complete
		withoutZone.TimeZone = ""

		seeder, err := New(registrarFunc(func(_ context.Context, _ tenancy.Scope, registration *signin.Registration) (*signin.Registered, error) {
			seen = registration

			return &signin.Registered{User: &identity.User{}, Account: &identity.Account{}}, nil
		}), &roleSetterFunc{}, lookupFunc(func(string) (*identity.User, error) { return nil, nil }), reader())
		must.NoError(t, err)

		_, err = seeder.ProvisionStudio(t.Context(), &withoutZone)
		must.NoError(t, err)
		must.NotNil(t, seen)
		test.Eq(t, DefaultTimeZone, seen.Account.TimeZone)
	})

	t.Run("refuses what it cannot invent", func(t *testing.T) {
		t.Parallel()

		for name, mangle := range map[string]func(Studio) *Studio{
			"username":      func(s Studio) *Studio { s.Username = " "; return &s },
			"email address": func(s Studio) *Studio { s.EmailAddress = ""; return &s },
			"password":      func(s Studio) *Studio { s.Password = ""; return &s },
			"studio name":   func(s Studio) *Studio { s.Name = ""; return &s },
		} {
			seeder, err := New(refusing(t), &roleSetterFunc{}, lookupFunc(func(string) (*identity.User, error) { return nil, nil }), reader())
			must.NoError(t, err)

			_, err = seeder.ProvisionStudio(t.Context(), mangle(*complete))
			test.ErrorIs(t, err, ErrIncomplete, test.Sprintf("a missing %s must be refused", name))
		}
	})

	t.Run("carries the registrar's refusal out", func(t *testing.T) {
		t.Parallel()

		sentinel := errors.New("username is taken")

		seeder, err := New(registrarFunc(func(context.Context, tenancy.Scope, *signin.Registration) (*signin.Registered, error) {
			return nil, sentinel
		}), &roleSetterFunc{}, lookupFunc(func(string) (*identity.User, error) { return nil, nil }), reader())
		must.NoError(t, err)

		_, err = seeder.ProvisionStudio(t.Context(), complete)
		test.ErrorIs(t, err, sentinel)
	})
}

func TestSeeder_EnsureServiceAdmin(t *testing.T) {
	t.Parallel()

	admin := &Admin{Username: "jeffrey", EmailAddress: "jeffrey@example.com", Password: "a good long password"}

	t.Run("promotes an operator the directory already holds", func(t *testing.T) {
		t.Parallel()

		setter := &roleSetterFunc{fn: func(userID string, serviceRoles []string) (*identity.User, error) {
			test.Eq(t, "existing", userID)
			test.Eq(t, []string{roles.ServiceAdmin}, serviceRoles)

			return &identity.User{ID: userID, Username: "jeffrey", ServiceRoles: serviceRoles}, nil
		}}

		seeder, err := New(refusing(t), setter, lookupFunc(func(username string) (*identity.User, error) {
			test.Eq(t, "jeffrey", username)

			return &identity.User{ID: "existing", Username: "jeffrey"}, nil
		}), reader())
		must.NoError(t, err)

		operator, err := seeder.EnsureServiceAdmin(t.Context(), admin)
		must.NoError(t, err)
		test.Eq(t, []string{roles.ServiceAdmin}, operator.ServiceRoles)
		test.Eq(t, 1, setter.calls)
	})

	t.Run("writes nothing for an operator who already holds the role", func(t *testing.T) {
		t.Parallel()

		setter := &roleSetterFunc{fn: func(string, []string) (*identity.User, error) {
			t.Error("an operator who already holds the role must not be written again")

			return nil, errors.New("unexpected promotion")
		}}

		seeder, err := New(refusing(t), setter, lookupFunc(func(string) (*identity.User, error) {
			return &identity.User{ID: "existing", ServiceRoles: []string{roles.ServiceAdmin}}, nil
		}), reader())
		must.NoError(t, err)

		operator, err := seeder.EnsureServiceAdmin(t.Context(), admin)
		must.NoError(t, err)
		test.Eq(t, "existing", operator.ID)
		test.Eq(t, 0, setter.calls)
	})

	t.Run("registers an operator the directory does not hold", func(t *testing.T) {
		t.Parallel()

		var seen *signin.Registration

		setter := &roleSetterFunc{fn: func(userID string, serviceRoles []string) (*identity.User, error) {
			return &identity.User{ID: userID, ServiceRoles: serviceRoles}, nil
		}}

		seeder, err := New(registrarFunc(func(_ context.Context, _ tenancy.Scope, registration *signin.Registration) (*signin.Registered, error) {
			seen = registration

			return &signin.Registered{User: &identity.User{ID: "minted"}, Account: &identity.Account{ID: "operator"}}, nil
		}), setter, lookupFunc(func(string) (*identity.User, error) {
			return nil, identity.ErrUserNotFound
		}), reader())
		must.NoError(t, err)

		operator, err := seeder.EnsureServiceAdmin(t.Context(), admin)
		must.NoError(t, err)
		test.Eq(t, "minted", operator.ID)
		test.Eq(t, []string{roles.ServiceAdmin}, operator.ServiceRoles)
		test.Eq(t, 1, setter.calls)

		must.NotNil(t, seen)
		test.Eq(t, DefaultOperatorAccountName, seen.Account.Name)
		test.Eq(t, DefaultTimeZone, seen.Account.TimeZone)
	})

	t.Run("does not register over a lookup that failed for another reason", func(t *testing.T) {
		t.Parallel()

		sentinel := errors.New("the database is down")

		setter := &roleSetterFunc{fn: func(string, []string) (*identity.User, error) {
			t.Error("nothing should be promoted when the lookup failed")

			return nil, sentinel
		}}

		seeder, err := New(refusing(t), setter, lookupFunc(func(string) (*identity.User, error) {
			return nil, sentinel
		}), reader())
		must.NoError(t, err)

		_, err = seeder.EnsureServiceAdmin(t.Context(), admin)
		test.ErrorIs(t, err, sentinel)
		test.Eq(t, 0, setter.calls)
	})

	t.Run("refuses an operator with no username", func(t *testing.T) {
		t.Parallel()

		seeder, err := New(refusing(t), &roleSetterFunc{}, lookupFunc(func(string) (*identity.User, error) {
			t.Error("a nameless operator must not be looked up")

			return nil, nil
		}), reader())
		must.NoError(t, err)

		_, err = seeder.EnsureServiceAdmin(t.Context(), &Admin{Username: "  "})
		test.ErrorIs(t, err, ErrIncomplete)
	})
}

func TestSeeder_UserExists(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		lookupErr error
		wantErr   error
		want      bool
	}{
		"a user the directory holds":         {want: true},
		"a user the directory does not hold": {lookupErr: identity.ErrUserNotFound},
		"a lookup that failed":               {lookupErr: errDatabaseDown, wantErr: errDatabaseDown},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var looked string

			seeder, err := New(refusing(t), &roleSetterFunc{}, lookupFunc(func(username string) (*identity.User, error) {
				looked = username

				if tc.lookupErr != nil {
					return nil, tc.lookupErr
				}

				return &identity.User{ID: "existing"}, nil
			}), reader())
			must.NoError(t, err)

			exists, err := seeder.UserExists(t.Context(), " maria ")
			test.ErrorIs(t, err, tc.wantErr)
			test.Eq(t, tc.want, exists)
			test.Eq(t, "maria", looked)
		})
	}
}

var errDatabaseDown = errors.New("the database is down")
