package node_test

import (
	"context"

	userpb "github.com/cs3org/go-cs3apis/cs3/identity/user/v1beta1"
	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ctxpkg "github.com/opencloud-eu/reva/v2/pkg/ctx"
	"github.com/opencloud-eu/reva/v2/pkg/storage/pkg/decomposedfs/node"
	helpers "github.com/opencloud-eu/reva/v2/pkg/storage/pkg/decomposedfs/testhelpers"
	"github.com/stretchr/testify/mock"
)

// Nested subspace permission test:
//
//	Space-Root (owner: O0)
//	  └── x3/          ← subspace, editor: O3
//	       └── x5/     ← subspace, editor: O5
//	            └── x6/ ← subspace, editor: O6
//
// The innermost subspace wins: O3 can only access x3, not x5/x6.

var _ = Describe("Subspace nested permissions", func() {
	var (
		env   *helpers.DecomposedTestEnv
		perms *node.Permissions

		userO3 *userpb.User
		userO5 *userpb.User
		userO6 *userpb.User

		x3Node *node.Node
		x5Node *node.Node
		x6Node *node.Node
	)

	editorPerms := &provider.ResourcePermissions{
		Stat: true, GetPath: true, ListContainer: true,
		InitiateFileDownload: true, InitiateFileUpload: true,
	}

	BeforeEach(func() {
		var err error
		env, err = helpers.NewTestEnv(nil)
		Expect(err).ToNot(HaveOccurred())

		// Mock allows owner to do all setup operations (CreateDir, AddGrant)
		env.Permissions.On("AssemblePermissions", mock.Anything, mock.Anything).Return(
			node.OwnerPermissions(), nil,
		)

		userO3 = &userpb.User{Id: &userpb.UserId{Idp: "idp", OpaqueId: "user-o3", Type: userpb.UserType_USER_TYPE_PRIMARY}}
		userO5 = &userpb.User{Id: &userpb.UserId{Idp: "idp", OpaqueId: "user-o5", Type: userpb.UserType_USER_TYPE_PRIMARY}}
		userO6 = &userpb.User{Id: &userpb.UserId{Idp: "idp", OpaqueId: "user-o6", Type: userpb.UserType_USER_TYPE_PRIMARY}}

		// Create nested dirs
		Expect(env.Fs.CreateDir(env.Ctx, &provider.Reference{ResourceId: env.SpaceRootRes, Path: "./x3"})).To(Succeed())
		Expect(env.Fs.CreateDir(env.Ctx, &provider.Reference{ResourceId: env.SpaceRootRes, Path: "./x3/x5"})).To(Succeed())
		Expect(env.Fs.CreateDir(env.Ctx, &provider.Reference{ResourceId: env.SpaceRootRes, Path: "./x3/x5/x6"})).To(Succeed())

		x3Node, err = env.Lookup.NodeFromResource(env.Ctx, &provider.Reference{ResourceId: env.SpaceRootRes, Path: "./x3"})
		Expect(err).ToNot(HaveOccurred())
		x5Node, err = env.Lookup.NodeFromResource(env.Ctx, &provider.Reference{ResourceId: env.SpaceRootRes, Path: "./x3/x5"})
		Expect(err).ToNot(HaveOccurred())
		x6Node, err = env.Lookup.NodeFromResource(env.Ctx, &provider.Reference{ResourceId: env.SpaceRootRes, Path: "./x3/x5/x6"})
		Expect(err).ToNot(HaveOccurred())

		// Mark as subspaces
		spaceRoot, err := node.ReadNode(env.Ctx, env.Lookup, env.SpaceRootRes.SpaceId, env.SpaceRootRes.OpaqueId, "", false, nil, false)
		Expect(err).ToNot(HaveOccurred())
		Expect(node.AddSubspace(env.Ctx, spaceRoot, x3Node.ID, "/x3")).To(Succeed())
		Expect(node.AddSubspace(env.Ctx, spaceRoot, x5Node.ID, "/x3/x5")).To(Succeed())
		Expect(node.AddSubspace(env.Ctx, spaceRoot, x6Node.ID, "/x3/x5/x6")).To(Succeed())

		// Add editor grants
		grant := func(path string, user *userpb.User) {
			Expect(env.Fs.AddGrant(env.Ctx, &provider.Reference{ResourceId: env.SpaceRootRes, Path: path}, &provider.Grant{
				Grantee:     &provider.Grantee{Type: provider.GranteeType_GRANTEE_TYPE_USER, Id: &provider.Grantee_UserId{UserId: user.Id}},
				Permissions: editorPerms,
			})).To(Succeed())
		}
		grant("./x3", userO3)
		grant("./x3/x5", userO5)
		grant("./x3/x5/x6", userO6)

		// Real permissions engine for assertions
		perms = node.NewPermissions(env.Lookup)
	})

	AfterEach(func() {
		node.InvalidateSubspaceCache(env.SpaceRootRes.SpaceId)
		if env != nil {
			env.Cleanup()
		}
	})

	// Re-read node from disk to avoid stale xattr cache
	assembleFor := func(user *userpb.User, n *node.Node) *provider.ResourcePermissions {
		ctx := ctxpkg.ContextSetUser(context.Background(), user)
		fresh, err := node.ReadNode(ctx, env.Lookup, n.SpaceID, n.ID, "", false, nil, false)
		Expect(err).ToNot(HaveOccurred())
		ap, err := perms.AssemblePermissions(ctx, fresh)
		Expect(err).ToNot(HaveOccurred())
		return ap
	}

	Describe("Owner O0", func() {
		It("has full access to x3", func() {
			ap := assembleFor(env.Owner, x3Node)
			Expect(ap.Stat).To(BeTrue())
			Expect(ap.InitiateFileDownload).To(BeTrue())
			Expect(ap.InitiateFileUpload).To(BeTrue())
			Expect(ap.Delete).To(BeTrue())
		})
		It("has full access to x5 (nested subspace)", func() {
			ap := assembleFor(env.Owner, x5Node)
			Expect(ap.Stat).To(BeTrue())
			Expect(ap.InitiateFileDownload).To(BeTrue())
		})
		It("has full access to x6 (deeply nested)", func() {
			ap := assembleFor(env.Owner, x6Node)
			Expect(ap.Stat).To(BeTrue())
			Expect(ap.InitiateFileDownload).To(BeTrue())
		})
	})

	Describe("User O3 (editor on x3 only)", func() {
		It("has editor on x3", func() {
			ap := assembleFor(userO3, x3Node)
			Expect(ap.Stat).To(BeTrue())
			Expect(ap.InitiateFileDownload).To(BeTrue())
			Expect(ap.InitiateFileUpload).To(BeTrue())
		})
		It("has NO editor on x5 (subspace x5 blocks)", func() {
			ap := assembleFor(userO3, x5Node)
			Expect(ap.InitiateFileDownload).To(BeFalse())
			Expect(ap.InitiateFileUpload).To(BeFalse())
		})
		It("has NO access to x6 (blocked by x5 and x6)", func() {
			ap := assembleFor(userO3, x6Node)
			Expect(ap.InitiateFileDownload).To(BeFalse())
		})
	})

	Describe("User O5 (editor on x5 only)", func() {
		It("has editor on x5", func() {
			ap := assembleFor(userO5, x5Node)
			Expect(ap.Stat).To(BeTrue())
			Expect(ap.InitiateFileDownload).To(BeTrue())
			Expect(ap.InitiateFileUpload).To(BeTrue())
		})
		It("has NO editor on x6 (subspace x6 blocks)", func() {
			ap := assembleFor(userO5, x6Node)
			Expect(ap.InitiateFileDownload).To(BeFalse())
		})
		It("has NO access to x3 (not a member)", func() {
			ap := assembleFor(userO5, x3Node)
			Expect(ap.InitiateFileDownload).To(BeFalse())
		})
	})

	Describe("User O6 (editor on x6 only)", func() {
		It("has editor on x6", func() {
			ap := assembleFor(userO6, x6Node)
			Expect(ap.Stat).To(BeTrue())
			Expect(ap.InitiateFileDownload).To(BeTrue())
			Expect(ap.InitiateFileUpload).To(BeTrue())
		})
		It("has NO access to x5", func() {
			ap := assembleFor(userO6, x5Node)
			Expect(ap.InitiateFileDownload).To(BeFalse())
		})
		It("has NO access to x3", func() {
			ap := assembleFor(userO6, x3Node)
			Expect(ap.InitiateFileDownload).To(BeFalse())
		})
	})
})
