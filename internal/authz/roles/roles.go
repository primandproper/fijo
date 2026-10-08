// Package roles is Fijo's authorization policy: the roles a principal can hold
// and what each one grants.
//
// The studio is an identity account and Fijo's tenant. Its people hold one of
// two account roles — Owner runs the studio, Staff teaches in it — and the
// operator of the deployment holds the ServiceAdmin service role. The policy is
// written once here, as the []authorization.Role platform-go's rbac package
// seeds into its tables on every migration, so the roles the interceptor
// resolves against are the roles this file says and never a copy of them.
//
// Only platform permissions appear in it today. Fijo's own RPCs are all
// self-service in phase 0 — a caller acts on their own passkeys, their own
// session and their own time zone — and the identity convention for those is
// to declare them public to the enforcer and refuse a request with no
// principal inside the method, which is what Fijo's servers do. A permission
// of Fijo's own arrives with the first RPC that acts on somebody else's row.
package roles

import (
	auditgrpc "github.com/primandproper/platform-go/v15/audit/grpc"
	identitygrpc "github.com/primandproper/platform-go/v15/identity/grpc"
	"github.com/primandproper/platform-go/v15/rbac"
	"github.com/primandproper/primitives-go/v2/authorization"
)

// The role names. They are what identity_membership_roles and
// identity_user_roles hold, and the seeded authz_roles rows they reference.
const (
	// Owner runs a studio: its people, its settings, its audit trail.
	Owner = "owner"

	// Staff teaches in a studio and reads what they need to.
	Staff = "staff"

	// ServiceAdmin operates the deployment. It is a service role, held on the
	// user rather than on a membership, and it inherits everything an owner has.
	ServiceAdmin = "service_admin"
)

// Policy is the whole of Fijo's authorization policy, in the shape rbac seeds.
func Policy() rbac.Policy {
	return rbac.Policy{Roles: Roles()}
}

// Roles is the declaration Policy wraps, for callers that hold a slice.
func Roles() []authorization.Role {
	return []authorization.Role{
		{
			Name:        ServiceAdmin,
			Description: "operator of the Fijo deployment",
			Permissions: []authorization.Permission{
				identitygrpc.PermissionReadUsers,
				identitygrpc.PermissionArchiveUsers,
				identitygrpc.PermissionUpdateUserStatus,
				identitygrpc.PermissionUpdateUserServiceRoles,
				identitygrpc.PermissionRequirePasswordChange,
				identitygrpc.PermissionListAllAccounts,
				identitygrpc.PermissionCreateAccounts,
				identitygrpc.PermissionTransferAccountOwnership,
				identitygrpc.PermissionArchiveAccounts,
				auditgrpc.PermissionVerifyChain,
			},
			Inherits: []string{Owner},
		},
		{
			Name:        Owner,
			Description: "runs a studio",
			Permissions: []authorization.Permission{
				identitygrpc.PermissionUpdateAccounts,
				identitygrpc.PermissionManageMembers,
				identitygrpc.PermissionInviteMembers,
				identitygrpc.PermissionReadInvitations,
				auditgrpc.PermissionReadEntries,
			},
			Inherits: []string{Staff},
		},
		{
			Name:        Staff,
			Description: "teaches in a studio",
			Permissions: []authorization.Permission{
				identitygrpc.PermissionReadAccounts,
			},
		},
	}
}
