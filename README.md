# Reva (KOSMOS Edition)

**Storage- und Orchestrierungsschicht des OpenCore-Stacks (OpenCloud + OpenCosmos).**

Reva verbindet Storage-, Sync- und Share-Plattformen über die
[CS3-APIs](https://github.com/cs3org/cs3apis) — vendor- und plattformneutral.

In der KOSMOS Edition (OpenCore) erweitert Reva das Upstream-Projekt um:

- **Immutable Spaces**: revisionssicherer, unveränderbarer Dateispeicher
- **Aktenzeichen-Metadaten**: xattrs-basierte Metadatenpflege
- **Scanner-Integration**: Anbindung an Scanserver
- **Erlaubnis-Modell**: erweitertes Rechtemodell für Verwaltungsbetrieb

## Build

``` console
make build
make test
```

## Technology

- **Storage**: Filesystem-basiert, keine Datenbank
- **APIs**: CS3 (gRPC) + WebDAV
- **Search**: Bleve (Metadaten) + Qdrant (semantisch, via opencloud)
- **Events**: NATS Jetstream

## License

[Apache 2.0](LICENSE)
