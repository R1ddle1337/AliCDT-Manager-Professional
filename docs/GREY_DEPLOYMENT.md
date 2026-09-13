# Grey Deployment Record (Archived)

This document is retained only as a short record of the completed Go-platform
migration. The former FastAPI/Python service and its root Docker Compose stack
are no longer part of the repository or the supported runtime.

The production service is now the Go controller defined by
[`deploy/docker-compose.go.production.yml`](../deploy/docker-compose.go.production.yml).
Relay configuration and cloud data remain in the existing SQLite database under
`/app/alicdt-manager/data`; updates are performed by
[`deploy/alicdt-manager-update.sh`](../deploy/alicdt-manager-update.sh), which
backs up the database before switching the image.

For current development and deployment instructions, see
[`GO_REFACTOR.md`](GO_REFACTOR.md) and
[`PRODUCTION_CUTOVER.md`](PRODUCTION_CUTOVER.md). Do not try to start or restore
the removed Python container.
