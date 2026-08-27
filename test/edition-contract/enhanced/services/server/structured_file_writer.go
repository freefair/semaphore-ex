package server

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type safeRotatingFileConfig struct {
	filename         string
	maxSizeMegabytes int
	maxAgeDays       int
	maxBackups       int
	compress         bool
	localTime        bool
	rotationInterval time.Duration
}

type safeRotatingFile struct {
	config   safeRotatingFileConfig
	file     *os.File
	size     int64
	openedAt time.Time
	now      func() time.Time
	mu       sync.Mutex
}

func newSafeRotatingFile(config safeRotatingFileConfig) (*safeRotatingFile, error) {
	if err := validateStructuredLogPath(config.filename); err != nil {
		return nil, err
	}
	writer := &safeRotatingFile{config: config, now: time.Now}
	if err := writer.open(); err != nil {
		return nil, err
	}
	return writer, nil
}

func validateStructuredLogPath(filename string) error {
	if filename == "" {
		return errors.New("destination path is empty")
	}
	if !filepath.IsAbs(filename) {
		return errors.New("destination path must be absolute")
	}
	if filepath.Clean(filename) != filename {
		return errors.New("destination path must not contain traversal or redundant components")
	}
	if err := rejectSymlinkComponents(filename); err != nil {
		return err
	}
	if info, err := os.Lstat(filename); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("destination must not be a symlink")
		}
		if !info.Mode().IsRegular() {
			return errors.New("destination must be a regular file")
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect destination: %w", err)
	}
	return nil
}

func rejectSymlinkComponents(filename string) error {
	current := filepath.Clean(filename)
	components := []string{}
	for {
		components = append(components, current)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	for index := len(components) - 1; index >= 0; index-- {
		info, err := os.Lstat(components[index])
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect destination path: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("destination path contains symlink %q", components[index])
		}
	}
	return nil
}

func (w *safeRotatingFile) open() error {
	parent := filepath.Dir(w.config.filename)
	if err := rejectSymlinkComponents(w.config.filename); err != nil {
		return err
	}
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}
	if err := validateStructuredLogPath(w.config.filename); err != nil {
		return err
	}
	if err := repairTrailingPartialLine(w.config.filename); err != nil {
		return err
	}
	file, err := os.OpenFile(w.config.filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open destination: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return fmt.Errorf("restrict destination permissions: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return fmt.Errorf("stat destination: %w", err)
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return errors.New("destination must be a regular file")
	}
	w.file = file
	w.size = info.Size()
	w.openedAt = w.now()
	return nil
}

func repairTrailingPartialLine(filename string) error {
	file, err := os.OpenFile(filename, os.O_RDWR, 0o600)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open destination for tail repair: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		return err
	}
	last := []byte{0}
	if _, err := file.ReadAt(last, info.Size()-1); err != nil {
		return fmt.Errorf("inspect destination tail: %w", err)
	}
	if last[0] == '\n' {
		return nil
	}

	const chunkSize int64 = 4096
	end := info.Size()
	buffer := make([]byte, chunkSize)
	for end > 0 {
		start := end - chunkSize
		if start < 0 {
			start = 0
		}
		length := int(end - start)
		if _, err := file.ReadAt(buffer[:length], start); err != nil && err != io.EOF {
			return fmt.Errorf("scan destination tail: %w", err)
		}
		if index := strings.LastIndexByte(string(buffer[:length]), '\n'); index >= 0 {
			if err := file.Truncate(start + int64(index) + 1); err != nil {
				return fmt.Errorf("repair destination tail: %w", err)
			}
			return nil
		}
		end = start
	}
	if err := file.Truncate(0); err != nil {
		return fmt.Errorf("repair destination tail: %w", err)
	}
	return nil
}

func (w *safeRotatingFile) WriteLine(line []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(line) == 0 || line[len(line)-1] != '\n' {
		return errors.New("structured log record must end with a newline")
	}
	if w.file == nil {
		return errors.New("destination is not open")
	}
	if w.shouldRotate(int64(len(line))) {
		if err := w.rotate(); err != nil {
			return err
		}
	}
	written, err := w.file.Write(line)
	w.size += int64(written)
	if err != nil {
		return fmt.Errorf("write destination: %w", err)
	}
	if written != len(line) {
		return io.ErrShortWrite
	}
	return nil
}

