package principal

import (
	"context"
	"testing"

	"github.com/primandproper/platform-go/v15/identity"
	"github.com/primandproper/primitives-go/v2/authorization"
	"github.com/primandproper/primitives-go/v2/tenancy"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestCaller(t *testing.T) {
	t.Parallel()

	caller := &Caller{
		Principal: &identity.Principal{
			User:            &identity.User{ID: "user-1", Scope: tenancy.Global()},
			ActiveAccountID: "studio-1",
		},
		Grants:    authorization.NewGrants(authorization.NewPermissionSet("audit.entries.read")),
		SessionID: "session-1",
	}

	t.Run("reads the three facts a platform surface wants", func(t *testing.T) {
		t.Parallel()

		test.Eq(t, "user-1", caller.UserID())
		test.Eq(t, "studio-1", caller.ActiveAccountID())
		test.Eq(t, tenancy.Global(), caller.Scope())
		test.Eq(t, tenancy.Of("studio-1"), caller.StudioScope())
	})

	t.Run("a caller with no studio is in the global scope", func(t *testing.T) {
		t.Parallel()

		operator := &Caller{Principal: &identity.Principal{User: &identity.User{ID: "op"}}}
		test.Eq(t, tenancy.Global(), operator.StudioScope())
	})

	t.Run("the tenant a platform surface reads is the studio", func(t *testing.T) {
		t.Parallel()

		scope, err := Tenant(caller)
		must.NoError(t, err)
		test.Eq(t, tenancy.Of("studio-1"), scope)

		_, err = Tenant(nil)
		test.ErrorIs(t, err, ErrNotACaller)
	})

	t.Run("round-trips through a context", func(t *testing.T) {
		t.Parallel()

		ctx := With(context.Background(), caller)

		got, ok := FromContext(ctx)
		must.True(t, ok)
		test.Eq(t, caller, got)

		extracted, ok := Extract(ctx)
		must.True(t, ok)
		test.Eq(t, "user-1", extracted.UserID())

		grants, ok := Grants(ctx)
		must.True(t, ok)
		test.True(t, grants.Has("audit.entries.read"))
	})

	t.Run("an empty context has nobody on it and grants nothing", func(t *testing.T) {
		t.Parallel()

		_, ok := FromContext(context.Background())
		test.False(t, ok)

		_, ok = Extract(context.Background())
		test.False(t, ok)

		grants, ok := Grants(context.Background())
		test.False(t, ok)
		test.False(t, grants.Has("audit.entries.read"))
	})

	t.Run("a caller with no user is nobody", func(t *testing.T) {
		t.Parallel()

		_, ok := FromContext(With(context.Background(), &Caller{Principal: &identity.Principal{}}))
		test.False(t, ok)
	})
}
