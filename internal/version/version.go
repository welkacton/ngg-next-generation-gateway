// Package version is the single source of truth for NGG's public release
// identity. It deliberately has no dependencies so every layer (engine, API,
// update checker, Web UI) can read the same generation / release string.
package version

const (
	// Generation is the product generation, not the semantic version.
	// Gen 1 = Stage 1~5 server platform.
	// Gen 2 = local deployment kit (VPS + Windows installer), minimal console,
	//         rule/rule-set enable controls and in-app repository updates.
	Generation = 2

	GenerationLabel = "Gen 2"
	GenerationName  = "第二代 · 本地部署与统一控制台"

	// Release is the product version shown in the UI and on GitHub.
	Release = "2.7.3"

	RepoOwner = "welkacton"
	RepoName  = "ngg-next-generation-gateway"
)

// Repo returns "owner/name".
func Repo() string { return RepoOwner + "/" + RepoName }
