package proxy

// New returns the starting policy for a preset.
//
// A preset's own allowances are not copied into Rules. They stay behind the
// preset, so Rules holds only what a person added — which is what makes
// switching preset mean something. Materialised, a switch would carry the old
// preset's whole list forward and change nothing at all.
func New(preset Preset) Policy { return Policy{Preset: preset} }

// Allows reports whether a preset permits a target on its own, and which of its
// entries said so.
func (p Preset) Allows(t Target) (pattern string, ok bool) {
	if p != PresetBalanced {
		// open is handled as a default rather than a list, and locked-down
		// allows nothing until told to.
		return "", false
	}
	for _, entry := range balanced {
		if Matches(entry, t) {
			return entry, true
		}
	}
	return "", false
}

// Allowances lists what a preset permits on its own, for display.
func (p Preset) Allowances() []string {
	if p != PresetBalanced {
		return nil
	}
	return balanced
}

// balanced is what the balanced preset allows: the places an agent has to
// reach to do ordinary work, and nothing else.
//
// Both the apex and the wildcard are listed wherever a service uses both,
// because "*.example.com" deliberately does not cover "example.com".
//
// Ports are left off throughout. Pinning :443 would be tighter, but every one
// of these is also fetched over :80 somewhere — apt and alpine repositories
// plainly, redirects to https elsewhere — and a policy that breaks `apt update`
// is one people turn off rather than narrow.
var balanced = []string{
	// model providers, and the vendors' own endpoints beside them. An agent
	// talks to more than the model API — it signs in, checks what it is
	// entitled to, and looks for its own updates — and a preset that allows
	// only the API is one the agent will not start under.
	//
	// locked-down is the preset that refuses these.
	"api.anthropic.com",
	"anthropic.com", "*.anthropic.com",
	"claude.com", "*.claude.com",
	"api.openai.com",
	"openai.com", "*.openai.com",
	"generativelanguage.googleapis.com",

	// source hosting, and the separate hosts git and release downloads use.
	"github.com", "*.github.com",
	"codeload.github.com",
	"raw.githubusercontent.com",
	"objects.githubusercontent.com",
	"gitlab.com", "*.gitlab.com",
	"bitbucket.org", "*.bitbucket.org",

	// javascript
	"registry.npmjs.org", "*.npmjs.org",
	"registry.yarnpkg.com",
	"nodejs.org",

	// python
	"pypi.org", "*.pypi.org",
	"files.pythonhosted.org",

	// go
	"proxy.golang.org",
	"sum.golang.org",
	"storage.googleapis.com",

	// rust
	"crates.io", "*.crates.io",
	"static.crates.io",
	"sh.rustup.rs",

	// ruby, php, java
	"rubygems.org", "*.rubygems.org",
	"packagist.org", "*.packagist.org",
	"repo.maven.apache.org",
	"repo1.maven.org",

	// linux distributions, for `apt install` and `apk add`
	"deb.debian.org",
	"security.debian.org",
	"archive.ubuntu.com",
	"security.ubuntu.com",
	"ports.ubuntu.com",
	"dl-cdn.alpinelinux.org",

	// editors attaching to a sandbox, and the CDNs they pull from. An editor
	// installs its own server into the sandbox on first attach, so these are
	// what stands between `balanced` and a remote-ssh session that hangs.
	//
	// Telemetry is deliberately absent. mobile.events.data.microsoft.com is
	// what VS Code reaches for next and it stays denied: the preset allows the
	// work, not the reporting of it, and nothing breaks without it.
	"update.code.visualstudio.com",
	"vscode.download.prss.microsoft.com",
	"marketplace.visualstudio.com",
	"*.vsassets.io",
	"*.vscode-cdn.net",
	"*.blob.core.windows.net",
}
