package auditing

import (
	"context"
	"testing"
	"time"

	"github.com/primandproper/platform-go/v15/audit"
	auditmock "github.com/primandproper/platform-go/v15/audit/mock"
	"github.com/primandproper/platform-go/v15/authentication/signin"
	"github.com/primandproper/platform-go/v15/identity"
	"github.com/primandproper/primitives-go/v2/database"
	"github.com/primandproper/primitives-go/v2/tenancy"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

type recorded struct {
	scope   tenancy.Scope
	entries []*audit.Entry
}

func newHooks(t *testing.T) (*Hooks, *[]recorded) {
	t.Helper()

	var got []recorded

	recorder := &auditmock.RecorderMock{
		RecordFunc: func(_ context.Context, _ database.Tx, scope tenancy.Scope, entries ...*audit.Entry) error {
			got = append(got, recorded{scope: scope, entries: entries})

			return nil
		},
	}

	h, err := New(recorder)
	must.NoError(t, err)

	return h, &got
}

func TestHooks(t *testing.T) {
	t.Parallel()

	t.Run("a registration is three entries in the new studio's scope", func(t *testing.T) {
		t.Parallel()

		h, got := newHooks(t)

		must.NoError(t, h.AfterRegister(t.Context(), nil, tenancy.Global(), &identity.Registration{
			User:       &identity.User{ID: "u1"},
			Account:    &identity.Account{ID: "a1"},
			Membership: &identity.Membership{ID: "m1", Roles: []string{"owner"}},
		}))

		must.SliceLen(t, 1, *got)
		test.Eq(t, tenancy.Of("a1"), (*got)[0].scope)
		must.SliceLen(t, 3, (*got)[0].entries)

		types := []string{}
		for _, e := range (*got)[0].entries {
			types = append(types, e.ResourceType)
			test.Eq(t, audit.EventCreated, e.EventType)
			test.Eq(t, "u1", e.Actor.ID)
			test.NotEq(t, "", e.ID)
		}

		test.Eq(t, []string{ResourceUser, ResourceAccount, ResourceMembership}, types)
		test.Eq(t, "owner", (*got)[0].entries[2].Metadata[KeyRoles])
	})

	t.Run("a sign-in lands in the active studio's scope", func(t *testing.T) {
		t.Parallel()

		h, got := newHooks(t)

		principal := &identity.Principal{User: &identity.User{ID: "u1"}, ActiveAccountID: "a1"}

		must.NoError(t, h.AfterAuthenticate(t.Context(), nil, tenancy.Global(), &signin.Authentication{Principal: principal}))
		must.NoError(t, h.AfterIssueToken(t.Context(), nil, tenancy.Global(), &signin.SignIn{Principal: principal, FamilyID: "f1"}))

		must.SliceLen(t, 2, *got)
		test.Eq(t, tenancy.Of("a1"), (*got)[0].scope)
		test.Eq(t, audit.EventAccessed, (*got)[0].entries[0].EventType)
		test.Eq(t, "f1", (*got)[1].entries[0].Metadata[KeyFamilyID])
	})

	t.Run("a failed sign-in for nobody is unattributed and global", func(t *testing.T) {
		t.Parallel()

		h, got := newHooks(t)

		must.NoError(t, h.AfterFailedSignIn(t.Context(), nil, tenancy.Global(), &signin.FailedSignIn{
			Handle: "ghost",
			Reason: signin.ErrInvalidCredentials,
		}))

		must.SliceLen(t, 1, *got)
		test.Eq(t, tenancy.Global(), (*got)[0].scope)
		test.Eq(t, audit.ActorUnattributed, (*got)[0].entries[0].Actor.ID)
		test.Eq(t, "ghost", (*got)[0].entries[0].Metadata[KeyHandle])
	})

	t.Run("a password write records the column and never the value", func(t *testing.T) {
		t.Parallel()

		h, got := newHooks(t)

		must.NoError(t, h.AfterUpdateUserPassword(t.Context(), nil, tenancy.Global(), &identity.User{ID: "u1", HashedPassword: "argon2$secret"}, true))

		must.SliceLen(t, 1, *got)

		for k, v := range (*got)[0].entries[0].Metadata {
			test.StrNotContains(t, v, "secret", test.Sprintf("metadata %s", k))
		}

		test.Eq(t, fieldHashedPassword, (*got)[0].entries[0].Metadata[KeyFields])
	})

	t.Run("a studio edit names the fields that moved and not the save's timestamp", func(t *testing.T) {
		t.Parallel()

		h, got := newHooks(t)

		before := &identity.Account{ID: "a1", OwnerUserID: "u1", Name: "Old"}
		after := &identity.Account{ID: "a1", OwnerUserID: "u1", Name: "New", LastUpdatedAt: new(time.Now())}

		must.NoError(t, h.AfterUpdateAccount(t.Context(), nil, tenancy.Global(), before, after))

		must.SliceLen(t, 1, *got)
		test.Eq(t, tenancy.Of("a1"), (*got)[0].scope)
		test.Eq(t, "name", (*got)[0].entries[0].Metadata[KeyFields])
	})

	t.Run("a save that moved nothing but its timestamp records nothing", func(t *testing.T) {
		t.Parallel()

		h, got := newHooks(t)

		before := &identity.Account{ID: "a1", OwnerUserID: "u1", Name: "Same"}
		after := &identity.Account{ID: "a1", OwnerUserID: "u1", Name: "Same", LastUpdatedAt: new(time.Now())}

		must.NoError(t, h.AfterUpdateAccount(t.Context(), nil, tenancy.Global(), before, after))
		test.SliceLen(t, 0, *got)
	})

	t.Run("nil payloads record nothing", func(t *testing.T) {
		t.Parallel()

		h, got := newHooks(t)

		must.NoError(t, h.AfterRegister(t.Context(), nil, tenancy.Global(), nil))
		must.NoError(t, h.AfterUpdateAccount(t.Context(), nil, tenancy.Global(), nil, nil))
		must.NoError(t, h.AfterUpdateProfile(t.Context(), nil, tenancy.Global(), &identity.User{ID: "u1"}, nil))
		test.SliceLen(t, 0, *got)
	})

	t.Run("refuses a nil recorder", func(t *testing.T) {
		t.Parallel()

		_, err := New(nil)
		test.ErrorIs(t, err, ErrNilRecorder)
	})
}
