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


## Bekannte Fehler bei `--all` (Stand 2026-11-20)

- Keine festen Fehler: `--all` läuft grün. Die 3 purge-Specs waren Testfehler (purgten als Space-Owner, seit
  750675c14 darf das nur `delete-all-spaces`); behoben mit dem Upstream-Testfix b08d8bb99.
- `decomposedfs/upload` schlug im Gesamtlauf am 18.11. gelegentlich fehl. Am 20.11. in 7 Gesamtläufen nicht
  aufgetreten, Ursache ungeklärt.

Behoben am 2026-11-18: 5 Panics (Verwaltungs-Bypass fragte in jedem Space den Rechte-Dienst) und 3 FAIL
(Delete ohne Recht, SetImmutable ohne Recht: Mock zu spät registriert, der Test prüfte mit Owner-Rechten).
