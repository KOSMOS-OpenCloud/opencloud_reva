package tree

// InternalPath returns the on-disk path for a node.
// Used by zipfs to access ZIP files directly.
func (t *Tree) InternalPath(spaceID, nodeID string) string {
	return t.lookup.InternalPath(spaceID, nodeID)
}
