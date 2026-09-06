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

package decomposedfs

import (
	"context"
	"path/filepath"
	"strings"

	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	"github.com/opencloud-eu/reva/v2/internal/grpc/services/storageprovider"
	"github.com/opencloud-eu/reva/v2/pkg/appctx"
	ctxpkg "github.com/opencloud-eu/reva/v2/pkg/ctx"
	"github.com/opencloud-eu/reva/v2/pkg/errtypes"
	"github.com/opencloud-eu/reva/v2/pkg/storage/pkg/decomposedfs/metadata"
	"github.com/opencloud-eu/reva/v2/pkg/storage/pkg/decomposedfs/metadata/prefixes"
	"github.com/opencloud-eu/reva/v2/pkg/storage/pkg/decomposedfs/node"
	"github.com/opencloud-eu/reva/v2/pkg/storage/utils/ace"
	"github.com/opencloud-eu/reva/v2/pkg/storagespace"
	"github.com/opencloud-eu/reva/v2/pkg/utils"
)

// DenyGrant denies access to a resource.
func (fs *Decomposedfs) DenyGrant(ctx context.Context, ref *provider.Reference, grantee *provider.Grantee) error {
	_, span := tracer.Start(ctx, "DenyGrant")
	defer span.End()
	log := appctx.GetLogger(ctx)

	log.Debug().Interface("ref", ref).Interface("grantee", grantee).Msg("DenyGrant()")

	grantNode, err := fs.lu.NodeFromResource(ctx, ref)
	if err != nil {
		return err
	}
	if !grantNode.Exists {
		return errtypes.NotFound(filepath.Join(grantNode.ParentID, grantNode.Name))
	}

	// set all permissions to false
	grant := &provider.Grant{
		Grantee:     grantee,
		Permissions: &provider.ResourcePermissions{},
	}

	// add acting user
	u := ctxpkg.ContextMustGetUser(ctx)
	grant.Creator = u.GetId()

	rp, err := fs.p.AssemblePermissions(ctx, grantNode)

	switch {
	case err != nil:
		return err
	case !rp.DenyGrant:
		return errtypes.PermissionDenied(filepath.Join(grantNode.ParentID, grantNode.Name))
	}

	return fs.storeGrant(ctx, grantNode, grant)
}

// AddGrant adds a grant to a resource
func (fs *Decomposedfs) AddGrant(ctx context.Context, ref *provider.Reference, g *provider.Grant) (err error) {
	_, span := tracer.Start(ctx, "AddGrant")
	defer span.End()
	log := appctx.GetLogger(ctx)
	log.Debug().Interface("ref", ref).Interface("grant", g).Msg("AddGrant()")
	grantNode, unlockFunc, grant, err := fs.loadGrant(ctx, ref, g)
	if err != nil {
		return err
	}
	defer func() {
		_ = unlockFunc()
	}()

	if grant != nil {
		return errtypes.AlreadyExists(filepath.Join(grantNode.ParentID, grantNode.Name))
	}

	owner := grantNode.Owner()
	grants, err := grantNode.ListGrants(ctx)
	if err != nil {
		return err
	}

	// If the owner is empty and there are no grantees then we are dealing with a just created project space.
	// In this case we don't need to check for permissions and just add the grant since this will be the project
	// manager.
	// When the owner is empty but grants are set then we do want to check the grants.
	// However, if we are trying to edit an existing grant we do not have to check for permission if the user owns the grant
	// TODO: find a better to check this
	if len(grants) != 0 || (owner != nil && owner.OpaqueId != "" && (owner.OpaqueId != grantNode.SpaceID || owner.Type != 8)) {
		// Space managers (ManageSpaceProperties) can manage grants on any node
		// within the space, including subspace roots, without needing a CS3 grant.
		if !fs.p.ManageSpaceProperties(ctx, grantNode.SpaceID) {
			rp, err := fs.p.AssemblePermissions(ctx, grantNode)
			switch {
			case err != nil:
				return err
			case !rp.AddGrant:
				f, _ := storagespace.FormatReference(ref)
				if rp.Stat {
					return errtypes.PermissionDenied(f)
				}
				return errtypes.NotFound(f)
			}
		}
	}

	if fs.o.MultiTenantEnabled {
		spaceTenant, err := grantNode.SpaceRoot.XattrString(ctx, prefixes.SpaceTenantIDAttr)
		if err != nil {
			log.Error().Err(err).Msg("failed to read tenant id of space")
			return errtypes.InternalError("error validating tenantID")
		}
		if g.Grantee.Type == provider.GranteeType_GRANTEE_TYPE_USER {
			if g.Grantee.GetUserId().GetTenantId() != spaceTenant {
				log.Error().Str("spaceTenant", spaceTenant).Str("granteeTenant", g.Grantee.GetUserId().GetTenantId()).Msg("cannot add grant for user from different tenant")
				return errtypes.PermissionDenied("cannot add grant for user from different tenant")
			}
		}
	}

	if err := fs.storeGrant(ctx, grantNode, g); err != nil {
		return err
	}

	// Auto-register as subspace when a grant is added to a non-root folder
	// in a project space.
	appctx.GetLogger(ctx).Info().Str("nodeid", grantNode.ID).Str("spaceid", grantNode.SpaceID).Msg("AddGrant: calling autoAddSubspace")
	fs.autoAddSubspace(ctx, grantNode)

	return nil
}

