package media

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenValidated_rejects_same_metadata_file_replacement_after_validation(t *testing.T) {
	// Given
	root := t.TempDir()
	catalogPath := filepath.Join(root, "track.flac")
	if err := os.WriteFile(catalogPath, []byte("catalog"), 0o600); err != nil {
		t.Fatal(err)
	}
	catalogInfo, err := os.Stat(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	identity := fileIdentity{size: catalogInfo.Size(), modifiedNS: catalogInfo.ModTime().UnixNano()}
	validatedPath, err := safeRegularPath(root, "track.flac", identity)
	if err != nil {
		t.Fatal(err)
	}
	replacementPath := filepath.Join(root, "replacement.flac")
	if err := os.WriteFile(replacementPath, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(replacementPath, catalogInfo.ModTime(), catalogInfo.ModTime()); err != nil {
		t.Fatal(err)
	}
	replacementInfo, err := os.Stat(replacementPath)
	if err != nil {
		t.Fatal(err)
	}
	if replacementInfo.Size() != identity.size || replacementInfo.ModTime().UnixNano() != identity.modifiedNS {
		t.Fatalf("fixture identities differ: catalog=%d/%d replacement=%d/%d", identity.size, identity.modifiedNS, replacementInfo.Size(), replacementInfo.ModTime().UnixNano())
	}
	if err := os.Remove(catalogPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacementPath, catalogPath); err != nil {
		t.Fatal(err)
	}

	// When
	file, openErr := openValidated(validatedPath, Claims{FileSize: identity.size, ModifiedNS: identity.modifiedNS})

	// Then
	if file == nil {
		if !errors.Is(openErr, ErrStaleFile) {
			t.Fatalf("open error = %v; want %v", openErr, ErrStaleFile)
		}
		return
	}
	defer file.Close()
	body, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Fatalf("open error = %v, body = %q; want %v and no replacement bytes", openErr, body, ErrStaleFile)
}

func TestOpenValidated_rejects_intermediate_symlink_replacement_after_validation(t *testing.T) {
	// Given
	root := t.TempDir()
	album := filepath.Join(root, "album")
	if err := os.Mkdir(album, 0o700); err != nil {
		t.Fatal(err)
	}
	catalogPath := filepath.Join(album, "track.flac")
	if err := os.WriteFile(catalogPath, []byte("catalog"), 0o600); err != nil {
		t.Fatal(err)
	}
	catalogInfo, err := os.Stat(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	outsideAlbum := t.TempDir()
	outsidePath := filepath.Join(outsideAlbum, "track.flac")
	if err := os.WriteFile(outsidePath, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(outsidePath, catalogInfo.ModTime(), catalogInfo.ModTime()); err != nil {
		t.Fatal(err)
	}
	outsideInfo, err := os.Stat(outsidePath)
	if err != nil {
		t.Fatal(err)
	}
	if outsideInfo.Size() != catalogInfo.Size() || outsideInfo.ModTime().UnixNano() != catalogInfo.ModTime().UnixNano() {
		t.Fatalf("fixture identities differ: catalog=%d/%d outside=%d/%d", catalogInfo.Size(), catalogInfo.ModTime().UnixNano(), outsideInfo.Size(), outsideInfo.ModTime().UnixNano())
	}
	identity := fileIdentity{size: catalogInfo.Size(), modifiedNS: catalogInfo.ModTime().UnixNano()}
	validatedPath, err := safeRegularPath(root, filepath.Join("album", "track.flac"), identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(album, filepath.Join(root, "validated-album")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideAlbum, album); err != nil {
		t.Fatal(err)
	}

	// When
	file, openErr := openValidated(validatedPath, Claims{FileSize: identity.size, ModifiedNS: identity.modifiedNS})

	// Then
	if file == nil {
		if !errors.Is(openErr, ErrUnsafePath) {
			t.Fatalf("open error = %v; want %v", openErr, ErrUnsafePath)
		}
		return
	}
	defer file.Close()
	body, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Fatalf("open error = %v, body = %q; want %v and no outside bytes", openErr, body, ErrUnsafePath)
}
