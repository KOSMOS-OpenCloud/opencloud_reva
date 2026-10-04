# MOVE 500 — „Fehler beim Verschieben“ (brandis.eu, 2026-09-29)

## Symptom

UI zeigt „Fehler beim Verschieben von »2026-08-11 Harttig Tobias Krankmeldung.pdf«"
mit X-Request-Id `d467516b-7072-41f5-b54e-b1cc0f484063`.

## Request

```
MOVE /dav/spaces/f7e671d7-36e5-493f-b0c7-ffe5ee4319a5$79f79149-7c4e-4dd5-94f7-19340b938e06/FB Interne Service/Berger/Inbox/2026-08-11 Harttig Tobias Krankmeldung.pdf
     → 500 (14:40:00 UTC)
```

- Quell-Space: `79f79149-7c4e-4dd5-94f7-19340b938e06` (Posteingang)
- Ziel-Space:  `5ac86946-ee5f-4fa7-ac60-04aa9397dae8` („Innere Verwaltung“)
- Trace: `2be3d05c7093929dc9d6ac3768e5a5cf`

## Logs (storage-users, 14:40:00 UTC)

```
info | subspace-debug: GetMD permissions after subspaceGrantPermission | sp: 79f79149… | node: 75d8d7e9-…
info | subspace-debug: GetMD permissions after subspaceGrantPermission | sp: 5ac86946… | node: 52d85d62-…
error | error listing attributes  | node.XattrsWithReader: no data available | sp: 5ac86946… | nodeid: (leer)
error | error listing grantees    | node.XattrsWithReader: no data available | sp: 5ac86946… | nodeid: (leer)
error | error reading permissions | node.XattrsWithReader: no data available | sp: 5ac86946… | nodeid: (leer)
info | access-log | status: 500 | MOVE
```

`nodeid` leer = **Space-Root des Zielspace**.

## WICHTIG: Fehler ist intermittierend

| Zeitpunkt (UTC) | Operation | Status | Errors |
|---|---|---|---|
| 13:39:15 | Cross-Space-Move (Innere Verw. → Personal…) | **201** | identische 3 |
| 14:40:00 | Move Posteingang → Innere Verw. (unserer Fall) | **500** | identische 3 |
| 14:43:07 | Cross-Space-Move (Posteingang → Innere Verw.) | **201** | identische 3 |

→ Die 3 Error-Logzeilen sind **nicht die Ursache des 500**. Sie erscheinen bei
Success UND Failure. Die echte Fehlerursache wird nicht geloggt.

## Disk-Zustand (geprüft)

- Ziel-Space-Root `5ac86946…`: alle xattrs vorhanden (19 `user.oc.*`, grants, subspaces)
- Kein Offload-Marker (`user.oc.metadata_offloaded`), 0 `.mpk`-Dateien im Space
- 92k `locks/*.mlock`-Dateien im `.oc-nodes/locks/` = **erwartet** (Hybrid-Backend-Pfad
  `<spaceRoot>/.oc-nodes/locks/<id>.mlock`)
- Kein `error getting parent for node` im Log (stelle die den Permissions-Walk hart
  abbrechen würde)

## Ist es das bekannte nodeid-Problem?

Nein. Der frühere Bug „Datei wird beim Move ohne nodeid gespeichert → nächste
Operation bricht" passt hier nicht:

- Der failing call hat `spaceid` gesetzt und `nodeid` leer — das ist der **Space-Root**
  des Ziels, nicht die bewegte Datei
- Die bewegte Datei selbst (Quelle `75d8d7e9-…`, Ziel `52d85d62-…` = Ordner) hat
  gültige IDs
- Cross-Space-Move wird hier nur EINMAL ausgeführt (kein 2-stufiger Flow)

Also: **kein nodeid-Datenkorruptions-Fall**.

## Warum tritt der Error-Log-Eintrag auf (auch bei 201)?

