# Testbench reva (KOSMOS)

```
cd /data/source/gitapps/opencloud_reva/testbench
./run_subspace_specs.sh          # Subspace-Specs, ohne Instanz, wenige Sekunden
./run_subspace_specs.sh --all    # ganze decomposedfs-Suite
```

Pflicht vor jedem Commit an Grants, Freigaben, Subspaces oder der Rechteberechnung
(`pkg/storage/pkg/decomposedfs`, gateway `usershareprovider.go`, `storageprovider.go`).

| Datei | Inhalt |
|---|---|
| `decomposedfs/subspace_share_boundary_test.go` | Abgrenzung normale Freigabe / Subspace, Rechte-Walk, fünf Rollen, echte Rechte-Engine |
| `decomposedfs/subspace_integration_test.go` | Registrierung, Space-Sichtbarkeit, Ordnerlisten, fremde Subspaces, Verwaltungs-Bypass |
| `decomposedfs/node/subspace_permissions_test.go` | Rechte in verschachtelten Subspaces |
| `decomposedfs/node/subspace_test.go` | Hilfsfunktionen der Subspace-Liste |

Die Kette Graph-API → Gateway → Storageprovider prüfen diese Specs nicht. Dafür gibt es den Test gegen eine
laufende Instanz: `opencloud/testbench/subspace_share_boundary.py`.

## Bekannte Fehler bei `--all` (Stand 2026-11-18, schon vor dem Subspace-Fix vorhanden)

- 6 × FAIL: Immutable (1), Delete ohne Recht (2), purge-Index (3)
- 5 × PANICKED: CreateDir (3), Grants „AddGrant ohne Recht“ (2). Der Mock `CheckPermission` wird öfter aufgerufen
  als vorbereitet. Diese Tests prüfen derzeit nichts.
