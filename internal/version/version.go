// Package version centralises the launcher's version string and ASCII
// banner so the bootstrap, GUI, and updater read from the same source.
package version

// Name is the human-facing product name. Used in the updater's
// user-agent and CLI help text.
const Name = "PrAImate"

// Current is the build's semantic version. Bump on every release; the
// updater compares this against the latest GitHub release tag to decide
// whether an update is available.
//
// Declared as a var (not a const) so scripts/build.{sh,ps1} can
// override it at link time with `-ldflags "-X .../internal/version.Current=X.Y.Z"`.
// The literal here is the fallback when nothing is injected (e.g.
// `go run`, `go install`, or `go build` without our scripts).
var Current = "1.3.5"

// ForgeBaseURL is the canonical GitHub web URL for this repository.
const ForgeBaseURL = "https://github.com"

// Repo is the GitHub owner/repository path the updater queries for releases.
const Repo = "sPROFFEs/praimate"

// RepoURL is the canonical browser URL for the repository.
const RepoURL = ForgeBaseURL + "/" + Repo

// RepoCloneURL is the canonical HTTPS clone URL.
const RepoCloneURL = RepoURL + ".git"

// ReleaseLatestAPIURL is the GitHub API endpoint for the latest release.
const ReleaseLatestAPIURL = "https://api.github.com/repos/" + Repo + "/releases/latest"

// Banner is the PRAIMATE wordmark with the monkey mascot on the left.
// Rendered on -version and mirrored by the boot splash
// (cmd/praimate/screen_splash.go keeps its own copy because it
// animates per-row; update both together).
const Banner = `   .-"-.     ██████╗ ██████╗  █████╗ ██╗███╗   ███╗ █████╗ ████████╗███████╗
  /|6 6|\    ██╔══██╗██╔══██╗██╔══██╗██║████╗ ████║██╔══██╗╚══██╔══╝██╔════╝
 {/(_0_)\}   ██████╔╝██████╔╝███████║██║██╔████╔██║███████║   ██║   █████╗
  _/ ^ \_    ██╔═══╝ ██╔══██╗██╔══██║██║██║╚██╔╝██║██╔══██║   ██║   ██╔══╝
 (/ /^\ \)   ██║     ██║  ██║██║  ██║██║██║ ╚═╝ ██║██║  ██║   ██║   ███████╗
  ""' '""    ╚═╝     ╚═╝  ╚═╝╚═╝  ╚═╝╚═╝╚═╝     ╚═╝╚═╝  ╚═╝   ╚═╝   ╚══════╝

                  one harness, every agent — workflows & MCP`