Der Permissions-Walk (`assemblePermissions`) am Ziel liest für jeden Parent-Node
die xattrs. Beim Space-Root selbst wird `ReadUserPermissions` aufgerufen — dort wird
`ListGrantees` → `Xattrs` → `XattrsWithReader` aufgerufen. Die Fehlermeldung kommt
aus `node.go:1393` (`ListGrantees`) und `node.go:1285` (`ReadUserPermissions`-Root-Fall).

Dass die 3 Fehler bei Erfolg UND Fehler auftreten deutet auf einen **Race beim
Cross-Space-Move**: `SetMultiple` am Ziel-Node (crossSpaceMove Schritt 4) und der
Permissions-Walk auf dem Space-Root laufen parallel, und das Hybrid-Backend
`getAll` gibt in bestimmten Momenten `ENOATTR`/`ENODATA` zurück, obwohl die
xattrs auf Disk existieren.

## Offene Frage: Was blockiert den Move?

Nichts im Sinne eines File-Locks. Die PDF selbst wird nur **gelesen** (ReadBlob),
der Reader wird sofort geschlossen (`reader.Close()` in crossSpaceMove).
Ein geöffneter PDF-Reader in der UI hält **keine** lock-Datei am File.

Mögliche Blocker:
1. **Race zwischen crossSpaceMove + Permissions-Walk** (am wahrscheinlichsten)
2. **NATS Event-Consumer** blockiert den Postprocessing-Step (Move-Event wird
   publishet, Consumer hängt) — aber das würde nach 40ms (Request-Dauer) nicht
   blockieren
3. **Transitions-Fehler** im WebDAV-Frontend — aber der Trace zeigt keinen
   Frontend-Fehler, nur die storage-users-Errors

## Vorschlag: Logging um den wahren Fehler zu sehen

Die 3 Error-Logzeilen kommen aus `node.go` (`ListGrantees`, `ReadUserPermissions`,
`XattrsWithReader`). Um den **tatsächlichen 500-Fehler** zu sehen, brauchen wir:

1. **In `crossSpaceMove`**: Jede Step (1–7) mit `log.Info().Msg("crossSpaceMove step N")`
   loggen. So sehen wir, an welchem Step es scheitert.
2. **In `HybridBackend.getAll`** (`metadata/hybrid_backend.go:152`): Wenn `xattr.List`
   oder `xattr.Get` einen Fehler gibt, logge `path`, `len(attrNames)`, `xerr`
   (aktuell wird nur der `xerrs > 0`-Zähler inkrementiert, der Fehler selbst geht
   verloren)
3. **In `Decomposedfs.Move`** (`decomposedfs.go:855`): Um den finalen `fs.tp.Move`
   einen `log.Info()` mit Source/Target Path + `defer log.Error().Err(err)` einbauen

So können wir beim nächsten 500 sofort sehen: welcher Step, welche xattr, welche
Pfad-Kombination.

## UPDATE 2026-09-30: Debug-Log-Bug hat ALLE Moves blockiert

Nach Deploy des Debug-Loggings (Tag 20260929-1931, 29.09. 21:42 CEST) sind
**ALLE** Moves gescheitert (500) — auch Intra-Space-Moves. Ursache: **Bug im
eigenen Debug-Code**, nicht der MOVE-500-Bug.

### Der Panic (aus dem Recovery-Interceptor-Stack, 30.09. 11:43 UTC)

```
runtime error: invalid memory address or nil pointer dereference
github.com/opencloud-eu/reva/v2/pkg/storage/pkg/decomposedfs.(*Decomposedfs).Move
  decomposedfs.go:876
```

Zeile 876 = der neu eingefügte Info-Log "MOVE: resolved source node":

```go
log.Info().
    Str("source_space", oldNode.SpaceID).
    ...
    Str("dest_space", newNode.SpaceID). // BUG: newNode ist hier noch NIL!
    Msg("MOVE: resolved source node")
```

`newNode` wird erst später (`NodeFromResource(dest)`) aufgelöst.
`newNode.SpaceID` auf nil-`*node.Node` → Panic bei JEDEM Move.
Der gRPC-Recovery-Interceptor fängt es ab → `Internal: nil pointer dereference`
→ HTTP 500.

### Konsequenz

