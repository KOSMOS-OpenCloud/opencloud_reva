package node_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/opencloud-eu/reva/v2/pkg/storage/pkg/decomposedfs/node"
)

var _ = Describe("Subspace helpers", func() {
	Describe("IsSubspaceID", func() {
		It("returns false for empty list", func() {
			Expect(node.IsSubspaceID("node1", nil)).To(BeFalse())
		})

		It("returns true when node ID is in the list", func() {
			subspaces := []node.SubspaceEntry{
				{ID: "node1", Path: "/dir1"},
				{ID: "node2", Path: "/dir2"},
			}
			Expect(node.IsSubspaceID("node1", subspaces)).To(BeTrue())
			Expect(node.IsSubspaceID("node2", subspaces)).To(BeTrue())
		})

		It("returns false when node ID is not in the list", func() {
			subspaces := []node.SubspaceEntry{
				{ID: "node1", Path: "/dir1"},
			}
			Expect(node.IsSubspaceID("other", subspaces)).To(BeFalse())
		})
	})

	Describe("IsAncestorOfSubspace", func() {
		var subspaces []node.SubspaceEntry

		BeforeEach(func() {
			subspaces = []node.SubspaceEntry{
				{ID: "n1", Path: "/finance/budget"},
				{ID: "n2", Path: "/hr"},
			}
		})

		It("returns true for exact match", func() {
			Expect(node.IsAncestorOfSubspace("/finance/budget", subspaces)).To(BeTrue())
			Expect(node.IsAncestorOfSubspace("/hr", subspaces)).To(BeTrue())
		})

		It("returns true for ancestor path", func() {
			Expect(node.IsAncestorOfSubspace("/finance", subspaces)).To(BeTrue())
		})

		It("returns false for non-ancestor path", func() {
			Expect(node.IsAncestorOfSubspace("/legal", subspaces)).To(BeFalse())
		})

		It("returns false for sibling path", func() {
			Expect(node.IsAncestorOfSubspace("/finance/taxes", subspaces)).To(BeFalse())
		})

		It("returns false for empty list", func() {
			Expect(node.IsAncestorOfSubspace("/anything", nil)).To(BeFalse())
		})
	})

	Describe("FindAncestorSubspace", func() {
		It("returns nil for empty list", func() {
			n := &node.Node{}
			n.ID = "somenode"
			Expect(node.FindAncestorSubspace(n, nil)).To(BeNil())
		})

		It("returns the entry when node itself is a subspace", func() {
			n := &node.Node{}
			n.ID = "sub1"
			subspaces := []node.SubspaceEntry{
				{ID: "sub1", Path: "/dir1"},
			}
			entry := node.FindAncestorSubspace(n, subspaces)
			Expect(entry).ToNot(BeNil())
			Expect(entry.ID).To(Equal("sub1"))
			Expect(entry.Path).To(Equal("/dir1"))
		})

		It("returns nil when node is not a subspace", func() {
			n := &node.Node{}
			n.ID = "other"
			subspaces := []node.SubspaceEntry{
				{ID: "sub1", Path: "/dir1"},
			}
			Expect(node.FindAncestorSubspace(n, subspaces)).To(BeNil())
		})
	})
})
