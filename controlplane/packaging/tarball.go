// tar.gz formatter — the v1 default. Universal across linux,
// darwin, and the BSDs; doesn't require any host-side packaging
// runtime to install.

package packaging

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"sort"
	"time"
)

func init() {
	Register(&tarballFormatter{})
}

type tarballFormatter struct{}

func (tarballFormatter) Format() string      { return "tar.gz" }
func (tarballFormatter) Suffix() string      { return ".tar.gz" }
func (tarballFormatter) ContentType() string { return "application/gzip" }

func (tarballFormatter) Build(p Payload) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	// Sort keys so the archive is reproducible — same input
	// produces the same bytes.
	keys := make([]string, 0, len(p.Files))
	for k := range p.Files {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	now := time.Now().UTC()
	for _, name := range keys {
		body := p.Files[name]
		mode := int64(0o644)
		switch {
		case name == "install.sh":
			mode = 0o755
		case name == p.PrimaryBinary:
			mode = 0o755
		case len(name) > 8 && name[:6] == "okesu-":
			// any okesu-<os>-<arch> binary
			mode = 0o755
		case name == "package-key.pem":
			mode = 0o600
		}
		if err := tw.WriteHeader(&tar.Header{
			Name:    name,
			Mode:    mode,
			Size:    int64(len(body)),
			ModTime: now,
		}); err != nil {
			return nil, fmt.Errorf("tar header %s: %w", name, err)
		}
		if _, err := tw.Write(body); err != nil {
			return nil, fmt.Errorf("tar body %s: %w", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