- Der eigentliche MOVE-500-Bug (29.09) ist damit überdeckt und weiterhin OFFEN.
- Intra-Space-Moves (die bisher liefen) sind seit 29.09. 21:42 CEST ebenfalls
  kaputt — das ist ein Regression durch das Debug-Deploy.
- Die "3 Error-Logzeilen" (no data available) waren weiterhin nur Rauschen
  (erscheinen bei 201 UND 500).

### Fix

Commit `2399d6699`: Die `dest_space`-Zeile aus dem "resolved source node"-Log
entfernt (newNode ist dort noch nil). Build läuft → neuer Tag → Deploy.

Danach:
- [x] Move-Test (Intra + Cross-Space) nach Deploy 20260930-1329 → beide OK
- [x] Cross-Space wieder intermittierend 500 → siehe UPDATE 2026-10-01

## UPDATE 2026-10-01: Echter Fehler lokalisiert (falsche `.mpk`-Datei)

### Reproduktion (Trace `d73fcef6c2a647429f4df4b3bc4de7d5`, 01.10. 12:52:46 CEST)

Cross-Space-Move Space `79f79149-7c4e-4dd5-94f7-19340b938e06` → `5ac86946-ee5f-4fa7-ac60-04aa9397dae8`:

```
MOVE: starting                    → storageprovider.go:818
MOVE: resolved source node        → source_node: cd288a88-3d00-459f-ae09-afef434f31a1
MOVE: resolved dest node          → dest_node: (leer, wie erwartet bei neuem Ziel)
MOVE: calling tp.Move             → decomposedfs.go:970
MOVE: tp.Move failed              → crossSpaceMove: error copying metadata:
  open /var/lib/.../projects/5ac86946-.../
       .oc-nodes/07/a4/fa/a2/-4963-44c3-863b-3ad778a88816.mpk: no such file or directory
MOVE: failed                      → storageprovider.go:826 → HTTP 500
```

### Inkonsistenz nachgewiesen

| | Wert |
|---|---|
| `user.oc.id` (xattr auf der Quelldatei) | `cd288a88-3d00-459f-ae09-afef434f31a1` |
| `source_node` im MOVE-Log (oldNode.ID) | `cd288a88-3d00-459f-ae09-afef434f31a1` ✓ |
| `.mpk`-Datei unter **Quell-Space** | **existiert NICHT** (auch nicht unter `cd288a88-…`) |
| `.mpk`-Datei, die `CopyMetadata` im **Ziel-Space** öffnen will | `.oc-nodes/07/a4/fa/a2/-4963-44c3-863b-3ad778a88816.mpk` |

**Die `.mpk`-Datei, die `CopyMetadata` im Ziel-Space anlegt, referenziert eine
Node-ID (`4963-44c3-863b-3ad778a88816`), die weder die Quell- noch die
Ziel-Node-ID ist.**

### Wer ermittelt / wer liefert die falsche ID?

Pfad: `posix/tree/tree.go:crossSpaceMove()` → `lookup.CopyMetadata()` →
`metadataBackend.SetMultiple()` (hybrid) → `MetadataPath(n)` →
`pathify(n.GetID(), 4, 2)` → `.oc-nodes/<4hex>/<2hex>/<2hex>/<2hex>/-<rest>.mpk`

`GetID()` liefert `n.ID` der `newNode`. In `crossSpaceMove` (tree.go:781–783)
wird bei leerem `newNode.ID` eine **frische UUID** gesetzt. Das Problem:

**Hypothese A — Race beim frischen UUID-Setzen:**
Zwischen Schritt 3 (`newNode.ID = uuid.New()`) und Schritt 4
(`CopyMetadata → SetMultiple`) könnte die `newNode`-Struktur parallel
(Permissions-Walk, Event-Consumer) einen eigenen `ID`-Wert setzen, der
dann `MetadataPath`-Aufruf mit `n.GetID()` trifft.