// ListGrants lists the grants on the specified resource
func (fs *Decomposedfs) ListGrants(ctx context.Context, ref *provider.Reference) (grants []*provider.Grant, err error) {
	_, span := tracer.Start(ctx, "ListGrants")
	defer span.End()
	var grantNode *node.Node
	if grantNode, err = fs.lu.NodeFromResource(ctx, ref); err != nil {
		return
	}
	if !grantNode.Exists {
		err = errtypes.NotFound(filepath.Join(grantNode.ParentID, grantNode.Name))
		return
	}
	rp, err := fs.p.AssemblePermissions(ctx, grantNode)
	switch {
	case err != nil:
		return nil, err
	case !rp.ListGrants && !rp.Stat:
		f, _ := storagespace.FormatReference(ref)
		return nil, errtypes.NotFound(f)
	}
	log := appctx.GetLogger(ctx)
	var attrs node.Attributes
	if attrs, err = grantNode.Xattrs(ctx); err != nil {
		log.Error().Err(err).Msg("error listing attributes")
		return nil, err
	}

	aces := []*ace.ACE{}
	for k, v := range attrs {
		if strings.HasPrefix(k, prefixes.GrantPrefix) {
			var err error
			var e *ace.ACE
			principal := k[len(prefixes.GrantPrefix):]
			if e, err = ace.Unmarshal(principal, v); err != nil {
				log.Error().Err(err).Str("principal", principal).Str("attr", k).Msg("could not unmarshal ace")
				continue
			}
			aces = append(aces, e)
		}
	}

	uid := ctxpkg.ContextMustGetUser(ctx).GetId()
	grants = make([]*provider.Grant, 0, len(aces))
	for i := range aces {
		g := aces[i].Grant()

		// you may list your own grants even without listgrants permission
		if !rp.ListGrants && !utils.UserIDEqual(g.Creator, uid) && !utils.UserIDEqual(g.Grantee.GetUserId(), uid) {
			continue
		}

		grants = append(grants, g)
	}

	return grants, nil
}

