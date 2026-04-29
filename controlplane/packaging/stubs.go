// Stub formatters for .deb, .rpm, .pkg, .msi.
//
// Each one is a real Formatter that returns a clear "not yet
// implemented" error. This keeps the registry surface complete
// (the UI can list the format and show a "coming soon" badge) and
// makes it a tiny PR to wire one up — replace the Build method with
// a real implementation, no other changes needed.
//
// Implementation notes for whoever picks these up:
//
//   .deb — ar archive containing debian-binary, control.tar.gz
//          (manifest + postinst), data.tar.gz (the payload). The
//          stdlib doesn't provide an `ar` writer; use a small inline
//          implementation (debian's spec is ~50 lines).
//
//   .rpm — RPM v3 lead + signature + header + cpio.gz body. Most
//          callers use rpm-build at runtime; for a pure-Go path,
//          github.com/google/rpmpack does the right thing.
//
//   .pkg — macOS flat package. Requires `pkgbuild` on the host or
//          a pure-Go xar writer; either is doable.
//
//   .msi — Windows installer. Best done with WiX toolset or a
//          pure-Go MSI writer (github.com/AlekSi/go-msi).

package packaging

import "errors"

func init() {
	Register(stubFormatter{name: "deb", suffix: ".deb", mime: "application/vnd.debian.binary-package"})
	Register(stubFormatter{name: "rpm", suffix: ".rpm", mime: "application/x-rpm"})
	Register(stubFormatter{name: "pkg", suffix: ".pkg", mime: "application/x-newton-compatible-pkg"})
	Register(stubFormatter{name: "msi", suffix: ".msi", mime: "application/x-msi"})
}

// stubFormatter is a placeholder until the format-specific writer
// lands. The UI shows it as registered; the API rejects build
// requests with a clear error.
type stubFormatter struct {
	name   string
	suffix string
	mime   string
}

func (s stubFormatter) Format() string      { return s.name }
func (s stubFormatter) Suffix() string      { return s.suffix }
func (s stubFormatter) ContentType() string { return s.mime }
func (s stubFormatter) Build(Payload) ([]byte, error) {
	return nil, errors.New("packaging: " + s.name + " formatter not yet implemented — use tar.gz for now")
}
