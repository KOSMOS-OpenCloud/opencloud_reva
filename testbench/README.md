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
| `decomposedfs/subspace_bypass_scope_test.go` | Reichweite des Verwaltungs-Bypass: nie in persönlichen Spaces, in Project-Spaces nur Ordner |
| `decomposedfs/subspace_integration_test.go` | Registrierung, Space-Sichtbarkeit, Ordnerlisten, fremde Subspaces, Verwaltungs-Bypass |
| `decomposedfs/node/subspace_permissions_test.go` | Rechte in verschachtelten Subspaces |
| `decomposedfs/node/subspace_test.go` | Hilfsfunktionen der Subspace-Liste |

Die Kette Graph-API → Gateway → Storageprovider prüfen diese Specs nicht. Dafür gibt es den Test gegen eine
laufende Instanz: `opencloud/testbench/subspace_share_boundary.py`.


## Bekannte Fehler bei `--all` (Stand 2026-11-18)

- 3 × FAIL „can delete (purge) project spaces“ (by-user-id-Index): Die Specs purgen als Space-Owner, der Mock der
  Datei erlaubt `delete-all-spaces` nur dem `DeleteAllSpacesUser` → PermissionDenied. Ungeklärt, ob Test oder Mock
  dem gewollten Verhalten entspricht (Specs stammen aus Upstream, Merge #695).
- `decomposedfs/upload` schlägt im Gesamtlauf gelegentlich fehl, allein dreimal grün. Nicht untersucht.

Behoben am 2026-11-18: 5 Panics (Verwaltungs-Bypass fragte in jedem Space den Rechte-Dienst) und 3 FAIL
(Delete ohne Recht, SetImmutable ohne Recht: Mock zu spät registriert, der Test prüfte mit Owner-Rechten).
