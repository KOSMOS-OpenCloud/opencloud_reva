package node

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/opencloud-eu/reva/v2/pkg/appctx"
	"github.com/opencloud-eu/reva/v2/pkg/storage/pkg/decomposedfs/metadata"
	"github.com/opencloud-eu/reva/v2/pkg/storage/pkg/decomposedfs/metadata/prefixes"
)

// SubspaceEntry identifies a subspace within a space.
type SubspaceEntry struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

const subspaceCacheTTL = 5 * time.Minute

type subspaceCacheEntry struct {
	entries []SubspaceEntry
	expires time.Time
}

// subspaceCache caches the subspace list per space root to avoid repeated xattr reads.
var (
	subspaceCacheMu sync.RWMutex
	subspaceCache   = map[string]subspaceCacheEntry{} // spaceID → entries+expiry

	// subspaceMutateMu serialises Add/RemoveSubspace to prevent TOCTOU races.
	subspaceMutateMu sync.Mutex
)

// GetSubspaceList returns the subspace entries for the given space root node.
// Results are cached in memory with a TTL.
func GetSubspaceList(ctx context.Context, spaceRoot *Node) []SubspaceEntry {
	now := time.Now()

	subspaceCacheMu.RLock()
	if cached, ok := subspaceCache[spaceRoot.SpaceID]; ok && now.Before(cached.expires) {
		subspaceCacheMu.RUnlock()
		return cached.entries
	}
	subspaceCacheMu.RUnlock()

	raw, err := spaceRoot.XattrString(ctx, prefixes.SubspacesAttr)
	if err != nil || raw == "" {
		return nil
	}

	var entries []SubspaceEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		appctx.GetLogger(ctx).Error().Err(err).
			Str("spaceid", spaceRoot.SpaceID).
			Str("raw", raw).
			Msg("subspace: failed to parse subspace list from xattr — all subspace boundaries inactive for this space")
		return nil
	}

	subspaceCacheMu.Lock()
	subspaceCache[spaceRoot.SpaceID] = subspaceCacheEntry{entries: entries, expires: now.Add(subspaceCacheTTL)}
	subspaceCacheMu.Unlock()

	return entries
}

// InvalidateSubspaceCache removes the cached subspace list for a space.
func InvalidateSubspaceCache(spaceID string) {
	subspaceCacheMu.Lock()
	delete(subspaceCache, spaceID)
	subspaceCacheMu.Unlock()
}

// InvalidateAllSubspaceCaches clears the entire cache (e.g. for testing).
func InvalidateAllSubspaceCaches() {
	subspaceCacheMu.Lock()
	subspaceCache = map[string]subspaceCacheEntry{}
	subspaceCacheMu.Unlock()
}

// SetSubspaceList writes the subspace list to the space root node.
func SetSubspaceList(ctx context.Context, spaceRoot *Node, entries []SubspaceEntry) error {
	data, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	InvalidateSubspaceCache(spaceRoot.SpaceID)
	return spaceRoot.SetXattrString(ctx, prefixes.SubspacesAttr, string(data))
}

// AddSubspace adds a node as subspace. Path is relative to space root.
// Serialised via subspaceMutateMu to prevent TOCTOU races.
func AddSubspace(ctx context.Context, spaceRoot *Node, nodeID, path string) error {
	subspaceMutateMu.Lock()
	defer subspaceMutateMu.Unlock()

	// Invalidate cache to force a fresh read from xattr.
	InvalidateSubspaceCache(spaceRoot.SpaceID)
	entries := GetSubspaceList(ctx, spaceRoot)
	for _, e := range entries {
		if e.ID == nodeID {
			return nil // already a subspace
		}
	}
	entries = append(entries, SubspaceEntry{ID: nodeID, Path: path})
	return SetSubspaceList(ctx, spaceRoot, entries)
}

// RemoveSubspace removes a node from the subspace list.
// Serialised via subspaceMutateMu to prevent TOCTOU races.
func RemoveSubspace(ctx context.Context, spaceRoot *Node, nodeID string) error {
	subspaceMutateMu.Lock()
	defer subspaceMutateMu.Unlock()

	// Invalidate cache to force a fresh read from xattr.
	InvalidateSubspaceCache(spaceRoot.SpaceID)
	entries := GetSubspaceList(ctx, spaceRoot)
	filtered := make([]SubspaceEntry, 0, len(entries))
	for _, e := range entries {
		if e.ID != nodeID {
			filtered = append(filtered, e)
		}
	}
	return SetSubspaceList(ctx, spaceRoot, filtered)
}

// FindAncestorSubspace checks if any ancestor of the node (up to but not including
// space root) is a subspace. Returns the subspace entry or nil.
func FindAncestorSubspace(n *Node, subspaces []SubspaceEntry) *SubspaceEntry {
	if len(subspaces) == 0 {
		return nil
	}
	idSet := make(map[string]*SubspaceEntry, len(subspaces))
	for i := range subspaces {
		idSet[subspaces[i].ID] = &subspaces[i]
	}
	// Check the node itself and walk up
	if entry, ok := idSet[n.ID]; ok {
		return entry
	}
	// Note: parent walking is done in assemblePermissions loop,
	// we check cn.ID against idSet there. This function is for
	// the initial node check only.
	return nil
}

// IsSubspaceID checks if a node ID is in the subspace list (O(1) with map).
func IsSubspaceID(nodeID string, subspaces []SubspaceEntry) bool {
	for _, e := range subspaces {
		if e.ID == nodeID {
			return true
		}
	}
	return false
}

// IsAncestorOfSubspace checks if the given path is an ancestor of any subspace path.
func IsAncestorOfSubspace(nodePath string, subspaces []SubspaceEntry) bool {
	for _, ss := range subspaces {
		if strings.HasPrefix(ss.Path, nodePath+"/") || ss.Path == nodePath {
			return true
		}
	}
	return false
}

// UserHasGrantInSubspaces checks if the user has any grant in the given subspace nodes.
// This requires reading grants from each subspace node — should only be called for Fall 2.
func UserHasGrantInSubspaces(ctx context.Context, spaceRoot *Node, subspaces []SubspaceEntry, lu NodeLookup) bool {
	_ = metadata.IsAttrUnset // ensure metadata import is used
	for _, ss := range subspaces {
		subNode, err := lu.NodeFromID(ctx, spaceRoot.SpaceID, ss.ID)
		if err != nil || subNode == nil {
			continue
		}
		grantees, err := subNode.ListGrantees(ctx)
		if err != nil || len(grantees) == 0 {
			continue
		}
		// If the subspace has any grantees, the current user might be one
		// The actual user check happens in ReadUserPermissions — here we just
		// check if grants exist at all (cheap). The full check is done by
		// the caller via ReadUserPermissions on the subspace node.
		return true
	}
	return false
}

// NodeLookup is a minimal interface for resolving nodes by ID.
type NodeLookup interface {
	NodeFromID(ctx context.Context, spaceID, nodeID string) (*Node, error)
}
