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
	"github.com/opencloud-eu/reva/v2/pkg/storage/pkg/decomposedfs"
	"github.com/opencloud-eu/reva/v2/pkg/storage/pkg/decomposedfs/node"
	helpers "github.com/opencloud-eu/reva/v2/pkg/storage/pkg/decomposedfs/testhelpers"
	"github.com/opencloud-eu/reva/v2/pkg/utils"
	"github.com/stretchr/testify/mock"
	"google.golang.org/grpc"
)

// Scope of the subspace management bypass.
//
// The bypass lets a user with the global "manage space properties" permission
// (Drives.ReadWrite) stat folders and add or remove grants without a grant on
// the folder. It exists to manage subspaces in project spaces.
//
// These specs fix how far it reaches: a space admin without any grant, acting
// in the personal space of another user (no bypass at all) and in a project
// space (folders only, never files or listings).
var _ = Describe("Subspace management bypass scope", func() {
	var (
		env        *helpers.DecomposedTestEnv
		spaceAdmin *userpb.User // holds Drives.ReadWrite, has no grant anywhere
		adminCtx   context.Context

		// number of "manage space properties" checks sent to the permissions service
		managementChecks int
	)

	const manageSpaceProperties = "Drives.ReadWrite"

	useRealPermissions := func() {
		env.Permissions.ExpectedCalls = nil
		realPermissions := node.NewPermissions(env.Lookup)
		env.Permissions.On("AssemblePermissions", mock.Anything, mock.Anything).Return(
			func(ctx context.Context, n *node.Node) (*provider.ResourcePermissions, error) {
				return realPermissions.AssemblePermissions(ctx, n)
			}, nil,
		)
	}

	selfGrant := func() *provider.Grant {
		return &provider.Grant{
			Grantee: &provider.Grantee{Type: provider.GranteeType_GRANTEE_TYPE_USER, Id: &provider.Grantee_UserId{UserId: spaceAdmin.Id}},
			Permissions: &provider.ResourcePermissions{
				Stat: true, GetPath: true, ListContainer: true, InitiateFileDownload: true,
			},
		}
	}

	BeforeEach(func() {
		var err error
		env, err = helpers.NewTestEnv(nil)
		Expect(err).ToNot(HaveOccurred())

		spaceAdmin = &userpb.User{
			Id:     &userpb.UserId{Idp: "idp", OpaqueId: "space-admin", Type: userpb.UserType_USER_TYPE_PRIMARY},
			Groups: []string{},
		}
		adminCtx = ctxpkg.ContextSetUser(context.Background(), spaceAdmin)

		managementChecks = 0
		env.PermissionsClient.ExpectedCalls = nil
		env.PermissionsClient.On("CheckPermission", mock.Anything, mock.Anything, mock.Anything).Return(
			func(_ context.Context, request *cs3permissions.CheckPermissionRequest, _ ...grpc.CallOption) (*cs3permissions.CheckPermissionResponse, error) {
				actingUser := request.GetSubjectRef().GetUserId().GetOpaqueId()
				statusCode := rpcv1beta1.Code_CODE_PERMISSION_DENIED
				switch {
				case request.GetPermission() == manageSpaceProperties:
					// only the space admin holds the global management permission
					managementChecks++
					if actingUser == spaceAdmin.GetId().GetOpaqueId() {
						statusCode = rpcv1beta1.Code_CODE_OK
					}
				case actingUser == env.Owner.GetId().GetOpaqueId():
					// the owner may create the test spaces
					statusCode = rpcv1beta1.Code_CODE_OK
				}
				return &cs3permissions.CheckPermissionResponse{Status: &rpcv1beta1.Status{Code: statusCode}}, nil
			},
		)
	})

	AfterEach(func() {
		if env != nil {
			env.Cleanup()
		}
	})

	Context("in the personal space of another user", func() {
		// NewTestEnv creates the personal space of env.Owner with /dir1/file1
		folder := func() *provider.Reference {
			return &provider.Reference{ResourceId: env.SpaceRootRes, Path: "./dir1"}
		}
		file := func() *provider.Reference {
			return &provider.Reference{ResourceId: env.SpaceRootRes, Path: "./dir1/file1"}
		}

		BeforeEach(func() {
			useRealPermissions()
		})

		It("does not let the space admin stat a folder", func() {
			_, err := env.Fs.GetMD(adminCtx, folder(), nil, nil)
			Expect(err).To(MatchError(ContainSubstring("not found")))
		})

		It("does not let the space admin stat a file", func() {
			_, err := env.Fs.GetMD(adminCtx, file(), nil, nil)
			Expect(err).To(MatchError(ContainSubstring("not found")))
		})

		It("does not let the space admin list a folder", func() {
			_, err := env.Fs.ListFolder(adminCtx, folder(), nil, nil)
			Expect(err).To(HaveOccurred())
		})

		It("does not let the space admin add a grant for himself", func() {
			Expect(env.Fs.AddGrant(adminCtx, folder(), selfGrant())).ToNot(Succeed())
			_, err := env.Fs.GetMD(adminCtx, file(), nil, nil)
			Expect(err).To(MatchError(ContainSubstring("not found")))
		})

		It("does not ask the permissions service at all", func() {
			_, _ = env.Fs.GetMD(adminCtx, folder(), nil, nil)
			_, _ = env.Fs.GetMD(env.Ctx, folder(), nil, nil)
			Expect(managementChecks).To(BeZero())
		})
	})

	Context("in a project space the admin is not a member of", func() {
		var projectSpace *provider.ResourceId

		folder := func() *provider.Reference {
			return &provider.Reference{ResourceId: projectSpace, Path: "./team"}
		}
		file := func() *provider.Reference {
			return &provider.Reference{ResourceId: projectSpace, Path: "./team/plan.txt"}
		}

		BeforeEach(func() {
			var err error
			projectSpace, err = env.CreateTestStorageSpace("project", nil)
			Expect(err).ToNot(HaveOccurred())
			managementChecks = 0

			env.Permissions.ExpectedCalls = nil
			env.Permissions.On("AssemblePermissions", mock.Anything, mock.Anything).Return(node.OwnerPermissions(), nil)
			Expect(env.Fs.CreateDir(env.Ctx, folder())).To(Succeed())
			teamFolder, err := env.Fs.GetMD(env.Ctx, folder(), nil, nil)
			Expect(err).ToNot(HaveOccurred())
			_, err = env.CreateTestFile("plan.txt", "plan-blobid", teamFolder.Id.OpaqueId, projectSpace.SpaceId, 10)
			Expect(err).ToNot(HaveOccurred())

			useRealPermissions()
		})

		AfterEach(func() {
			node.InvalidateSubspaceCache(projectSpace.SpaceId)
		})

		It("lets the space admin stat a folder, marked as bypass only", func() {
			info, err := env.Fs.GetMD(adminCtx, folder(), nil, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(utils.ReadPlainFromOpaque(info.GetOpaque(), decomposedfs.SubspaceManagementBypassOpaqueKey)).To(Equal("true"))
		})

		It("does not let the space admin stat a file", func() {
			_, err := env.Fs.GetMD(adminCtx, file(), nil, nil)
			Expect(err).To(MatchError(ContainSubstring("not found")))
		})

		It("does not let the space admin list a folder", func() {
			_, err := env.Fs.ListFolder(adminCtx, folder(), nil, nil)
			Expect(err).To(HaveOccurred())
		})

		// Pinned behaviour, not a wish: the bypass covers AddGrant, so a space
		// admin can add any grant in any project space, also for himself. This
		// matches the role (it may manage all project spaces), but it means the
		// role can reach the content of every project space.
		It("lets the space admin add a grant, also for himself", func() {
			Expect(env.Fs.AddGrant(adminCtx, folder(), selfGrant())).To(Succeed())
			info, err := env.Fs.GetMD(adminCtx, file(), nil, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(info.GetPermissionSet().GetInitiateFileDownload()).To(BeTrue())
		})
	})
})
