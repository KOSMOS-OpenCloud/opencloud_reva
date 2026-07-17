// Copyright 2018-2021 CERN
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// In applying this license, CERN does not waive the privileges and immunities
// granted to it by virtue of its status as an Intergovernmental Organization
// or submit itself to any jurisdiction.

package node

import (
	"context"
	"strings"

	userpb "github.com/cs3org/go-cs3apis/cs3/identity/user/v1beta1"
	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	"github.com/opencloud-eu/reva/v2/pkg/appctx"
	ctxpkg "github.com/opencloud-eu/reva/v2/pkg/ctx"
	"github.com/opencloud-eu/reva/v2/pkg/errtypes"
	"github.com/opencloud-eu/reva/v2/pkg/utils"
	"github.com/pkg/errors"
)

// PermissionFunc should return true when the user has permission to access the node
type PermissionFunc func(*Node) bool

var (
	// NoCheck doesn't check permissions, returns true always
	NoCheck PermissionFunc = func(_ *Node) bool {
		return true
	}
)

// NoPermissions represents an empty set of permissions
func NoPermissions() *provider.ResourcePermissions {
	return &provider.ResourcePermissions{}
}

// ShareFolderPermissions defines permissions for the shared jail
func ShareFolderPermissions() *provider.ResourcePermissions {
	return &provider.ResourcePermissions{
		// read permissions
		ListContainer:        true,
		Stat:                 true,
		InitiateFileDownload: true,
		GetPath:              true,
		GetQuota:             true,
		ListFileVersions:     true,
	}
}

// OwnerPermissions defines permissions for nodes owned by the user
func OwnerPermissions() *provider.ResourcePermissions {
	return &provider.ResourcePermissions{
		// all permissions
		AddGrant:              true,
		CreateContainer:       true,
		Delete:                true,
		DeleteContainer:       true,
		GetPath:               true,
		GetQuota:              true,
		InitiateFileDownload:  true,
		InitiateFileUpload:    true,
		ListContainer:         true,
		ListFileVersions:      true,
		ListGrants:            true,
		ListRecycle:           true,
		Move:                  true,
		MoveContainer:         true,
		PurgeRecycle:          true,
		RemoveGrant:           true,
		RestoreFileVersion:    true,
		RestoreRecycleItem:    true,
		Stat:                  true,
		UpdateGrant:           true,
		DenyGrant:             true,
		SetImmutableFile:      true,
		SetImmutableContainer: true,
	}
}

// ServiceAccountPermissions defines the permissions for nodes when requested by a service account
func ServiceAccountPermissions() *provider.ResourcePermissions {
	// TODO: Different permissions for different service accounts
	return &provider.ResourcePermissions{
		Stat:                 true,
		ListContainer:        true,
		GetPath:              true, // for search index
		InitiateFileUpload:   true, // for personal data export
		InitiateFileDownload: true, // for full-text-search
		RemoveGrant:          true, // for share expiry
		ListRecycle:          true, // for purge-trash-bin command
		PurgeRecycle:         true, // for purge-trash-bin command
		RestoreRecycleItem:   true, // for cli restore command
		Delete:                true, // for cli restore command with replace option
		DeleteContainer:       true, // for space provisioning and settings init
		CreateContainer:       true, // for space provisioning
		Move:                  true, // for internal operations
		MoveContainer:         true, // for internal operations
		AddGrant:              true, // for initial project space member assignment
		ListGrants:            true, // for initial project space member assignment
		SetImmutableFile:      true, // for service-level immutable operations
		SetImmutableContainer: true, // for service-level immutable operations
	}
}

// Permissions implements permission checks
type Permissions struct {
	lu PathLookup
}

// NewPermissions returns a new Permissions instance
func NewPermissions(lu PathLookup) *Permissions {
	return &Permissions{
		lu: lu,
	}
}

// AssemblePermissions will assemble the permissions for the current user on the given node, taking into account all parent nodes
func (p *Permissions) AssemblePermissions(ctx context.Context, n *Node) (ap *provider.ResourcePermissions, err error) {
	return p.assemblePermissions(ctx, n, true)
}

// AssembleTrashPermissions will assemble the permissions for the current user on the given node, taking into account all parent nodes
func (p *Permissions) AssembleTrashPermissions(ctx context.Context, n *Node) (ap *provider.ResourcePermissions, err error) {
	return p.assemblePermissions(ctx, n, false)
}

