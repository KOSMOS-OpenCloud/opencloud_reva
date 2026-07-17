# Subspace Security Review — Änderungen in permissions.go

## Zusammenfassung

Subspace unterbricht die Permission-Vererbung an einem markierten Ordner.
Die Änderung betrifft ausschließlich `assemblePermissions()` in
`node/permissions.go` — die zentrale Zugriffskontrolle für ALLE Operationen.

## Code-Änderungen

### permissions.go — 4 Eingriffspunkte

**1. Zeile 153 (vor dem Loop): Subspace-Liste laden**
```go
subspaces := GetSubspaceList(ctx, rn)
```
- Gecacht per Space (sync.RWMutex), kein I/O bei Folgeaufrufen
- Leere Liste = nil → alle weiteren Checks sind No-Ops
- **Risiko:** Gering. Nur ein Funktionsaufruf, kein Einfluss auf Logik.

**2. Zeile 176-180 (im Loop, nach AddPermissions): Subspace-Stopp**
```go
if IsSubspaceID(cn.ID, subspaces) {
    break
}
```
- Prüft ob der aktuelle Node ein Subspace ist
- Wenn ja: Walk endet hier, Root-Grants werden NICHT addiert
- `IsSubspaceID` ist O(n) über die Subspace-Liste (typisch 0-5 Einträge)
- **Risiko:** MITTEL. Ein falscher Match würde Root-Grants abschneiden.
  Absicherung: Bei leerer Liste ist der Check immer false → kein Break → wie bisher.

**3. Zeile 196-203 (nach Loop): Root-Grant nur wenn Root erreicht**
```go
if cn.ID == rn.ID {
    // Root-Grants addieren (wie bisher)
}
```
- Vorher: Root-Grants wurden immer addiert
- Jetzt: Nur wenn der Walk bis zum Root durchgelaufen ist
- Wenn bei Subspace gestoppt wurde (`break`), ist `cn.ID != rn.ID` → Root-Grants werden übersprungen
- **Risiko:** MITTEL. Gleiche Bedingung wie beim Break — konsistent.

**4. Zeile 206-219 (neu): Listing für Subspace-only User**
```go
if isPermissionsEmpty(ap) && len(subspaces) > 0 {
    if userHasSubspaceGrant(ctx, rn, subspaces, u) {
        AddPermissions(ap, &provider.ResourcePermissions{
            Stat: true, GetPath: true, ListContainer: true,
        })
    }
}
```
- Greift NUR wenn `ap` komplett leer ist (User hat keine Rechte aus dem Walk)
- UND Subspaces existieren
- UND User hat Grant in einem Subspace
- Gibt nur Listing (Stat, GetPath, ListContainer) — kein Read, Write, Delete
- **Risiko:** GERING. Kann nur Rechte hinzufügen, nie wegnehmen.
  Worst case: User bekommt Listing obwohl er es nicht sollte → sieht Dateinamen, kann aber nichts öffnen.
- **TODO:** `userHasSubspaceGrant` gibt aktuell immer `true` zurück — muss korrekt implementiert werden.

### Neue Hilfsfunktionen (permissions.go)

**`isPermissionsEmpty()`**
- Vergleicht `*ResourcePermissions` mit leerem Struct
- Kein Seiteneffekt, rein lesend

**`userHasSubspaceGrant()`**
- **TODO:** Aktuell Stub (`return true`)
- Muss pro Subspace-Node die Grants des Users prüfen
- Darf nur bei leeren Permissions aufgerufen werden (Performance)

### Neue Datei: subspace.go

| Funktion | Zweck | I/O |
|---|---|---|
| `GetSubspaceList()` | Liest xattr, gecacht | 1x xattr pro Space |
| `SetSubspaceList()` | Schreibt xattr, invalidiert Cache | 1x xattr |
| `AddSubspace()` | Fügt Eintrag hinzu | Liest + Schreibt |
| `RemoveSubspace()` | Entfernt Eintrag | Liest + Schreibt |
| `IsSubspaceID()` | O(n) Loop über Liste | Kein I/O |
| `IsAncestorOfSubspace()` | Prefix-Check auf Pfaden | Kein I/O |
| `InvalidateSubspaceCache()` | Cache leeren | Kein I/O |

Cache: `sync.RWMutex` geschützt, `map[spaceID][]SubspaceEntry`.

### prefixes.go

```go
SubspacesAttr string = OcPrefix + "subspaces"
```
Ein neues xattr. Kein Einfluss auf bestehende Attribute.

## Was sich NICHT ändert

| Komponente | Status |
|---|---|
| `ReadUserPermissions()` | Unangetastet — Grants pro Node bleiben identisch |
| `AddPermissions()` | Unangetastet — additive Logik unverändert |
| Owner-Override (Zeile 222) | Unangetastet — Owner bekommt immer OwnerPermissions |
| Service-Account-Override (Zeile 228) | Unangetastet — Service-Accounts unverändert |
| Deny-Logik (Zeile 168) | Unangetastet — accessDenied return vor AddPermissions |
| Grant-Speicherung (grants.go) | Unangetastet — Grants werden normal gelesen/geschrieben |

## Regressions-Garantie

**Leere Subspace-Liste → ZERO Verhaltensänderung:**
1. `GetSubspaceList()` → nil (kein xattr gesetzt)
2. `IsSubspaceID(cn.ID, nil)` → false → kein Break
3. Loop läuft komplett durch → `cn.ID == rn.ID` → Root-Grants werden addiert
4. `len(subspaces) > 0` → false → Listing-Check wird übersprungen
5. Ergebnis: identisch zum Code ohne Subspace-Änderung

## Offene Punkte

1. **`userHasSubspaceGrant()` ist ein Stub** — gibt immer true zurück.
   Muss korrekt implementiert werden bevor Subspaces produktiv gehen.
   Ohne korrekte Implementierung: User ohne Subspace-Grant bekommt
   fälschlich Listing (sieht Dateinamen, kann nichts öffnen).

2. **`IsSubspaceID()` ist O(n)** — bei vielen Subspaces pro Space (>50)
   könnte das spürbar werden. Dann auf Map umstellen.

3. **Cache-Invalidierung** — `InvalidateSubspaceCache()` wird bei
   `SetSubspaceList()` aufgerufen. Bei Cluster-Betrieb (mehrere reva-Instanzen)
   muss die Cache-Invalidierung über Events propagiert werden.

4. **Verschachtelte Subspaces** — nicht explizit behandelt. Der innerste
   Subspace gewinnt (erster Match im Walk von Blatt → Root). Das ist
   korrekt, aber sollte getestet werden.
