package conversions

import (
	"testing"

	providerv1beta1 "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	"github.com/stretchr/testify/assert"
)

func TestContainerPermissionsInRoles(t *testing.T) {
	// Verify that new container-specific permissions are correctly assigned to roles

	editor := NewEditorRole().CS3ResourcePermissions()
	assert.True(t, editor.DeleteContainer, "Editor should have DeleteContainer")
	assert.True(t, editor.MoveContainer, "Editor should have MoveContainer")
	assert.False(t, editor.SetImmutableFile, "Editor should not have SetImmutableFile")
	assert.False(t, editor.SetImmutableContainer, "Editor should not have SetImmutableContainer")

	spaceEditor := NewSpaceEditorRole().CS3ResourcePermissions()
	assert.True(t, spaceEditor.DeleteContainer, "SpaceEditor should have DeleteContainer")
	assert.True(t, spaceEditor.MoveContainer, "SpaceEditor should have MoveContainer")

	manager := NewManagerRole().CS3ResourcePermissions()
	assert.True(t, manager.DeleteContainer, "Manager should have DeleteContainer")
	assert.True(t, manager.MoveContainer, "Manager should have MoveContainer")
	assert.True(t, manager.SetImmutableFile, "Manager should have SetImmutableFile")
	assert.True(t, manager.SetImmutableContainer, "Manager should have SetImmutableContainer")

	coowner := NewCoownerRole().CS3ResourcePermissions()
	assert.True(t, coowner.DeleteContainer, "Coowner should have DeleteContainer")
	assert.True(t, coowner.MoveContainer, "Coowner should have MoveContainer")
	assert.True(t, coowner.SetImmutableFile, "Coowner should have SetImmutableFile")
	assert.True(t, coowner.SetImmutableContainer, "Coowner should have SetImmutableContainer")

	viewer := NewViewerRole().CS3ResourcePermissions()
	assert.False(t, viewer.DeleteContainer, "Viewer should not have DeleteContainer")
	assert.False(t, viewer.MoveContainer, "Viewer should not have MoveContainer")
	assert.False(t, viewer.SetImmutableFile, "Viewer should not have SetImmutableFile")

	// Verify SufficientPermissions: manager permissions should be sufficient for editor
	assert.True(t, SufficientCS3Permissions(manager, editor),
		"Manager permissions should be sufficient for Editor")

	// Verify SufficientPermissions: editor should not be sufficient for manager (missing SetImmutable)
	assert.False(t, SufficientCS3Permissions(editor, manager),
		"Editor permissions should not be sufficient for Manager")
}

func TestSufficientPermissions(t *testing.T) {
	type testData struct {
		Existing   *providerv1beta1.ResourcePermissions
		Requested  *providerv1beta1.ResourcePermissions
		Sufficient bool
	}
	table := []testData{
		{
			Existing:   nil,
			Requested:  nil,
			Sufficient: false,
		},
		{
			Existing:   RoleFromName("editor").CS3ResourcePermissions(),
			Requested:  nil,
			Sufficient: false,
		},
		{
			Existing:   nil,
			Requested:  RoleFromName("viewer").CS3ResourcePermissions(),
			Sufficient: false,
		},
		{
			Existing:   RoleFromName("editor").CS3ResourcePermissions(),
			Requested:  RoleFromName("viewer").CS3ResourcePermissions(),
			Sufficient: true,
		},
		{
			Existing:   RoleFromName("viewer").CS3ResourcePermissions(),
			Requested:  RoleFromName("editor").CS3ResourcePermissions(),
			Sufficient: false,
		},
		{
			Existing:   RoleFromName("spaceviewer").CS3ResourcePermissions(),
			Requested:  RoleFromName("spaceeditor").CS3ResourcePermissions(),
			Sufficient: false,
		},
		{
			Existing:   RoleFromName("manager").CS3ResourcePermissions(),
			Requested:  RoleFromName("spaceeditor").CS3ResourcePermissions(),
			Sufficient: true,
		},
		{
			Existing:   RoleFromName("manager").CS3ResourcePermissions(),
			Requested:  RoleFromName("spaceviewer").CS3ResourcePermissions(),
			Sufficient: true,
		},
		{
			Existing:   RoleFromName("manager").CS3ResourcePermissions(),
			Requested:  RoleFromName("manager").CS3ResourcePermissions(),
			Sufficient: true,
		},
		{
			Existing:   RoleFromName("manager").CS3ResourcePermissions(),
			Requested:  RoleFromName("denied").CS3ResourcePermissions(),
			Sufficient: true,
		},
		{
			Existing:   RoleFromName("spaceeditor").CS3ResourcePermissions(),
			Requested:  RoleFromName("denied").CS3ResourcePermissions(),
			Sufficient: false,
		},
		{
			Existing:   RoleFromName("editor").CS3ResourcePermissions(),
			Requested:  RoleFromName("denied").CS3ResourcePermissions(),
			Sufficient: false,
		},
		{
			Existing:   RoleFromName("secure-viewer").CS3ResourcePermissions(),
			Requested:  RoleFromName("secure-viewer").CS3ResourcePermissions(),
			Sufficient: true,
		},
		{
			Existing:   RoleFromName("secure-viewer").CS3ResourcePermissions(),
			Requested:  RoleFromName("viewer").CS3ResourcePermissions(),
			Sufficient: false,
		},
		{
			Existing:   RoleFromName("secure-viewer").CS3ResourcePermissions(),
			Requested:  RoleFromName("editor").CS3ResourcePermissions(),
			Sufficient: false,
		},
		{
			Existing: &providerv1beta1.ResourcePermissions{
				// all permissions, used for personal space owners
				AddGrant:             true,
				CreateContainer:      true,
				Delete:               true,
				GetPath:              true,
				GetQuota:             true,
				InitiateFileDownload: true,
				InitiateFileUpload:   true,
				ListContainer:        true,
				ListFileVersions:     true,
				ListGrants:           true,
				ListRecycle:          true,
				Move:                 true,
				PurgeRecycle:         true,
				RemoveGrant:          true,
				RestoreFileVersion:   true,
				RestoreRecycleItem:   true,
				Stat:                 true,
				UpdateGrant:          true,
				DenyGrant:            true,
			},
			Requested:  RoleFromName("denied").CS3ResourcePermissions(),
			Sufficient: true,
		},
	}
	for _, test := range table {
		assert.Equal(t, test.Sufficient, SufficientCS3Permissions(test.Existing, test.Requested))
	}
}
