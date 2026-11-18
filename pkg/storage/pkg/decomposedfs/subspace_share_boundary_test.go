package decomposedfs_test

import (
	"context"

	userpb "github.com/cs3org/go-cs3apis/cs3/identity/user/v1beta1"
	cs3permissions "github.com/cs3org/go-cs3apis/cs3/permissions/v1beta1"
	rpcv1beta1 "github.com/cs3org/go-cs3apis/cs3/rpc/v1beta1"
	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/opencloud-eu/reva/v2/internal/grpc/services/storageprovider"
	ctxpkg "github.com/opencloud-eu/reva/v2/pkg/ctx"
	"github.com/opencloud-eu/reva/v2/pkg/storage/pkg/decomposedfs/node"
	helpers "github.com/opencloud-eu/reva/v2/pkg/storage/pkg/decomposedfs/testhelpers"
	"github.com/stretchr/testify/mock"
	"google.golang.org/grpc"
)

// Boundary between plain shares and subspaces, and the permission walk of
// subspaces inside a project space.
//
// A plain share of a folder is a share: the grants of the space still apply
// below it. A subspace is a folder with its own access rules: the permission
// walk stops at the subspace root, grants from above do not apply.
//
// Unlike subspace_integration_test.go this suite is close to production:
//   - only the admin holds the global "manage space properties" permission
//     (CheckPermission), so the subspace management bypass does not hide
//     missing permissions of the other users
//   - every access check runs through the driver (GetMD) with the real
//     permission engine
//
// Layout (project space, space manager: env.SpaceManager, member: spaceMember):
//
//	Space-Root
//	  ├── open/            plain share: shareRecipient
//	  │    ├── a.txt
//	  │    └── deep/d.txt
//	  ├── secret/          subspace, member: subspaceMember
//	  │    ├── s.txt
//	  │    └── inner/i.txt (inner/ may get a plain share)
//	  ├── mixed/           plain share: shareRecipient (in some specs)
//	  │    ├── m.txt
//	  │    └── vault/v.txt (vault/ is a subspace in some specs)
//	  └── plain/p.txt      no grants
var _ = Describe("Subspace and share boundary", func() {
	var (
		env *helpers.DecomposedTestEnv

		spaceMember    *userpb.User // editor on the space root
		shareRecipient *userpb.User // receives plain shares only
		subspaceMember *userpb.User // member of subspaces only
		outsider       *userpb.User // no grants at all

		editorPermissions *provider.ResourcePermissions
	)

	newUser := func(opaqueID string) *userpb.User {
		return &userpb.User{
			Id:     &userpb.UserId{Idp: "idp", OpaqueId: opaqueID, Type: userpb.UserType_USER_TYPE_PRIMARY},
			Groups: []string{},
		}
	}

	// useOwnerPermissions lets the setup calls of the admin pass.
	useOwnerPermissions := func() {
		env.Permissions.ExpectedCalls = nil
		env.Permissions.On("AssemblePermissions", mock.Anything, mock.Anything).Return(
			node.OwnerPermissions(), nil,
		)
	}

	// useRealPermissions switches the driver to the real permission engine.
	useRealPermissions := func() {
		env.Permissions.ExpectedCalls = nil
		realPermissions := node.NewPermissions(env.Lookup)
		env.Permissions.On("AssemblePermissions", mock.Anything, mock.Anything).Return(
			func(ctx context.Context, n *node.Node) (*provider.ResourcePermissions, error) {
				return realPermissions.AssemblePermissions(ctx, n)
			}, nil,
		)
	}

	reference := func(path string) *provider.Reference {
		return &provider.Reference{ResourceId: env.SpaceRootRes, Path: path}
	}

	userGrant := func(user *userpb.User) *provider.Grant {
		return &provider.Grant{
			Grantee:     &provider.Grantee{Type: provider.GranteeType_GRANTEE_TYPE_USER, Id: &provider.Grantee_UserId{UserId: user.Id}},
			Permissions: editorPermissions,
		}
	}

	// sharePlain shares a folder the normal way (share panel): no subspace marker.
	sharePlain := func(path string, user *userpb.User) {
		Expect(env.Fs.AddGrant(env.Ctx, reference(path), userGrant(user))).To(Succeed())
	}

	// addSubspaceMember adds a member the subspace way (subspace panel): the
	// grant carries the subspace marker and registers the folder as subspace.
	addSubspaceMember := func(path string, user *userpb.User) {
		Expect(env.Fs.AddGrant(storageprovider.WithSubspaceGrant(env.Ctx), reference(path), userGrant(user))).To(Succeed())
	}

	removeGrant := func(path string, user *userpb.User) {
		Expect(env.Fs.RemoveGrant(env.Ctx, reference(path), userGrant(user))).To(Succeed())
	}

	createFile := func(folderPath, name string) {
		folder, err := env.Fs.GetMD(env.Ctx, reference(folderPath), nil, nil)
		Expect(err).ToNot(HaveOccurred())
		_, err = env.CreateTestFile(name, name+"-blobid", folder.Id.OpaqueId, env.SpaceRootRes.SpaceId, 10)
		Expect(err).ToNot(HaveOccurred())
	}

	subspacePaths := func() []string {
		node.InvalidateSubspaceCache(env.SpaceRootRes.SpaceId)
		spaceRoot, err := env.Lookup.NodeFromResource(env.Ctx, &provider.Reference{ResourceId: env.SpaceRootRes})
		Expect(err).ToNot(HaveOccurred())
		paths := []string{}
		for _, entry := range node.GetSubspaceList(env.Ctx, spaceRoot) {
			paths = append(paths, entry.Path)
		}
		return paths
	}

	// canRead reports whether the user can read the file or folder through the
	// driver with the real permission engine: stat succeeds and the content
	// may be downloaded (files) or listed (folders).
	canRead := func(user *userpb.User, path string) bool {
		node.InvalidateSubspaceCache(env.SpaceRootRes.SpaceId)
		useRealPermissions()
		defer useOwnerPermissions()

		userContext := ctxpkg.ContextSetUser(context.Background(), user)
		info, err := env.Fs.GetMD(userContext, reference(path), nil, nil)
		if err != nil {
			return false
		}
		if info.GetType() == provider.ResourceType_RESOURCE_TYPE_CONTAINER {
			return info.GetPermissionSet().GetListContainer() && info.GetPermissionSet().GetInitiateFileDownload()
		}
		return info.GetPermissionSet().GetInitiateFileDownload()
	}

	// canSee reports whether the user can stat the path at all (navigation).
	canSee := func(user *userpb.User, path string) bool {
		node.InvalidateSubspaceCache(env.SpaceRootRes.SpaceId)
		useRealPermissions()
		defer useOwnerPermissions()

		userContext := ctxpkg.ContextSetUser(context.Background(), user)
		_, err := env.Fs.GetMD(userContext, reference(path), nil, nil)
		return err == nil
	}

	BeforeEach(func() {
		var err error
		env, err = helpers.NewTestEnv(nil)
		Expect(err).ToNot(HaveOccurred())

		spaceMember = newUser("space-member")
		shareRecipient = newUser("share-recipient")
		subspaceMember = newUser("subspace-member")
		outsider = newUser("outsider")

		editorPermissions = &provider.ResourcePermissions{
			Stat: true, GetPath: true, ListContainer: true,
			InitiateFileDownload: true, InitiateFileUpload: true,
		}

		// Create the project space with the helper's own mocks, then replace them.
		projectSpace, err := env.CreateTestStorageSpace("project", nil)
		Expect(err).ToNot(HaveOccurred())
		env.SpaceRootRes = projectSpace

		// Only the admin (env.Owner) holds the global permission that allows
		// subspace management. Everybody else has to pass the grant walk.
		env.PermissionsClient.ExpectedCalls = nil
		env.PermissionsClient.On("CheckPermission", mock.Anything, mock.Anything, mock.Anything).Return(
			func(_ context.Context, request *cs3permissions.CheckPermissionRequest, _ ...grpc.CallOption) (*cs3permissions.CheckPermissionResponse, error) {
				statusCode := rpcv1beta1.Code_CODE_PERMISSION_DENIED
				if request.GetSubjectRef().GetUserId().GetOpaqueId() == env.Owner.GetId().GetOpaqueId() {
					statusCode = rpcv1beta1.Code_CODE_OK
				}
				return &cs3permissions.CheckPermissionResponse{Status: &rpcv1beta1.Status{Code: statusCode}}, nil
			},
		)
		useOwnerPermissions()

		for _, folderPath := range []string{"./open", "./open/deep", "./secret", "./secret/inner", "./mixed", "./mixed/vault", "./plain"} {
			Expect(env.Fs.CreateDir(env.Ctx, reference(folderPath))).To(Succeed())
		}
		createFile("./open", "a.txt")
		createFile("./open/deep", "d.txt")
		createFile("./secret", "s.txt")
		createFile("./secret/inner", "i.txt")
		createFile("./mixed", "m.txt")
		createFile("./mixed/vault", "v.txt")
		createFile("./plain", "p.txt")

		// space member: editor grant on the space root
		spaceGrantContext := storageprovider.WithSpaceType(env.Ctx, "project")
		Expect(env.Fs.AddGrant(spaceGrantContext, &provider.Reference{ResourceId: env.SpaceRootRes}, userGrant(spaceMember))).To(Succeed())
	})

	AfterEach(func() {
		node.InvalidateSubspaceCache(env.SpaceRootRes.SpaceId)
		if env != nil {
			env.Cleanup()
		}
	})

	Describe("a plain share", func() {
		BeforeEach(func() {
			sharePlain("./open", shareRecipient)
		})

		It("does not register a subspace", func() {
			Expect(subspacePaths()).To(BeEmpty())
		})

		It("gives the recipient the shared folder and everything below", func() {
			Expect(canRead(shareRecipient, "./open")).To(BeTrue())
			Expect(canRead(shareRecipient, "./open/a.txt")).To(BeTrue())
			Expect(canRead(shareRecipient, "./open/deep/d.txt")).To(BeTrue())
		})

		It("gives the recipient nothing outside the shared folder", func() {
			Expect(canRead(shareRecipient, "./plain/p.txt")).To(BeFalse())
			Expect(canRead(shareRecipient, "./secret/s.txt")).To(BeFalse())
			Expect(canSee(shareRecipient, "./plain")).To(BeFalse())
			Expect(canSee(shareRecipient, ".")).To(BeFalse())
		})

		It("keeps the access of the space members to the shared folder", func() {
			Expect(canRead(spaceMember, "./open")).To(BeTrue())
			Expect(canRead(spaceMember, "./open/a.txt")).To(BeTrue())
			Expect(canRead(spaceMember, "./open/deep/d.txt")).To(BeTrue())
			Expect(canRead(env.SpaceManager, "./open/a.txt")).To(BeTrue())
		})

		It("gives an outsider nothing", func() {
			Expect(canSee(outsider, "./open")).To(BeFalse())
			Expect(canRead(outsider, "./open/a.txt")).To(BeFalse())
		})

		It("stays a share when a second user is shared in the same way", func() {
			sharePlain("./open", subspaceMember)
			Expect(subspacePaths()).To(BeEmpty())
			Expect(canRead(spaceMember, "./open/a.txt")).To(BeTrue())
		})

		It("leaves no trace when the share is removed", func() {
			removeGrant("./open", shareRecipient)
			Expect(subspacePaths()).To(BeEmpty())
			Expect(canRead(shareRecipient, "./open/a.txt")).To(BeFalse())
			Expect(canRead(spaceMember, "./open/a.txt")).To(BeTrue())
		})
	})

	Describe("a subspace", func() {
		BeforeEach(func() {
			addSubspaceMember("./secret", subspaceMember)
		})

		It("is registered with its path", func() {
			Expect(subspacePaths()).To(ConsistOf("/secret"))
		})

		It("gives its member the subspace and everything below", func() {
			Expect(canRead(subspaceMember, "./secret")).To(BeTrue())
			Expect(canRead(subspaceMember, "./secret/s.txt")).To(BeTrue())
			Expect(canRead(subspaceMember, "./secret/inner/i.txt")).To(BeTrue())
		})

		It("stops the permission walk: space members lose access below the subspace root", func() {
			Expect(canRead(spaceMember, "./secret/s.txt")).To(BeFalse())
			Expect(canRead(spaceMember, "./secret/inner/i.txt")).To(BeFalse())
			Expect(canRead(spaceMember, "./secret")).To(BeFalse())
		})

		It("keeps the access of space members to everything outside the subspace", func() {
			Expect(canRead(spaceMember, "./plain/p.txt")).To(BeTrue())
			Expect(canRead(spaceMember, "./open/a.txt")).To(BeTrue())
		})

		It("keeps the access of the space manager across the subspace boundary", func() {
			Expect(canRead(env.SpaceManager, "./secret/s.txt")).To(BeTrue())
			Expect(canRead(env.SpaceManager, "./secret/inner/i.txt")).To(BeTrue())
		})

		It("gives its member navigation to the subspace but no content outside", func() {
			Expect(canSee(subspaceMember, ".")).To(BeTrue())
			Expect(canRead(subspaceMember, ".")).To(BeFalse())
			Expect(canSee(subspaceMember, "./plain")).To(BeFalse())
			Expect(canRead(subspaceMember, "./plain/p.txt")).To(BeFalse())
			Expect(canRead(subspaceMember, "./open/a.txt")).To(BeFalse())
		})

		It("gives an outsider nothing", func() {
			Expect(canSee(outsider, "./secret")).To(BeFalse())
			Expect(canRead(outsider, "./secret/s.txt")).To(BeFalse())
		})

		It("is removed with its last member, the folder falls back to the space rules", func() {
			removeGrant("./secret", subspaceMember)
			Expect(subspacePaths()).To(BeEmpty())
			Expect(canRead(spaceMember, "./secret/s.txt")).To(BeTrue())
			Expect(canRead(subspaceMember, "./secret/s.txt")).To(BeFalse())
		})
	})

	Describe("a plain share next to a subspace", func() {
		It("on another folder does not touch the subspace", func() {
			addSubspaceMember("./secret", subspaceMember)
			sharePlain("./open", shareRecipient)

			Expect(subspacePaths()).To(ConsistOf("/secret"))
			Expect(canRead(shareRecipient, "./open/a.txt")).To(BeTrue())
			Expect(canRead(shareRecipient, "./secret/s.txt")).To(BeFalse())
			Expect(canRead(subspaceMember, "./open/a.txt")).To(BeFalse())
			Expect(canRead(spaceMember, "./open/a.txt")).To(BeTrue())
			Expect(canRead(spaceMember, "./secret/s.txt")).To(BeFalse())
		})

		It("inside a subspace gives the recipient only the shared subfolder", func() {
			addSubspaceMember("./secret", subspaceMember)
			sharePlain("./secret/inner", shareRecipient)

			Expect(subspacePaths()).To(ConsistOf("/secret"))
			Expect(canRead(shareRecipient, "./secret/inner/i.txt")).To(BeTrue())
			Expect(canRead(shareRecipient, "./secret/s.txt")).To(BeFalse())
			// the subspace boundary still holds for the space members
			Expect(canRead(spaceMember, "./secret/inner/i.txt")).To(BeFalse())
			Expect(canRead(subspaceMember, "./secret/inner/i.txt")).To(BeTrue())
		})

		It("on a parent folder does not reach into a subspace below", func() {
			sharePlain("./mixed", shareRecipient)
			addSubspaceMember("./mixed/vault", subspaceMember)

			Expect(subspacePaths()).To(ConsistOf("/mixed/vault"))
			Expect(canRead(shareRecipient, "./mixed/m.txt")).To(BeTrue())
			Expect(canRead(shareRecipient, "./mixed/vault/v.txt")).To(BeFalse())
			Expect(canRead(shareRecipient, "./mixed/vault")).To(BeFalse())
			Expect(canRead(subspaceMember, "./mixed/vault/v.txt")).To(BeTrue())
			Expect(canRead(subspaceMember, "./mixed/m.txt")).To(BeFalse())
		})

		It("on a parent folder added after the subspace does not reach into it either", func() {
			addSubspaceMember("./mixed/vault", subspaceMember)
			sharePlain("./mixed", shareRecipient)

			Expect(subspacePaths()).To(ConsistOf("/mixed/vault"))
			Expect(canRead(shareRecipient, "./mixed/m.txt")).To(BeTrue())
			Expect(canRead(shareRecipient, "./mixed/vault/v.txt")).To(BeFalse())
		})

		It("removing a share on a parent folder keeps the subspace below", func() {
			sharePlain("./mixed", shareRecipient)
			addSubspaceMember("./mixed/vault", subspaceMember)
			removeGrant("./mixed", shareRecipient)

			Expect(subspacePaths()).To(ConsistOf("/mixed/vault"))
			Expect(canRead(subspaceMember, "./mixed/vault/v.txt")).To(BeTrue())
			Expect(canRead(spaceMember, "./mixed/vault/v.txt")).To(BeFalse())
		})
	})

	// Grants are stored on the folder. A folder has one set of grants, so
	// shares and subspace members of the same folder cannot be told apart.
	// These specs pin that behaviour down.
	Describe("shares and members on the same folder", func() {
		It("turning a shared folder into a subspace keeps the earlier recipients as members", func() {
			sharePlain("./open", shareRecipient)
			addSubspaceMember("./open", subspaceMember)

			Expect(subspacePaths()).To(ConsistOf("/open"))
			Expect(canRead(shareRecipient, "./open/a.txt")).To(BeTrue())
			Expect(canRead(subspaceMember, "./open/a.txt")).To(BeTrue())
			Expect(canRead(spaceMember, "./open/a.txt")).To(BeFalse())
		})

		It("a plain share on a subspace root grants access like a membership", func() {
			addSubspaceMember("./secret", subspaceMember)
			sharePlain("./secret", shareRecipient)

			Expect(subspacePaths()).To(ConsistOf("/secret"))
			Expect(canRead(shareRecipient, "./secret/s.txt")).To(BeTrue())
			Expect(canRead(spaceMember, "./secret/s.txt")).To(BeFalse())
		})

		It("the subspace stays while any grant is left on its root", func() {
			addSubspaceMember("./secret", subspaceMember)
			sharePlain("./secret", shareRecipient)
			removeGrant("./secret", subspaceMember)

			Expect(subspacePaths()).To(ConsistOf("/secret"))
			Expect(canRead(spaceMember, "./secret/s.txt")).To(BeFalse())
			Expect(canRead(shareRecipient, "./secret/s.txt")).To(BeTrue())
		})
	})

	Describe("nested subspaces", func() {
		BeforeEach(func() {
			addSubspaceMember("./mixed", shareRecipient)
			addSubspaceMember("./mixed/vault", subspaceMember)
		})

		It("registers both", func() {
			Expect(subspacePaths()).To(ConsistOf("/mixed", "/mixed/vault"))
		})

		It("the member of the outer subspace does not reach into the inner one", func() {
			Expect(canRead(shareRecipient, "./mixed/m.txt")).To(BeTrue())
			Expect(canRead(shareRecipient, "./mixed/vault/v.txt")).To(BeFalse())
		})

		It("the member of the inner subspace gets only the inner one", func() {
			Expect(canRead(subspaceMember, "./mixed/vault/v.txt")).To(BeTrue())
			Expect(canRead(subspaceMember, "./mixed/m.txt")).To(BeFalse())
			Expect(canSee(subspaceMember, "./mixed")).To(BeTrue())
		})

		It("space members get neither", func() {
			Expect(canRead(spaceMember, "./mixed/m.txt")).To(BeFalse())
			Expect(canRead(spaceMember, "./mixed/vault/v.txt")).To(BeFalse())
		})
	})

	// The space list is what the web UI shows as "Spaces". A share must never
	// make the project space appear there, a subspace membership must.
	Describe("the space list of a user", func() {
		spaceCount := func(user *userpb.User) int {
			node.InvalidateSubspaceCache(env.SpaceRootRes.SpaceId)
			useRealPermissions()
			defer useOwnerPermissions()

			userContext := ctxpkg.ContextSetUser(context.Background(), user)
			spaces, err := env.Fs.ListStorageSpaces(userContext, []*provider.ListStorageSpacesRequest_Filter{
				{Type: provider.ListStorageSpacesRequest_Filter_TYPE_USER, Term: &provider.ListStorageSpacesRequest_Filter_User{User: user.Id}},
			}, false)
			Expect(err).ToNot(HaveOccurred())
			projectSpaces := 0
			for _, space := range spaces {
				if space.GetSpaceType() == "project" {
					projectSpaces++
				}
			}
			return projectSpaces
		}

		It("contains the project space for a space member", func() {
			Expect(spaceCount(spaceMember)).To(Equal(1))
		})

		It("does not contain the project space for the recipient of a plain share", func() {
			sharePlain("./open", shareRecipient)
			Expect(spaceCount(shareRecipient)).To(BeZero())
		})

		It("does not contain the project space for the recipient of a plain share inside a subspace", func() {
			addSubspaceMember("./secret", subspaceMember)
			sharePlain("./secret/inner", shareRecipient)
			Expect(spaceCount(shareRecipient)).To(BeZero())
		})

		It("contains the project space for a subspace member, until the last membership is removed", func() {
			addSubspaceMember("./secret", subspaceMember)
			Expect(spaceCount(subspaceMember)).To(Equal(1))

			removeGrant("./secret", subspaceMember)
			Expect(spaceCount(subspaceMember)).To(BeZero())
		})

		It("does not contain the project space for an outsider", func() {
			addSubspaceMember("./secret", subspaceMember)
			Expect(spaceCount(outsider)).To(BeZero())
		})
	})

	Describe("who may create a subspace", func() {
		It("a subspace grant by a user without the manage-space-properties permission stays a plain grant", func() {
			managerContext := storageprovider.WithSubspaceGrant(ctxpkg.ContextSetUser(context.Background(), env.SpaceManager))
			useRealPermissions()
			err := env.Fs.AddGrant(managerContext, reference("./plain"), userGrant(subspaceMember))
			useOwnerPermissions()
			Expect(err).ToNot(HaveOccurred())

			Expect(subspacePaths()).To(BeEmpty())
			Expect(canRead(spaceMember, "./plain/p.txt")).To(BeTrue())
		})
	})
})
