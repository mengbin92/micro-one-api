package data

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
	"github.com/stretchr/testify/require"
)

// Build the untrusted input directly: Seal correctly rejects these names and
// would otherwise prevent the tests from reaching Unseal's extraction boundary.
func encryptedArchiveHeaders(t *testing.T, key *age.X25519Identity, headers ...*tar.Header) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "untrusted.age")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	require.NoError(t, err)
	enc, err := age.Encrypt(f, key.Recipient())
	require.NoError(t, err)
	gz := gzip.NewWriter(enc)
	tw := tar.NewWriter(gz)
	for _, header := range headers {
		require.NoError(t, tw.WriteHeader(header))
		if header.Typeflag == tar.TypeReg {
			_, err = tw.Write(make([]byte, header.Size))
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	require.NoError(t, enc.Close())
	require.NoError(t, f.Close())
	return path
}

func TestUnsealRejectsUnsafeArchiveEntries(t *testing.T) {
	key, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	for _, name := range []string{
		"../escape", "nested/../../escape", "nested/../escape", "/escape",
		`..\escape`, `nested\escape`, "./escape", "nested//escape", ".",
	} {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			sentinel := filepath.Join(parent, "escape")
			require.NoError(t, os.WriteFile(sentinel, []byte("unchanged"), 0600))
			if name == "/escape" {
				name = sentinel // an absolute target we own, never a host file
			}
			archive := encryptedArchiveHeaders(t, key, &tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0600, Size: 1})
			_, err := Unseal(archive, key.String(), filepath.Join(parent, "restored"), 1<<20)
			require.Error(t, err)
			got, err := os.ReadFile(sentinel)
			require.NoError(t, err)
			require.Equal(t, "unchanged", string(got))
			entries, err := os.ReadDir(filepath.Join(parent, "restored"))
			require.NoError(t, err)
			require.Empty(t, entries, "reject the entry before creating files or directories")
		})
	}
}

func TestUnsealRejectsArchiveLinksAndDuplicates(t *testing.T) {
	key, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	for _, kind := range []byte{tar.TypeSymlink, tar.TypeLink, tar.TypeDir} {
		t.Run(string(kind), func(t *testing.T) {
			archive := encryptedArchiveHeaders(t, key, &tar.Header{Name: "nested", Linkname: "../outside", Typeflag: kind, Mode: 0700})
			_, err := Unseal(archive, key.String(), filepath.Join(t.TempDir(), "restored"), 1<<20)
			require.Error(t, err)
		})
	}
	header := &tar.Header{Name: "mysql.sql", Typeflag: tar.TypeReg, Mode: 0600, Size: 1}
	archive := encryptedArchiveHeaders(t, key, header, header)
	_, err = Unseal(archive, key.String(), filepath.Join(t.TempDir(), "restored"), 1<<20)
	require.ErrorContains(t, err, "invalid or oversized backup archive")
}
