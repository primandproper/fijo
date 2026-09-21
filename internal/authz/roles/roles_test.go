package roles

import (
	"testing"

	auditgrpc "github.com/primandproper/platform-go/v14/audit/grpc"
	identitygrpc "github.com/primandproper/platform-go/v14/identity/grpc"
	"github.com/primandproper/primitives-go/v2/authorization"
	"github.com/primandproper/primitives-go/v2/authorization/static"

	"github.com/shoenig/test"
	"github.com/shoenig/test/must"
)

func TestPolicy(t *testing.T) {
	t.Parallel()

	t.Run("is a valid policy", func(t *testing.T) {
		t.Parallel()

		must.NoError(t, Policy().Validate())
	})

	t.Run("owners inherit staff and admins inherit owners", func(t *testing.T) {
		t.Parallel()

		resolver, err := static.NewResolver(Roles())
		must.NoError(t, err)

		staff, err := resolver.PermissionsForRoles(t.Context(), Staff)
		must.NoError(t, err)
		test.True(t, staff.Has(identitygrpc.PermissionReadAccounts))
		test.False(t, staff.Has(identitygrpc.PermissionManageMembers))

		owner, err := resolver.PermissionsForRoles(t.Context(), Owner)
		must.NoError(t, err)
		test.True(t, owner.Has(identitygrpc.PermissionReadAccounts))
		test.True(t, owner.Has(identitygrpc.PermissionManageMembers))
		test.True(t, owner.Has(auditgrpc.PermissionReadEntries))
		test.False(t, owner.Has(identitygrpc.PermissionListAllAccounts))

		admin, err := resolver.PermissionsForRoles(t.Context(), ServiceAdmin)
		must.NoError(t, err)
		test.True(t, admin.Has(identitygrpc.PermissionManageMembers))
		test.True(t, admin.Has(identitygrpc.PermissionListAllAccounts))
	})

	t.Run("nobody is granted the whole identity surface by accident", func(t *testing.T) {
		t.Parallel()

		resolver, err := static.NewResolver(Roles())
		must.NoError(t, err)

		owner, err := resolver.PermissionsForRoles(t.Context(), Owner)
		must.NoError(t, err)

		for _, forbidden := range []authorization.Permission{
			identitygrpc.PermissionArchiveUsers,
			identitygrpc.PermissionUpdateUserServiceRoles,
			auditgrpc.PermissionVerifyChain,
		} {
			test.False(t, owner.Has(forbidden), test.Sprintf("owner must not hold %s", forbidden))
		}
	})
}
