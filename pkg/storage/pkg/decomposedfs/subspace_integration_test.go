package decomposedfs_test

import (
	"context"

	userpb "github.com/cs3org/go-cs3apis/cs3/identity/user/v1beta1"
	cs3permissions "github.com/cs3org/go-cs3apis/cs3/permissions/v1beta1"
	rpcv1beta1 "github.com/cs3org/go-cs3apis/cs3/rpc/v1beta1"
	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ctxpkg "github.com/opencloud-eu/reva/v2/pkg/ctx"
	"github.com/opencloud-eu/reva/v2/pkg/storage/pkg/decomposedfs/node"
	helpers "github.com/opencloud-eu/reva/v2/pkg/storage/pkg/decomposedfs/testhelpers"
	"github.com/stretchr/testify/mock"
)

// Test structure:
//
//	Space-Root (project space, owner: env.Owner)
//	  ├── finance/
//	  │    ├── budget/    ← subspace, editor grant: userA
//	  │    └── taxes/
//	  ├── hr/             ← subspace, editor grant: userA (second subspace)
//	  └── legal/

var _ = Describe("Subspace integration", func() {
	var (
		env *helpers.DecomposedTestEnv

		userA *userpb.User
		userB *userpb.User

		editorPerms *provider.ResourcePermissions
	)

	BeforeEach(func() {
		var err error
		env, err = helpers.NewTestEnv(nil)
		Expect(err).ToNot(HaveOccurred())

		// Allow all CheckPermission calls (ManageSpaceProperties, SubspaceManagement, etc.)
		env.PermissionsClient.ExpectedCalls = nil
		env.PermissionsClient.On("CheckPermission", mock.Anything, mock.Anything, mock.Anything).Return(
			&cs3permissions.CheckPermissionResponse{
				Status: &rpcv1beta1.Status{Code: rpcv1beta1.Code_CODE_OK},
			}, nil,
		)

		// Owner can do everything during setup
		env.Permissions.ExpectedCalls = nil
		env.Permissions.On("AssemblePermissions", mock.Anything, mock.Anything).Return(
			node.OwnerPermissions(), nil,
		)

		userA = &userpb.User{
			Id:     &userpb.UserId{Idp: "idp", OpaqueId: "user-a", Type: userpb.UserType_USER_TYPE_PRIMARY},
			Groups: []string{},
		}
		userB = &userpb.User{
			Id:     &userpb.UserId{Idp: "idp", OpaqueId: "user-b", Type: userpb.UserType_USER_TYPE_PRIMARY},
			Groups: []string{},
		}

		editorPerms = &provider.ResourcePermissions{
			Stat: true, GetPath: true, ListContainer: true,
			InitiateFileDownload: true, InitiateFileUpload: true,
		}

		// Create a project space (needed for subspace support)
		projectRes, err := env.CreateTestStorageSpace("project", nil)
		Expect(err).ToNot(HaveOccurred())
		env.SpaceRootRes = projectRes

		// Create directory structure in the project space
		Expect(env.Fs.CreateDir(env.Ctx, &provider.Reference{ResourceId: env.SpaceRootRes, Path: "./finance"})).To(Succeed())
		Expect(env.Fs.CreateDir(env.Ctx, &provider.Reference{ResourceId: env.SpaceRootRes, Path: "./finance/budget"})).To(Succeed())
		Expect(env.Fs.CreateDir(env.Ctx, &provider.Reference{ResourceId: env.SpaceRootRes, Path: "./finance/taxes"})).To(Succeed())
		Expect(env.Fs.CreateDir(env.Ctx, &provider.Reference{ResourceId: env.SpaceRootRes, Path: "./hr"})).To(Succeed())
		Expect(env.Fs.CreateDir(env.Ctx, &provider.Reference{ResourceId: env.SpaceRootRes, Path: "./legal"})).To(Succeed())
	})

	AfterEach(func() {
		node.InvalidateSubspaceCache(env.SpaceRootRes.SpaceId)
		if env != nil {
			env.Cleanup()
		}
	})

	addGrant := func(path string, user *userpb.User) {
		Expect(env.Fs.AddGrant(env.Ctx, &provider.Reference{ResourceId: env.SpaceRootRes, Path: path}, &provider.Grant{
			Grantee:     &provider.Grantee{Type: provider.GranteeType_GRANTEE_TYPE_USER, Id: &provider.Grantee_UserId{UserId: user.Id}},
			Permissions: editorPerms,
		})).To(Succeed())
	}

	removeGrant := func(path string, user *userpb.User) {
		Expect(env.Fs.RemoveGrant(env.Ctx, &provider.Reference{ResourceId: env.SpaceRootRes, Path: path}, &provider.Grant{
			Grantee: &provider.Grantee{Type: provider.GranteeType_GRANTEE_TYPE_USER, Id: &provider.Grantee_UserId{UserId: user.Id}},
		})).To(Succeed())
	}

	listSpacesFor := func(user *userpb.User) []*provider.StorageSpace {
		ctx := ctxpkg.ContextSetUser(context.Background(), user)
		spaces, err := env.Fs.ListStorageSpaces(ctx, []*provider.ListStorageSpacesRequest_Filter{
			{Type: provider.ListStorageSpacesRequest_Filter_TYPE_USER, Term: &provider.ListStorageSpacesRequest_Filter_User{User: user.Id}},
		}, false)
		Expect(err).ToNot(HaveOccurred())
		return spaces
	}

	// listFolderNames uses the real permission engine (not mock) for user-specific listing
	listFolderNames := func(user *userpb.User, path string) []string {
		// Switch to real permissions for this call
		env.Permissions.ExpectedCalls = nil
		perms := node.NewPermissions(env.Lookup)
		env.Permissions.On("AssemblePermissions", mock.Anything, mock.Anything).Return(
			func(ctx context.Context, n *node.Node) (*provider.ResourcePermissions, error) {
				u, ok := ctxpkg.ContextGetUser(ctx)
				if !ok {
					return node.NoPermissions(), nil
				}
				if u.Id.OpaqueId == env.Owner.Id.OpaqueId {
					return node.OwnerPermissions(), nil
				}
				return perms.AssemblePermissions(ctx, n)
			}, nil,
		)

		ctx := ctxpkg.ContextSetUser(context.Background(), user)
		infos, err := env.Fs.ListFolder(ctx, &provider.Reference{ResourceId: env.SpaceRootRes, Path: path}, nil, nil)
		Expect(err).ToNot(HaveOccurred())

		// Restore owner-only mock for subsequent setup operations
		env.Permissions.ExpectedCalls = nil
		env.Permissions.On("AssemblePermissions", mock.Anything, mock.Anything).Return(
			node.OwnerPermissions(), nil,
		)

		names := make([]string, len(infos))
		for i, info := range infos {
			names[i] = info.Name
		}
		return names
	}

	Describe("Space visibility via subspace grants", func() {
		It("subspace grant adds space to user's ListStorageSpaces", func() {
			spaces := listSpacesFor(userA)
			Expect(spaces).To(BeEmpty())

			addGrant("./finance/budget", userA)

			spaces = listSpacesFor(userA)
			Expect(spaces).To(HaveLen(1))
		})

		It("two subspaces for same user shows space only once", func() {
			addGrant("./finance/budget", userA)
			addGrant("./hr", userA)

			spaces := listSpacesFor(userA)
			Expect(spaces).To(HaveLen(1))
		})

		It("removing one of two subspace grants keeps space visible", func() {
			addGrant("./finance/budget", userA)
			addGrant("./hr", userA)

			removeGrant("./finance/budget", userA)

			spaces := listSpacesFor(userA)
			Expect(spaces).To(HaveLen(1))
		})

		It("removing last subspace grant removes space from list", func() {
			addGrant("./finance/budget", userA)

			removeGrant("./finance/budget", userA)

			spaces := listSpacesFor(userA)
			Expect(spaces).To(BeEmpty())
		})

		It("different users see space independently", func() {
			addGrant("./finance/budget", userA)
			addGrant("./hr", userB)

			spacesA := listSpacesFor(userA)
			spacesB := listSpacesFor(userB)
			Expect(spacesA).To(HaveLen(1))
			Expect(spacesB).To(HaveLen(1))

			removeGrant("./finance/budget", userA)
			spacesA = listSpacesFor(userA)
			spacesB = listSpacesFor(userB)
			Expect(spacesA).To(BeEmpty())
			Expect(spacesB).To(HaveLen(1))
		})
	})

	Describe("ListFolder filtering for subspace-only users", func() {
		BeforeEach(func() {
			addGrant("./finance/budget", userA)
		})

		It("listing space root shows only ancestor directories of subspace", func() {
			names := listFolderNames(userA, ".")
			Expect(names).To(ContainElement("finance"))
			Expect(names).NotTo(ContainElement("hr"))
			Expect(names).NotTo(ContainElement("legal"))
		})

		It("listing intermediate directory shows only path to subspace", func() {
			names := listFolderNames(userA, "./finance")
			Expect(names).To(ContainElement("budget"))
			Expect(names).NotTo(ContainElement("taxes"))
		})

		It("listing inside subspace shows all children", func() {
			Expect(env.Fs.CreateDir(env.Ctx, &provider.Reference{ResourceId: env.SpaceRootRes, Path: "./finance/budget/q1"})).To(Succeed())
			Expect(env.Fs.CreateDir(env.Ctx, &provider.Reference{ResourceId: env.SpaceRootRes, Path: "./finance/budget/q2"})).To(Succeed())

			names := listFolderNames(userA, "./finance/budget")
			Expect(names).To(ContainElement("q1"))
			Expect(names).To(ContainElement("q2"))
		})

		It("owner sees all directories (no filtering)", func() {
			names := listFolderNames(env.Owner, ".")
			Expect(names).To(ContainElement("finance"))
			Expect(names).To(ContainElement("hr"))
			Expect(names).To(ContainElement("legal"))
		})

		It("multiple subspaces on different branches shows both paths", func() {
			addGrant("./hr", userA)

			names := listFolderNames(userA, ".")
			Expect(names).To(ContainElement("finance"))
			Expect(names).To(ContainElement("hr"))
			Expect(names).NotTo(ContainElement("legal"))
		})
	})
})
