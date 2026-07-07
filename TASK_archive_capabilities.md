# Task: Archive Format Capabilities API

## Ziel
Das Web UI soll vom Server erfahren welche Archivformate browsbar sind,
statt eine hardcoded Liste zu pflegen.

## Aktueller Stand
- Reva archivefs: Opener-Registry (`RegisterOpener`), `CanHandle(name)` per Extension
- Web UI: hardcoded `BROWSABLE_ARCHIVE_EXTENSIONS` und `BROWSABLE_ARCHIVE_TYPES` in
  `useFileActionsNavigate.ts` — fehlt .7z, .jar, .war, .ear
- Kein API-Endpoint der die unterstützten Formate liefert

## Plan

### Phase 1: Reva — Extensions aus Opener-Registry exportieren

1. `Opener` Interface erweitern:
   ```go
   type Opener interface {
       Open(diskPath string) (fs.FS, io.Closer, error)
       CanHandle(name string) bool
       Extensions() []string  // z.B. [".zip", ".jar", ".war", ".ear"]
   }
   ```

2. `SupportedFormats()` Funktion in archivefs:
   ```go
   type ArchiveFormat struct {
       Extension string   // ".zip"
       MimeTypes []string // ["application/zip", "application/x-zip-compressed"]
   }
   func SupportedFormats() []ArchiveFormat
   ```

3. Jeder Opener definiert seine Extensions + MIME-Types:
   - zipOpener: `.zip`, `.jar`, `.war`, `.ear` → `application/zip`, `application/java-archive`
   - sevenzOpener: `.7z` → `application/x-7z-compressed`
   - diskfsOpener: `.iso`, `.img`, `.raw`, `.squashfs`, `.fat`, `.ext4`

### Phase 2: OpenCloud — Capabilities ausliefern

Option A: OCS Capabilities (einfacher)
- `GET /ocs/v2.php/cloud/capabilities` → `capabilities.files.browsable_archives`
- Wird beim Start aus Reva's `SupportedFormats()` befüllt

Option B: Graph API (sauberer)
- `GET /graph/v1.0/extensions/archives` → JSON mit Extensions + MIME-Types

### Phase 3: Web UI — dynamische Liste

1. `useFileActionsNavigate.ts`: `BROWSABLE_ARCHIVE_EXTENSIONS` und
   `BROWSABLE_ARCHIVE_TYPES` aus Capabilities laden statt hardcoded
2. Fallback auf aktuelle hardcoded Liste wenn Server keine Info liefert
