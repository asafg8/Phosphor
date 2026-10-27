package scan

// Bundle is the PHPCompatibility scan bundle built into this binary for its platform (bundle/, a copy of `make
// bundle`'s dist/scanbundle-<arch>.tar.zst; TestEmbeddedBundleMatchesDist). nil on a platform without one.
func Bundle() []byte { return bundle }