func (w *safeRotatingFile) shouldRotate(nextBytes int64) bool {
	if w.config.maxSizeMegabytes > 0 && w.size > 0 && w.size+nextBytes > int64(w.config.maxSizeMegabytes)*1024*1024 {
		return true
	}
	return w.config.rotationInterval > 0 && w.size > 0 && w.now().Sub(w.openedAt) >= w.config.rotationInterval
}

func (w *safeRotatingFile) rotate() error {
	if err := w.file.Sync(); err != nil {
		return fmt.Errorf("sync destination before rotation: %w", err)
	}
	if err := w.file.Close(); err != nil {
		return fmt.Errorf("close destination before rotation: %w", err)
	}
	w.file = nil
	stampTime := w.now()
	if !w.config.localTime {
		stampTime = stampTime.UTC()
	}
	baseRotated := fmt.Sprintf("%s.%s", w.config.filename, stampTime.Format("20060102T150405.000000000Z0700"))
	rotated := baseRotated
	for sequence := 1; ; sequence++ {
		_, err := os.Lstat(rotated)
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			reopenErr := w.open()
			return errors.Join(fmt.Errorf("inspect rotated destination: %w", err), reopenErr)
		}
		rotated = fmt.Sprintf("%s.%d", baseRotated, sequence)
	}
	if err := os.Rename(w.config.filename, rotated); err != nil {
		_ = w.open()
		return fmt.Errorf("rotate destination: %w", err)
	}

	var postRotationErr error
	if w.config.compress {
		if err := compressRotatedFile(rotated); err != nil {
			postRotationErr = errors.Join(postRotationErr, err)
		}
	}
	if err := w.applyRetention(); err != nil {
		postRotationErr = errors.Join(postRotationErr, err)
	}
	if err := w.open(); err != nil {
		return errors.Join(postRotationErr, err)
	}
	return postRotationErr
}

func compressRotatedFile(filename string) error {
	input, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("open rotated destination: %w", err)
	}
	outputName := filename + ".gz"
	output, err := os.OpenFile(outputName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		_ = input.Close()
		return fmt.Errorf("create compressed destination: %w", err)
	}
	compressor := gzip.NewWriter(output)
	_, copyErr := io.Copy(compressor, input)
	closeCompressorErr := compressor.Close()
	closeOutputErr := output.Close()
	closeInputErr := input.Close()
	if err := errors.Join(copyErr, closeCompressorErr, closeOutputErr, closeInputErr); err != nil {
		_ = os.Remove(outputName)
		return fmt.Errorf("compress rotated destination: %w", err)
	}
	if err := os.Remove(filename); err != nil {
		return fmt.Errorf("remove uncompressed rotated destination: %w", err)
	}
	return nil
}

func (w *safeRotatingFile) applyRetention() error {
	directory := filepath.Dir(w.config.filename)
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("list rotated destinations: %w", err)
	}
	type retainedFile struct {
		path    string
		modTime time.Time
	}
	retained := make([]retainedFile, 0, len(entries))
	prefix := filepath.Base(w.config.filename) + "."
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return fmt.Errorf("inspect rotated destination: %w", statErr)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if w.config.maxAgeDays > 0 && w.now().Sub(info.ModTime()) > time.Duration(w.config.maxAgeDays)*24*time.Hour {
			if removeErr := os.Remove(path); removeErr != nil {
				return fmt.Errorf("remove expired destination: %w", removeErr)
			}
			continue
		}
		retained = append(retained, retainedFile{path: path, modTime: info.ModTime()})
	}
	sort.Slice(retained, func(i, j int) bool { return retained[i].modTime.After(retained[j].modTime) })
	if w.config.maxBackups > 0 && len(retained) > w.config.maxBackups {
		for _, expired := range retained[w.config.maxBackups:] {
			if err := os.Remove(expired.path); err != nil {
				return fmt.Errorf("remove excess destination: %w", err)
			}
		}
	}
	return nil
}

func (w *safeRotatingFile) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return errors.New("destination is not open")
	}
	if err := w.file.Sync(); err != nil {
		return fmt.Errorf("sync destination: %w", err)
	}
	return nil
}

func (w *safeRotatingFile) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}
