package host

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

const (
	maxHostArchiveEntries       = 100_000
	maxHostArchiveExpandedBytes = 8 * 1024 * 1024 * 1024
)

type ExtractHostArchiveArgs struct {
	ArchivePath string
	Destination string
	Format      string
}

// ExtractHostArchive extracts a verified archive beneath the current user's
// home directory after rejecting absolute and traversal entries. The archive
// producer and its contents remain recipe-owned.
func (s *Service) ExtractHostArchive(args ExtractHostArchiveArgs, onData func(string)) (map[string]any, error) {
	if err := s.shared.RequireSharedHostOwner("extract_host_archive"); err != nil {
		return nil, err
	}
	format := strings.ToLower(strings.TrimSpace(args.Format))
	if format == "" {
		format = "tar.zst"
	}
	if format != "tar.zst" {
		return nil, fmt.Errorf("unsupported host archive format %q", format)
	}
	home, err := hostHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	archivePath, err := hostOwnedPath(home, args.ArchivePath)
	if err != nil {
		return nil, err
	}
	if info, statErr := os.Stat(archivePath); statErr != nil || !info.Mode().IsRegular() {
		if statErr == nil {
			statErr = errors.New("archive is not a regular file")
		}
		return nil, fmt.Errorf("inspect host archive: %w", statErr)
	}
	destination, err := hostOwnedPath(home, args.Destination)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return nil, fmt.Errorf("create host archive destination: %w", err)
	}
	if onData != nil {
		onData(fmt.Sprintf("Extracting verified host archive into %s...", destination))
	}
	entries, expandedBytes, err := extractZstdTar(archivePath, destination)
	if err != nil {
		return nil, fmt.Errorf("extract host archive: %w", err)
	}
	return map[string]any{"archivePath": archivePath, "destination": destination, "format": format, "entries": entries, "expandedBytes": expandedBytes, "changed": true}, nil
}

func extractZstdTar(archivePath, destination string) (int, int64, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return 0, 0, err
	}
	decompressor, err := zstd.NewReader(file)
	if err != nil {
		_ = file.Close()
		return 0, 0, err
	}
	defer decompressor.Close()
	defer file.Close()

	reader := tar.NewReader(decompressor)
	entries := 0
	var expandedBytes int64
	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return entries, expandedBytes, nextErr
		}
		entries++
		if entries > maxHostArchiveEntries {
			return entries, expandedBytes, fmt.Errorf("archive exceeds %d entries", maxHostArchiveEntries)
		}
		name, err := safeArchiveEntryName(header.Name)
		if err != nil {
			return entries, expandedBytes, err
		}
		target, err := safeArchiveTarget(destination, name)
		if err != nil {
			return entries, expandedBytes, err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := rejectExistingArchiveSymlink(target); err != nil {
				return entries, expandedBytes, err
			}
			if err := os.MkdirAll(target, os.FileMode(header.Mode)&0o777); err != nil {
				return entries, expandedBytes, err
			}
		case tar.TypeSymlink:
			linkTarget, err := safeArchiveLinkTarget(destination, target, header.Linkname)
			if err != nil {
				return entries, expandedBytes, err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return entries, expandedBytes, err
			}
			if existing, statErr := os.Lstat(target); statErr == nil {
				if existing.Mode()&os.ModeSymlink == 0 {
					return entries, expandedBytes, fmt.Errorf("archive symlink target %q already exists", name)
				}
				if err := os.Remove(target); err != nil {
					return entries, expandedBytes, err
				}
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return entries, expandedBytes, statErr
			}
			if err := os.Symlink(linkTarget, target); err != nil {
				return entries, expandedBytes, err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := rejectExistingArchiveSymlink(target); err != nil {
				return entries, expandedBytes, err
			}
			if header.Size < 0 || expandedBytes > maxHostArchiveExpandedBytes-header.Size {
				return entries, expandedBytes, fmt.Errorf("archive expanded content exceeds %d bytes", maxHostArchiveExpandedBytes)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return entries, expandedBytes, err
			}
			output, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode)&0o777)
			if err != nil {
				return entries, expandedBytes, err
			}
			written, copyErr := io.Copy(output, io.LimitReader(reader, header.Size+1))
			closeErr := output.Close()
			if copyErr != nil {
				return entries, expandedBytes, copyErr
			}
			if closeErr != nil {
				return entries, expandedBytes, closeErr
			}
			if written != header.Size {
				return entries, expandedBytes, fmt.Errorf("archive entry %q has truncated content", name)
			}
			expandedBytes += written
		default:
			return entries, expandedBytes, fmt.Errorf("archive entry %q has unsupported type %d", name, header.Typeflag)
		}
	}
	if entries == 0 {
		return 0, 0, errors.New("host archive is empty")
	}
	return entries, expandedBytes, nil
}

func safeArchiveEntryName(raw string) (string, error) {
	if raw == "" || strings.Contains(raw, `\`) || filepath.IsAbs(raw) {
		return "", fmt.Errorf("host archive contains unsafe entry %q", raw)
	}
	for _, part := range strings.Split(raw, "/") {
		if part == ".." {
			return "", fmt.Errorf("host archive contains unsafe entry %q", raw)
		}
	}
	cleaned := path.Clean(strings.ReplaceAll(raw, "\\", "/"))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("host archive contains unsafe entry %q", raw)
	}
	return cleaned, nil
}

func safeArchiveTarget(destination, name string) (string, error) {
	target := filepath.Join(destination, filepath.FromSlash(name))
	relative, err := filepath.Rel(destination, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("host archive entry escapes destination: %q", name)
	}
	if err := rejectArchiveSymlinkParents(destination, relative); err != nil {
		return "", err
	}
	return target, nil
}

func safeArchiveLinkTarget(destination, linkPath, raw string) (string, error) {
	if raw == "" || strings.Contains(raw, `\`) || filepath.IsAbs(raw) {
		return "", fmt.Errorf("host archive contains unsafe symlink %q -> %q", linkPath, raw)
	}
	for _, part := range strings.Split(raw, "/") {
		if part == ".." {
			return "", fmt.Errorf("host archive contains unsafe symlink %q -> %q", linkPath, raw)
		}
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(linkPath), filepath.FromSlash(raw)))
	relative, err := filepath.Rel(destination, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("host archive symlink escapes destination: %q -> %q", linkPath, raw)
	}
	return raw, nil
}

func rejectArchiveSymlinkParents(destination, relative string) error {
	current := destination
	parts := strings.Split(filepath.Dir(relative), string(filepath.Separator))
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return fmt.Errorf("inspect host archive parent %q: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("host archive path traverses existing symlink %q", current)
		}
	}
	return nil
}

func rejectExistingArchiveSymlink(target string) error {
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect host archive target %q: %w", target, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("host archive target is an existing symlink %q", target)
	}
	return nil
}