**Hypothese B — ID-Cache / Path-Cache-Inkonsistenz:**
Der `.mpk`-Pfad wird über `metadataPathFunc(n)` aufgelöst, die auf
`lu.IDCache.Get(ctx, n.GetSpaceID(), n.GetSpaceID())` für den Space-Root-Pfad
setzt. Wenn der IDCache für den **Ziel-Space-Root** einen veralteten Pfad
liefert (z.B. aus einer früheren Space-Partition), wird die `.mpk`-Datei
unter einen falschen `.oc-nodes`-Ordner geschrieben.

**Hypothese C — `newNode.ID` bleibt leer bis Schritt 3:**
Falls `newNode.ID` beim `CopyMetadata`-Aufruf noch `""` ist, würde
`pathify("", 4, 2)` einen leeren Pfad liefern → `.oc-nodes/.mpk`
(kann ausgeschlossen werden, da der Pfad ein gültiges UUID-Format hat).

### Nächste Schritte

- [ ] In `crossSpaceMove` (tree.go:780–788) `newNode.ID` vor und nach
      `uuid.New()` loggen — verifiziert Hypothese A
- [ ] In `HybridBackend.MetadataPath` (hybrid_backend.go:509) den
      resultierenden `.mpk`-Pfad loggen — verifiziert, dass `n.GetID()`
      den erwarteten Wert liefert
- [ ] In `lu.IDCache.Get` den Space-Root-Pfad loggen (nur bei Cross-Space-Move)

## UPDATE 2026-10-04: ROOT CAUSE + FIX

### Root Cause

Die obigen Hypothesen A/B/C waren **falsch**. Der eigentliche Bug:

`crossSpaceMove` kopiert **alle** xattrs der Quelle auf die Ziel-Datei,
**einschließlich** `user.oc.metadata_offloaded="1"`. Die Ziel-Datei hat aber
noch keinen `.mpk`-File (die neue Node-ID existiert erst seit `uuid.New()`).

Ablauf in `HybridBackend.SetMultiple`:
```
1. offloaded = xattr.Get(ZIEL, _metadataOffloadedAttr) → false (Ziel noch leer)
2. hasOffloadingAttrs=true, offloaded=false → mdSize berechnen
3. mdSize <= 1024 → offloadMetadata NICHT aufgerufen → offloaded bleibt false
4. if offloaded { ... } → übersprungen → KEIN .mpk angelegt
5. xattr.Set(ZIEL, "user.oc.metadata_offloaded", "1") → wird geschrieben!
6. getAll(ctx, n, skipCache=true, skipOffloaded=false)
   → liest user.oc.metadata_offloaded="1" von der Ziel-Datei
   → os.ReadFile(MetadataPath(n)) → ENOENT  ← FEHLER
```

### Fix (commit `36aa6a608`, Tag 20261004-1105)

In `crossSpaceMove` (tree.go:790) den `CopyMetadata`-Filter erweitert:

```go
case "user.oc.metadata_offloaded":
    hasOffloadMarker = true
    return nil, false  // NICHT kopieren
```

`HybridBackend.SetMultiple` wird den Offload neu durchführen, wenn
`mdSize > 1024` (Grants + `user.oc.md.*`), und den `.mpk` korrekt unter
der neuen Node-ID anlegen.

### Debug-Logging (Monitoring-Phase)

Jeder Cross-Space-Move loggt jetzt:
```
crossSpaceMove: metadata copied
  source_node=...  dest_node=...
  source_has_offload_marker=true/false
  offload_attr_count=N  offload_attr_bytes=N  offload_limit=1024
```

Damit können wir im Production-Log prüfen:
- Wie viele Moves betroffen sind (`source_has_offload_marker=true`)
- Ob `offload_attr_bytes` nahe an 1024 liegt (dann wird neu offloaded)
- Ob der Fix funktioniert (keine `error copying metadata` mehr)

### Xattr-Größenlimits (Kontext)

| FS | Limit/Xattr | Summe/Inode |
|---|---|---|
| ext4 | 4 KB | ~4 KB |
| Btrfs | 3.5 KB | ~32 KB |
| XFS | 64 KB | ~128 KB |

Die 1024-Byte-Offload-Schwelle ist konservativ für ext4 gewählt.
Bei 2 KB wäre der Puffer auf ext4 zu eng, bei 4 KB riskant.