// RemoveGrant removes a grant from resource
func (fs *Decomposedfs) RemoveGrant(ctx context.Context, ref *provider.Reference, g *provider.Grant) (err error) {
	_, span := tracer.Start(ctx, "RemoveGrant")
	defer span.End()
	grantNode, unlockFunc, grant, err := fs.loadGrant(ctx, ref, g)
	if err != nil {
		return err
	}
	defer func() {
		_ = unlockFunc()
	}()

	if grant == nil {
		return errtypes.NotFound("grant not found")
	}

	// you are allowed to remove grants if you created them yourself, have the proper
	// permission, or are a space manager (ManageSpaceProperties)
	if !utils.UserIDEqual(grant.Creator, ctxpkg.ContextMustGetUser(ctx).GetId()) && !fs.p.ManageSpaceProperties(ctx, grantNode.SpaceID) {
		rp, err := fs.p.AssemblePermissions(ctx, grantNode)
		switch {
		case err != nil:
			return err
		case !rp.RemoveGrant:
			f, _ := storagespace.FormatReference(ref)
			if rp.Stat {
				return errtypes.PermissionDenied(f)
			}
			return errtypes.NotFound(f)
		}
	}

	if err := grantNode.DeleteGrant(ctx, g, false); err != nil {
		return err
	}

	if isShareGrant(ctx) {
		// For share grants on a non-root node in a project space, remove the
		// user/group from the space index if no other grants remain on the node.
		// This is the inverse of the linkSpaceByUser/Group in storeGrant.
		if grantNode.ID != grantNode.SpaceRoot.ID {
			if actualSpaceType, err := grantNode.SpaceRoot.XattrString(ctx, prefixes.SpaceTypeAttr); err == nil && actualSpaceType == _spaceTypeProject {
				remaining, err := grantNode.ListGrants(ctx)
				if err == nil && len(remaining) == 0 {
					switch g.Grantee.Type {
					case provider.GranteeType_GRANTEE_TYPE_USER:
						_ = fs.userSpaceIndex.Remove(g.Grantee.GetUserId().GetOpaqueId(), grantNode.SpaceID)
					case provider.GranteeType_GRANTEE_TYPE_GROUP:
						_ = fs.groupSpaceIndex.Remove(g.Grantee.GetGroupId().GetOpaqueId(), grantNode.SpaceID)
					}
				}
			}
		}
	} else {
		// invalidate space grant
		switch g.Grantee.Type {
		case provider.GranteeType_GRANTEE_TYPE_USER:
			// remove from user index
			if err := fs.userSpaceIndex.Remove(g.Grantee.GetUserId().GetOpaqueId(), grantNode.SpaceID); err != nil {
				return err
			}
		case provider.GranteeType_GRANTEE_TYPE_GROUP:
			// remove from group index
			if err := fs.groupSpaceIndex.Remove(g.Grantee.GetGroupId().GetOpaqueId(), grantNode.SpaceID); err != nil {
				return err
			}
		}
	}

	// Auto-remove subspace when last grant is removed from a folder.
	fs.autoRemoveSubspace(ctx, grantNode)

	return fs.tp.Propagate(ctx, grantNode, 0)
}

func isShareGrant(ctx context.Context) bool {
	_, ok := storageprovider.SpaceTypeFromContext(ctx)
	return !ok
}

// UpdateGrant updates a grant on a resource
// TODO remove AddGrant or UpdateGrant grant from CS3 api, redundant? tracked in https://github.com/cs3org/cs3apis/issues/92
func (fs *Decomposedfs) UpdateGrant(ctx context.Context, ref *provider.Reference, g *provider.Grant) error {
	_, span := tracer.Start(ctx, "UpdateGrant")
	defer span.End()
	log := appctx.GetLogger(ctx)
	log.Debug().Interface("ref", ref).Interface("grant", g).Msg("UpdateGrant()")

	grantNode, unlockFunc, grant, err := fs.loadGrant(ctx, ref, g)
	if err != nil {
		return err
	}
	defer func() {
		_ = unlockFunc()
	}()

	if grant == nil {
		// grant not found
		// TODO: fallback to AddGrant?
		return errtypes.NotFound(g.Grantee.GetUserId().GetOpaqueId())
	}

	// You may update a grant when you have the UpdateGrant permission, created the grant,
	// or are a space manager (ManageSpaceProperties)
	if !utils.UserIDEqual(grant.Creator, ctxpkg.ContextMustGetUser(ctx).GetId()) && !fs.p.ManageSpaceProperties(ctx, grantNode.SpaceID) {
		rp, err := fs.p.AssemblePermissions(ctx, grantNode)
		switch {
		case err != nil:
			return err
		case !rp.UpdateGrant:
			f, _ := storagespace.FormatReference(ref)
			if rp.Stat {
				return errtypes.PermissionDenied(f)
			}
			return errtypes.NotFound(f)
		}
	}

	return fs.storeGrant(ctx, grantNode, g)
}

