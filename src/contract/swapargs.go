package contract

// PreflightArgs is the manifest Args["preflight"] for sub=preflight: the built prefix and the runtimes that
// will move onto it. Preflight changes nothing on the host (temporary configs only).
type PreflightArgs struct {
	// Prefix is build.json.prefix (/opt/phosphor/php/<version>-<shape8>).
	Prefix     string           `json:"prefix"`
	NewVersion string           `json:"new_version"`
	Host       HostFacts        `json:"host"`
	Runtimes   []SwapRuntimeArg `json:"runtimes"`
	// LabSkip are --lab-skip-preflight check ids (refused unless manifest.lab).
	LabSkip []string `json:"lab_skip,omitempty"`
}

// SwapArgs is the manifest Args["swap"] for sub=swap: one swap document per host, every eligible runtime
// swapped onto Prefix, consumers restarted, packages held.
type SwapArgs struct {
	Prefix     string           `json:"prefix"`
	NewVersion string           `json:"new_version"`
	Host       HostFacts        `json:"host"`
	Runtimes   []SwapRuntimeArg `json:"runtimes"`
	Consumers  []Consumer       `json:"consumers"`
	// UndoCmd is the exact CLI undo for this asset (swap.json.undo_cmd).
	UndoCmd string `json:"undo_cmd"`
	// InstallModel: prefix (default) or package-replace (distro-source debs in <prefix>/debs replace the
	// installed packages; originals cached on the host in BackupSlots before any change).
	InstallModel InstallModel `json:"install_model,omitempty"`
	BackupSlots  []string     `json:"backup_slots,omitempty"`
	// DistroKeys are the pinned archive keyrings for fetching originals the host mirror no longer serves.
	DistroKeys []BuildSource `json:"distro_keys,omitempty"`
}

// SwapRuntimeArg is one runtime from fingerprint.json plus its plan action.
type SwapRuntimeArg struct {
	RuntimeID string  `json:"runtime_id"`
	Action    Action  `json:"action"`
	Runtime   Runtime `json:"runtime"`
}
