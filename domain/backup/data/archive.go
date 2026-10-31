package data

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"filippo.io/age"
	"fmt"
	"io"
	"micro-one-api/pkg/jsonx"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type FileRecord struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}
type Manifest struct {
	Format         int               `json:"format"`
	Project        string            `json:"project"`
	JobID          string            `json:"job_id"`
	SnapshotAt     int64             `json:"snapshot_at"`
	SourceRevision string            `json:"source_revision"`
	Schemas        []string          `json:"schemas"`
	KeyReferences  []string          `json:"key_references"`
	Images         map[string]string `json:"images"`
	Files          []FileRecord      `json:"files"`
}

func fileDigest(path string) (string, int64, error) {
	f, err := os.Open(path) // #nosec G304 -- operator-configured worker file (config inventory or sha-verified artifact) read by the private host worker.
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return fmt.Sprintf("%x", h.Sum(nil)), n, err
}
func safeName(s string) bool {
	return s != "" && !strings.Contains(s, "\\") && !strings.HasPrefix(s, "/") && filepath.ToSlash(filepath.Clean(s)) == s && s != ".." && !strings.HasPrefix(s, "../")
}

// Seal streams a private tar.gz through age's authenticated streaming format.
// The worker needs only a public recipient. Decryption identities stay offline.
func Seal(output, recipient string, m Manifest, files map[string]string) error {
	r, err := age.ParseX25519Recipient(recipient)
	if err != nil {
		return fmt.Errorf("invalid age recipient")
	}
	names := make([]string, 0, len(files))
	for name := range files {
		if !safeName(name) || name == "manifest.json" {
			return fmt.Errorf("unsafe archive name")
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		info, e := os.Lstat(files[name])
		if e != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("regular backup input required")
		}
		hash, n, e := fileDigest(files[name])
		if e != nil {
			return e
		}
		m.Files = append(m.Files, FileRecord{Name: name, SHA256: hash, Bytes: n})
	}
	mf, err := jsonx.Marshal(m)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600) // #nosec G304 -- operator-configured artifact output path on the private host worker.
	if err != nil {
		return err
	}
	defer f.Close()
	enc, err := age.Encrypt(f, r)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(enc)
	tw := tar.NewWriter(gz)
	if err = tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0600, Size: int64(len(mf))}); err != nil {
		return err
	}
	if _, err = tw.Write(mf); err != nil {
		return err
	}
	for _, name := range names {
		source, e := os.Open(files[name])
		if e != nil {
			return e
		}
		info, e := source.Stat()
		if e == nil {
			e = tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: info.Size()})
		}
		if e == nil {
			_, e = io.Copy(tw, source)
		}
		source.Close()
		if e != nil {
			return e
		}
	}
	if err = tw.Close(); err != nil {
		return err
	}
	if err = gz.Close(); err != nil {
		return err
	}
	if err = enc.Close(); err != nil {
		return err
	}
	return f.Sync()
}

// Unseal requires a fresh extraction directory and validates every payload
// digest before the restore command can open its target database.
func Unseal(input, identity, dir string, maxBytes int64) (Manifest, error) {
	var m Manifest
	key, err := age.ParseX25519Identity(strings.TrimSpace(identity))
	if err != nil {
		return m, fmt.Errorf("invalid age identity")
	}
	f, err := os.Open(input) // #nosec G304 -- operator-configured artifact path; content digest is verified against the receipt before extraction.
	if err != nil {
		return m, err
	}
	defer f.Close()
	dec, err := age.Decrypt(f, key)
	if err != nil {
		return m, err
	}
	gz, err := gzip.NewReader(dec)
	if err != nil {
		return m, err
	}
	defer gz.Close()
	if err = os.Mkdir(dir, 0700); err != nil {
		return m, err
	}
	tr := tar.NewReader(gz)
	seen := map[string]bool{}
	var total int64
	for {
		hdr, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return m, e
		}
		if !safeName(hdr.Name) || hdr.Typeflag != tar.TypeReg || seen[hdr.Name] || len(seen) >= 10000 || hdr.Size < 0 || hdr.Size > maxBytes-total {
			return m, fmt.Errorf("invalid or oversized backup archive")
		}
		seen[hdr.Name] = true
		total += hdr.Size
		if hdr.Name == "manifest.json" {
			if hdr.Size > 1024*1024 {
				return m, fmt.Errorf("oversized manifest")
			}
			raw, e := io.ReadAll(tr)
			if e != nil {
				return m, e
			}
			if e = jsonx.Unmarshal(raw, &m); e != nil {
				return m, e
			}
			continue
		}
		path := filepath.Join(dir, hdr.Name) // #nosec G305 -- hdr.Name passed safeName validation above (relative, clean, no dot-dot, no absolutes).
		if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			return m, e
		}
		out, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600) // #nosec G304 -- path joins the fresh scratch dir with a safeName-validated entry name.
		if e != nil {
			return m, e
		}
		_, e = io.Copy(out, tr) // #nosec G110 -- per-entry and cumulative byte bounds are enforced above; the artifact SHA-256 is verified before decryption.
		ce := out.Close()
		if e != nil {
			return m, e
		}
		if ce != nil {
			return m, ce
		}
	}
	// Drain both layers: tar's end blocks alone do not authenticate the age EOF.
	if _, err = io.Copy(io.Discard, gz); err != nil { // #nosec G110 -- drains the sha-verified, byte-bounded gzip stream to authenticate the age EOF.
		return m, err
	}
	if _, err = io.Copy(io.Discard, dec); err != nil {
		return m, err
	}
	if m.Format != 1 || !ident.MatchString(m.JobID) || !ident.MatchString(m.Project) || len(m.Schemas) == 0 || len(m.Files)+1 != len(seen) {
		return m, fmt.Errorf("incomplete manifest")
	}
	checked := map[string]bool{}
	for _, rec := range m.Files {
		if !safeName(rec.Name) || rec.Name == "manifest.json" || !seen[rec.Name] || checked[rec.Name] {
			return m, fmt.Errorf("manifest file mismatch")
		}
		checked[rec.Name] = true
		hash, n, e := fileDigest(filepath.Join(dir, rec.Name))
		if e != nil || hash != rec.SHA256 || n != rec.Bytes {
			return m, fmt.Errorf("backup checksum mismatch")
		}
	}
	if !checked["mysql.sql"] || !checked["redis.rdb"] || !checked["config/compose.json"] {
		return m, fmt.Errorf("missing recovery payload")
	}
	return m, nil
}