// checks if the given grant exists and returns it. Nil grant means it doesn't exist
func (fs *Decomposedfs) loadGrant(ctx context.Context, ref *provider.Reference, g *provider.Grant) (*node.Node, metadata.UnlockFunc, *provider.Grant, error) {
	_, span := tracer.Start(ctx, "loadGrant")
	defer span.End()
	n, err := fs.lu.NodeFromResource(ctx, ref)
	if err != nil {
		return nil, nil, nil, err
	}
	if !n.Exists {
		return nil, nil, nil, errtypes.NotFound(filepath.Join(n.ParentID, n.Name))
	}

	// lock the metadata file
	unlockFunc, err := fs.lu.MetadataBackend().Lock(n)
	if err != nil {
		return nil, nil, nil, err
	}

	grants, err := n.ListGrants(ctx)
	if err != nil {
		return nil, nil, nil, err
	}

	for _, grant := range grants {
		switch grant.Grantee.GetType() {
		case provider.GranteeType_GRANTEE_TYPE_USER:
			if g.Grantee.GetUserId().GetOpaqueId() == grant.Grantee.GetUserId().GetOpaqueId() {
				return n, unlockFunc, grant, nil
			}
		case provider.GranteeType_GRANTEE_TYPE_GROUP:
			if g.Grantee.GetGroupId().GetOpaqueId() == grant.Grantee.GetGroupId().GetOpaqueId() {
				return n, unlockFunc, grant, nil
			}
		}
	}

	return n, unlockFunc, nil, nil
}

func (fs *Decomposedfs) storeGrant(ctx context.Context, n *node.Node, g *provider.Grant) error {
	_, span := tracer.Start(ctx, "storeGrant")
	defer span.End()
	// if is a grant to a space root, the receiver needs the space type to update the indexes
	spaceType, ok := storageprovider.SpaceTypeFromContext(ctx)
	if !ok {
		// this is not a grant on a space root we are just adding a share
		spaceType = spaceTypeShare
	}

	// set the grant
	e := ace.FromGrant(g)
	principal, value := e.Marshal()
	attribs := node.Attributes{
		prefixes.GrantPrefix + principal: value,
	}
	if err := n.SetXattrsWithContext(ctx, attribs, false); err != nil {
		appctx.GetLogger(ctx).Error().Err(err).
			Str("principal", principal).Msg("Could not set grant for principal")
		return err
	}

	// update the indexes only after successfully setting the grant
	err := fs.updateIndexes(ctx, g.GetGrantee(), spaceType, n.SpaceID, n.ID)
	if err != nil {
		return err
	}

	// If this is a share grant on a non-root node (subspace member),
	// the user/group still needs to be linked to the parent space so that
	// the space appears in me/drives. updateIndexes() skips the user/group
	// index for share grants, so we add it explicitly here.
	// We do this for ALL non-root share grants in project spaces — the node
	// may or may not already be registered as a subspace (autoAddSubspace
	// runs after storeGrant for new subspaces).
	if !ok && n.ID != n.SpaceRoot.ID {
		actualSpaceType, err := n.SpaceRoot.XattrString(ctx, prefixes.SpaceTypeAttr)
		if err == nil && actualSpaceType == _spaceTypeProject {
			// The index value must be the space-root symlink path (same as
			// updateIndexes uses), NOT the node ID. ResolveSpaceIDIndexEntry
			// parses this to recover the space ID.
			target := fs.tp.BuildSpaceIDIndexEntry(n.SpaceID)
			switch g.Grantee.Type {
			case provider.GranteeType_GRANTEE_TYPE_USER:
				if err := fs.linkSpaceByUser(ctx, g.Grantee.GetUserId().GetOpaqueId(), n.SpaceID, target); err != nil {
					appctx.GetLogger(ctx).Warn().Err(err).Str("spaceid", n.SpaceID).Msg("storeGrant: failed to link subspace grant by user")
				}
			case provider.GranteeType_GRANTEE_TYPE_GROUP:
				if err := fs.linkSpaceByGroup(ctx, g.Grantee.GetGroupId().GetOpaqueId(), n.SpaceID, target); err != nil {
					appctx.GetLogger(ctx).Warn().Err(err).Str("spaceid", n.SpaceID).Msg("storeGrant: failed to link subspace grant by group")
				}
			}
		}
	}

	return fs.tp.Propagate(ctx, n, 0)
}

