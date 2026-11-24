The `make docs` target extracts the pinned prometheus/docs snapshot into the
gitignored `docs/` subdirectory, which is embedded into the binary. This file
keeps the `go:embed` pattern in `main.go` valid on a fresh clone.