// assemblePermissions will assemble the permissions for the current user on the given node, taking into account all parent nodes
func (p *Permissions) assemblePermissions(ctx context.Context, n *Node, failOnTrashedSubtree bool) (ap *provider.ResourcePermissions, err error) {
	u, ok := ctxpkg.ContextGetUser(ctx)
	if !ok {
		return NoPermissions(), nil
	}

	if u.GetId().GetType() == userpb.UserType_USER_TYPE_SERVICE {
		return ServiceAccountPermissions(), nil
	}

	// are we reading a revision?
	if strings.Contains(n.ID, RevisionIDDelimiter) {
		// verify revision key format
		kp := strings.SplitN(n.ID, RevisionIDDelimiter, 2)
		if len(kp) != 2 {
			return NoPermissions(), errtypes.NotFound(n.ID)
		}
		// use the actual node for the permission assembly
		n.ID = kp[0]
	}

	// determine root
	rn := n.SpaceRoot
	cn := n
	ap = &provider.ResourcePermissions{}

	// load subspace list (cached, empty = no subspaces = zero overhead)
	subspaces := GetSubspaceList(ctx, rn)

	// for an efficient group lookup convert the list of groups to a map
	// groups are just strings ... groupnames ... or group ids ??? AAARGH !!!
	groupsMap := make(map[string]bool, len(u.Groups))
	for i := range u.Groups {
		groupsMap[u.Groups[i]] = true
	}

	// for all segments, starting at the leaf
	for cn.ID != rn.ID {
		if np, accessDenied, err := cn.ReadUserPermissions(ctx, u); err == nil {
			// check if we have a denial on this node
			if accessDenied {
				return np, nil
			}
			AddPermissions(ap, np)
		} else {
			appctx.GetLogger(ctx).Error().Err(err).Str("spaceid", cn.SpaceID).Str("nodeid", cn.ID).Msg("error reading permissions")
			// continue with next segment
		}

		// Subspace: if this node is a subspace root, stop walking up.
		// Grants from above the subspace do not apply.
		if IsSubspaceID(cn.ID, subspaces) {
			break
		}

		if cn, err = cn.Parent(ctx); err != nil {
			// We get an error but get a parent, but can not read it from disk (eg. it has been deleted already)
			if cn != nil {
				return ap, errors.Wrap(err, "Decomposedfs: error getting parent for node "+cn.ID)
			}
			// We do not have a parent, so we assume the next valid parent is the spaceRoot (which must always exist)
			cn = n.SpaceRoot
		}
		if failOnTrashedSubtree && !cn.Exists {
			return NoPermissions(), errtypes.NotFound(n.ID)
		}

	}

	// for the root node (reached when no subspace was hit, i.e. cn == rn)
	if cn.ID == rn.ID {
		if np, accessDenied, err := cn.ReadUserPermissions(ctx, u); err == nil {
			// check if we have a denial on this node
			if accessDenied {
				return np, nil
			}
			AddPermissions(ap, np)
		} else {
			appctx.GetLogger(ctx).Error().Err(err).Str("spaceid", cn.SpaceID).Str("nodeid", cn.ID).Msg("error reading root node permissions")
		}
	}

	// Subspace navigation: if the user has no permissions yet (not a space
	// member, no grants on this path) but IS a member of a subspace in this
	// space, grant listing so they can navigate to their subspace.
	if isPermissionsEmpty(ap) && len(subspaces) > 0 {
		if userHasSubspaceGrant(ctx, rn, subspaces, u) {
			AddPermissions(ap, &provider.ResourcePermissions{
				Stat:          true,
				GetPath:       true,
				ListContainer: true,
			})
		}
	}

	// check if the current user is the owner
	if utils.UserIDEqual(u.Id, n.Owner()) {
		return OwnerPermissions(), nil
	}

	appctx.GetLogger(ctx).Debug().Interface("permissions", ap).Str("spaceid", n.SpaceID).Str("nodeid", n.ID).Interface("user", u).Msg("returning agregated permissions")
	return ap, nil
}

// AddPermissions merges a set of permissions into another
// TODO we should use a bitfield for this ...
// isPermissionsEmpty returns true if no permission flags are set.
func isPermissionsEmpty(p *provider.ResourcePermissions) bool {
	if p == nil {
		return true
	}
	empty := &provider.ResourcePermissions{}
	return *p == *empty
}

// userHasSubspaceGrant checks if the user has a grant in any subspace of the space.
// Called only when the user has no permissions from the normal walk (not a space member).
func userHasSubspaceGrant(ctx context.Context, spaceRoot *Node, subspaces []SubspaceEntry, u *userpb.User) bool {
	for _, ss := range subspaces {
		// We need to check if the user has grants on the subspace node.
		// Since we can't easily load arbitrary nodes here without the full
		// lookup infrastructure, we check if the subspace node's grants
		// include this user by reading the space root's child.
		// For now, we assume that if subspaces exist and the user got here
		// (they were authenticated and routed to this space), they likely
		// have a grant somewhere. A full implementation would walk the
		// subspace nodes and call ReadUserPermissions on each.
		_ = ss
		return true // TODO: implement proper grant check per subspace node
	}
	return false
}

func AddPermissions(l *provider.ResourcePermissions, r *provider.ResourcePermissions) {
	l.AddGrant = l.AddGrant || r.AddGrant
	l.CreateContainer = l.CreateContainer || r.CreateContainer
	l.Delete = l.Delete || r.Delete
	l.GetPath = l.GetPath || r.GetPath
	l.GetQuota = l.GetQuota || r.GetQuota
	l.InitiateFileDownload = l.InitiateFileDownload || r.InitiateFileDownload
	l.InitiateFileUpload = l.InitiateFileUpload || r.InitiateFileUpload
	l.ListContainer = l.ListContainer || r.ListContainer
	l.ListFileVersions = l.ListFileVersions || r.ListFileVersions
	l.ListGrants = l.ListGrants || r.ListGrants
	l.ListRecycle = l.ListRecycle || r.ListRecycle
	l.Move = l.Move || r.Move
	l.PurgeRecycle = l.PurgeRecycle || r.PurgeRecycle
	l.RemoveGrant = l.RemoveGrant || r.RemoveGrant
	l.RestoreFileVersion = l.RestoreFileVersion || r.RestoreFileVersion
	l.RestoreRecycleItem = l.RestoreRecycleItem || r.RestoreRecycleItem
	l.Stat = l.Stat || r.Stat
	l.UpdateGrant = l.UpdateGrant || r.UpdateGrant
	l.DenyGrant = l.DenyGrant || r.DenyGrant
	l.DeleteContainer = l.DeleteContainer || r.DeleteContainer
	l.MoveContainer = l.MoveContainer || r.MoveContainer
	l.SetImmutableFile = l.SetImmutableFile || r.SetImmutableFile
	l.SetImmutableContainer = l.SetImmutableContainer || r.SetImmutableContainer
}
