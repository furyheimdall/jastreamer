package library

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	maximumArtworkPixels = 12_000_000
	maximumArtworkSide   = 600
)

func (service *Service) artworkFor(ctx context.Context, root Root, relative string, embedded []byte, embeddedMIME string) (string, error) {
	data, mime := embedded, embeddedMIME
	decoded, valid := decodeArtwork(data, mime)
	if !valid {
		var err error
		data, mime, err = folderArtwork(ctx, root.Path, filepath.ToSlash(filepath.Dir(filepath.FromSlash(relative))))
		if err != nil {
			return "", err
		}
		decoded, valid = decodeArtwork(data, mime)
	}
	if !valid {
		return "", nil
	}
	thumbnail := resizeArtwork(decoded, maximumArtworkSide)
	digest := sha256.Sum256(data)
	id := hex.EncodeToString(digest[:])
	name := id + ".jpg"
	finalPath := filepath.Join(service.cacheDir, name)
	if _, err := os.Lstat(finalPath); errors.Is(err, os.ErrNotExist) {
		temporary, createErr := os.CreateTemp(service.cacheDir, ".artwork-*.tmp")
		if createErr != nil {
			return "", fmt.Errorf("create artwork cache: %w", createErr)
		}
		temporaryName := temporary.Name()
		ok := false
		defer func() {
			temporary.Close()
			if !ok {
				os.Remove(temporaryName)
			}
		}()
		if err = temporary.Chmod(0o600); err == nil {
			err = jpeg.Encode(temporary, thumbnail, &jpeg.Options{Quality: 88})
		}
		if err == nil {
			err = temporary.Sync()
		}
		if closeErr := temporary.Close(); err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Rename(temporaryName, finalPath)
		}
		if err != nil {
			return "", fmt.Errorf("write artwork cache: %w", err)
		}
		ok = true
	} else if err != nil {
		return "", fmt.Errorf("inspect artwork cache: %w", err)
	}
	_, err := service.db.ExecContext(ctx, `INSERT INTO library_artwork(id,mime,cache_path,created_at) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET mime=excluded.mime,cache_path=excluded.cache_path`, id, "image/jpeg", name, timestamp(time.Now()))
	if err != nil {
		return "", fmt.Errorf("save artwork cache: %w", err)
	}
	return id, nil
}

func decodeArtwork(data []byte, mime string) (image.Image, bool) {
	if len(data) == 0 || mime == "" || len(data) > maximumArtworkBytes {
		return nil, false
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > maximumArtworkPixels {
		return nil, false
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	return decoded, err == nil
}

func folderArtwork(ctx context.Context, rootPath, folder string) ([]byte, string, error) {
	if folder == "." {
		folder = ""
	}
	candidates := []string{"cover.jpg", "cover.jpeg", "cover.png", "folder.jpg", "folder.jpeg", "folder.png", "Cover.jpg", "Cover.jpeg", "Cover.png", "Folder.jpg", "Folder.jpeg", "Folder.png", "cover.JPG", "cover.JPEG", "cover.PNG", "folder.JPG", "folder.JPEG", "folder.PNG"}
	for _, name := range candidates {
		relative := name
		if folder != "" {
			relative = filepath.ToSlash(filepath.Join(filepath.FromSlash(folder), name))
		}
		file, err := openRootFile(ctx, rootPath, relative)
		if err != nil {
			continue
		}
		info, statErr := file.Stat()
		if statErr != nil || info.Size() <= 0 || info.Size() > maximumArtworkBytes {
			file.Close()
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(file, maximumArtworkBytes+1))
		file.Close()
		if readErr != nil || len(data) == 0 || len(data) > maximumArtworkBytes {
			continue
		}
		mime := normalizeImageMIME("", data)
		if mime != "" {
			return data, mime, nil
		}
	}
	return nil, "", nil
}

func resizeArtwork(source image.Image, maximum int) image.Image {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= maximum && height <= maximum {
		return source
	}
	newWidth, newHeight := maximum, maximum
	if width >= height {
		newHeight = height * maximum / width
	} else {
		newWidth = width * maximum / height
	}
	if newWidth < 1 {
		newWidth = 1
	}
	if newHeight < 1 {
		newHeight = 1
	}
	target := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
	for y := range newHeight {
		sourceY := bounds.Min.Y + y*height/newHeight
		for x := range newWidth {
			target.Set(x, y, source.At(bounds.Min.X+x*width/newWidth, sourceY))
		}
	}
	return target
}

func (service *Service) cachedArtworkExists(id string) bool {
	if len(id) != 64 {
		return false
	}
	info, err := os.Lstat(filepath.Join(service.cacheDir, id+".jpg"))
	return err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 && info.Size() > 0
}

func (service *Service) Artwork(ctx context.Context, id string) (*os.File, string, error) {
	var mime, name string
	err := service.db.QueryRowContext(ctx, `SELECT mime,cache_path FROM library_artwork WHERE id=?`, id).Scan(&mime, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", notFound("artwork was not found")
	}
	if err != nil {
		return nil, "", fmt.Errorf("load artwork: %w", err)
	}
	if filepath.Base(name) != name || name != id+".jpg" {
		return nil, "", notFound("artwork was not found")
	}
	path := filepath.Join(service.cacheDir, name)
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 || before.Size() <= 0 {
		return nil, "", notFound("artwork was not found")
	}
	file, err := openCacheReadOnly(path)
	if err != nil {
		return nil, "", notFound("artwork was not found")
	}
	after, statErr := file.Stat()
	current, currentErr := os.Lstat(path)
	if statErr != nil || currentErr != nil || !after.Mode().IsRegular() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(before, after) || !os.SameFile(after, current) || after.Size() <= 0 {
		file.Close()
		return nil, "", notFound("artwork was not found")
	}
	return file, mime, nil
}