## UPDATE 2026-10-04: Offload-Limit 1KB → 2KB + Analyse der MOVE-Bug-Datei

### Offload-Limit erhöht

Btrfs hat kein 4KB xattr limit wie ext4. Die 1KB-Schwelle auf 2KB erhöht
(commit `2f686b6`). Reduziert das Offload-Churn, das NOK_SIDE-Ukonsistenzen
produziert hat.

- Code: `posix.go:133` (`NewHybridBackend(2048, ...)`)
- Deployed: Tag `20261004-1240`, Rev `kosmos-20261004-1240-oc-18d15f56-reva-2f686b6`

### MOVE-Bug-Datei: Warum hatte sie einen Marker ohne funktionierendes .mpk?

Die MOVE-Bug-Datei (`2026-08-11 Harttig Tobias Krankmeldung.pdf`, NID
`cd288a88-3d00-459f-ae09-afef434f31a1`) hat:

- **348 Bytes** md/grant-Werte (24 Keys: doc.issuer, doc.subject, sender, etc.)
- **~948 Bytes** mdSize (Key + Value Summe, wie `SetMultiple` es berechnet)
- **1011 Bytes** .mpk (msgpack-Encoding mit Keys + Values + Header)
- **0 Bytes** oy.*, info.*, grant.*

**948 Bytes < 1024** → unter der alten Schwelle. Trotzdem hatte die Datei einen
Marker + .mpk.

**Erläuterung:** Die Datei wurde offloaded, als sie **früher** mehr Metadaten
hatte (>1KB). Später wurden Metadaten entfernt (via `SetArbitraryMetadata`) →
die Metadaten schrumpften unter 1KB, aber Marker + .mpk blieben.

**Kein De-Offload:** `SetMultiple` (hybrid_backend.go:256) prüft `mdSize > offloadLimit`
**nur** wenn `offloaded=false`. Einmal offloaded (Marker=1), werden alle weiteren
md/grant-Updates direkt in den .mpk geschrieben (Zeile 301-317), **ohne** zu
prüfen ob die Metadaten jetzt kleiner als das Limit sind. Es gibt keinen
"De-Offload"-Pfad — der Marker wird nie entfernt, die Metadaten bleiben im .mpk,
egal wie klein sie werden.

**Ablauf des 500-Fehlers (vor dem Fix):**

1. `crossSpaceMove` kopiert die Datei vom Quell-Space (`79f79149`, NID
   `cd288a88`) zum Ziel-Space (`5ac86946`)
2. Am Ziel wird eine **neue Node-ID** generiert (`uuid.New()` → `4963-44c3-...`)
3. `CopyMetadata` kopiert ALLE xattrs der Quelle (inkl. Marker) auf die
   Ziel-Datei
4. `SetMultiple` am Ziel:
   - liest Marker von der Ziel-Datei → `offloaded=true`
   - springt den Offload-Block **über** (weil `offloaded=true`)
   - **KEIN .mpk wird für die neue Node-ID `4963-44c3-...` angelegt**
5. `getAll` liest den Marker → versucht
   `.oc-nodes/07/a4/fa/a2/-4963-44c3-....mpk` zu lesen → **ENOENT** → 500

Der .mpk im Quell-Space (`cd288a88`) ist **irrelevant** — der MOVE erstellt eine
neue Datei mit neuer Node-ID im Ziel-Space. Das .mpk für die neue Node-ID wird
nie angelegt, weil der Marker das Offload unterdrückt.

**Der Fix** (Marker NICHT kopieren, commit `36aa6a608`):

- `SetMultiple` am Ziel sieht KEINEN Marker → `offloaded=false`
- berechnet `mdSize` (~948 Bytes)
- `948 <= 2048` (neues Limit) → **kein Offload**
- kein .mpk wird angelegt → kein ENOENT → **keine 500**
- Die Metadaten bleiben als xattrs auf der Ziel-Datei

Wenn `mdSize > 2048` wäre, würde `SetMultiple` korrekt offloaden (Marker + .mpk
für die neue Node-ID).