// autoAddSubspace registers a folder as subspace when it receives its first
// share grant in a project space. This keeps the subspace list consistent
// without relying on the UI to call SetSubspace separately.
func (fs *Decomposedfs) autoAddSubspace(ctx context.Context, n *node.Node) {
	log := appctx.GetLogger(ctx)
	// Only for non-root nodes in project spaces (SPACE_OWNER type)
	if n.ID == n.SpaceRoot.ID {
		log.Info().Str("nodeid", n.ID).Msg("autoAddSubspace: skip (is space root)")
		return
	}
	// Only for project spaces. Check the space type directly — it is always
	// set on the space root, unlike owner.type which may be missing (e.g.
	// migrated spaces) and would wrongly skip subspace registration.
	spaceType, err := n.SpaceRoot.XattrString(ctx, prefixes.SpaceTypeAttr)
	if err != nil || spaceType != _spaceTypeProject {
		log.Info().Str("nodeid", n.ID).Str("spaceType", spaceType).Msg("autoAddSubspace: skip (not project space)")
		return
	}
	// Only a user with the OpenCloud "Manage space properties" right
	// (Drives.ReadWrite) may trigger subspace registration. This checks the
	// OpenCloud structured permission via the settings service, not the CS3
	// RemoveGrant/IsManager check.
	if !fs.p.ManageSpaceProperties(ctx, n.SpaceID) {
		log.Info().Str("nodeid", n.ID).Str("spaceid", n.SpaceID).Msg("autoAddSubspace: skip (no manage space properties right)")
		return
	}
	// Already a subspace?
	if node.IsSubspaceID(n.ID, node.GetSubspaceList(ctx, n.SpaceRoot)) {
		log.Info().Str("nodeid", n.ID).Msg("autoAddSubspace: skip (already subspace)")
		return
	}
	// Determine path relative to space root
	p, err := fs.lu.Path(ctx, n, func(_ *node.Node) bool { return true })
	if err != nil {
		appctx.GetLogger(ctx).Warn().Err(err).Str("nodeid", n.ID).Msg("autoAddSubspace: could not determine path")
		return
	}
	if err := node.AddSubspace(ctx, n.SpaceRoot, n.ID, p); err != nil {
		appctx.GetLogger(ctx).Warn().Err(err).Str("nodeid", n.ID).Msg("autoAddSubspace: failed")
	} else {
		appctx.GetLogger(ctx).Info().Str("nodeid", n.ID).Str("path", p).Msg("autoAddSubspace: registered")
	}
}

// autoRemoveSubspace removes a folder from the subspace list when its last
// share grant is removed.
func (fs *Decomposedfs) autoRemoveSubspace(ctx context.Context, n *node.Node) {
	if n.ID == n.SpaceRoot.ID {
		return
	}
	// Only act if this node is currently a subspace
	if !node.IsSubspaceID(n.ID, node.GetSubspaceList(ctx, n.SpaceRoot)) {
		return
	}
	// Check if any grants remain
	grantees, err := n.ListGrantees(ctx)
	if err != nil {
		appctx.GetLogger(ctx).Warn().Err(err).Str("nodeid", n.ID).Msg("autoRemoveSubspace: could not list grantees")
		return
	}
	if len(grantees) > 0 {
		return // still has members
	}
	if err := node.RemoveSubspace(ctx, n.SpaceRoot, n.ID); err != nil {
		appctx.GetLogger(ctx).Warn().Err(err).Str("nodeid", n.ID).Msg("autoRemoveSubspace: failed")
	}
}
