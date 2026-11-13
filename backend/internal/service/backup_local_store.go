package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LocalBackupStore 把备份对象写到本机目录，作为 S3 之外的第二个备份目标。
//
// 设计约束：
//   - 只实现 BackupObjectStore 的既有语义，分卷、保留策略、记录管理全部复用
//     BackupService 的原路径，不产生第二套备份流程。
//   - key 映射为 root 下的相对路径；绝对路径与 ".." 一律拒绝，配置写错也不会
//     写到 root 之外。
//   - 上传走同目录临时文件 + fsync + rename：任何时刻目录里要么是完整归档，
//     要么是不带目标名的 .part 残留，后者不会被当成可用备份。
//   - PresignURL 在本地目标没有意义，返回明确错误，让下载入口报错而不是给出
//     一个打不开的链接。
type LocalBackupStore struct {
	root string
}

// NewLocalBackupStore 创建本地目录后端。root 必须已存在、是目录且可写；
// 存在性由配置保存阶段校验，这里再兜一次底，避免配置被外部改坏后静默丢备份。
func NewLocalBackupStore(root string) (BackupObjectStore, error) {
	cleaned := filepath.Clean(strings.TrimSpace(root))
	if cleaned == "" || cleaned == "." || cleaned == string(filepath.Separator) {
		return nil, fmt.Errorf("local backup directory is invalid: %q", root)
	}
	if !filepath.IsAbs(cleaned) {
		return nil, fmt.Errorf("local backup directory must be absolute: %q", cleaned)
	}
	info, err := os.Stat(cleaned)
	if err != nil {
		return nil, fmt.Errorf("stat local backup directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("local backup path is not a directory: %q", cleaned)
	}
	return &LocalBackupStore{root: cleaned}, nil
}

// resolve 把对象 key 映射为 root 内的绝对路径，并拒绝任何逃逸尝试。
func (s *LocalBackupStore) resolve(key string) (string, error) {
	cleanedKey := filepath.Clean("/" + strings.TrimSpace(key))
	rel := strings.TrimPrefix(cleanedKey, "/")
	if rel == "" || rel == "." {
		return "", errors.New("backup object key is empty")
	}
	target := filepath.Join(s.root, rel)
	rootWithSep := s.root + string(filepath.Separator)
	if target != s.root && !strings.HasPrefix(target, rootWithSep) {
		return "", fmt.Errorf("backup object key escapes local directory: %q", key)
	}
	return target, nil
}

func (s *LocalBackupStore) Upload(ctx context.Context, key string, body io.Reader, _ string) (int64, error) {
	target, err := s.resolve(key)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return 0, fmt.Errorf("create local backup dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".upload-*.part")
	if err != nil {
		return 0, fmt.Errorf("create local backup temp: %w", err)
	}
	tmpName := tmp.Name()
	discard := func() { _ = os.Remove(tmpName) }

	written, copyErr := copyContextAware(ctx, tmp, body)
	if copyErr == nil {
		copyErr = tmp.Sync()
	}
	if closeErr := tmp.Close(); copyErr == nil && closeErr != nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		discard()
		return 0, fmt.Errorf("write local backup: %w", copyErr)
	}
	if err := os.Rename(tmpName, target); err != nil {
		discard()
		return 0, fmt.Errorf("commit local backup: %w", err)
	}
	return written, nil
}

func (s *LocalBackupStore) UploadFile(ctx context.Context, key string, filePath string, contentType string) (int64, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, fmt.Errorf("open upload file: %w", err)
	}
	defer func() { _ = file.Close() }()
	return s.Upload(ctx, key, file, contentType)
}

func (s *LocalBackupStore) Download(_ context.Context, key string) (io.ReadCloser, error) {
	target, err := s.resolve(key)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(target)
	if err != nil {
		return nil, fmt.Errorf("open local backup: %w", err)
	}
	return file, nil
}

func (s *LocalBackupStore) Delete(_ context.Context, key string) error {
	target, err := s.resolve(key)
	if err != nil {
		return err
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete local backup: %w", err)
	}
	return nil
}

// PresignURL 本地目标不提供预签名下载。
func (s *LocalBackupStore) PresignURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return "", fmt.Errorf("local backup target does not support presigned URLs (key %q); copy the archive from the backup directory on the host instead", key)
}

// HeadBucket 校验目录可写，作为本地目标的连通性测试。
func (s *LocalBackupStore) HeadBucket(_ context.Context) error {
	probe, err := os.CreateTemp(s.root, ".probe-*.tmp")
	if err != nil {
		return fmt.Errorf("local backup directory is not writable: %w", err)
	}
	name := probe.Name()
	closeErr := probe.Close()
	removeErr := os.Remove(name)
	if closeErr != nil {
		return fmt.Errorf("close probe file: %w", closeErr)
	}
	if removeErr != nil {
		return fmt.Errorf("remove probe file: %w", removeErr)
	}
	return nil
}

// copyContextAware 在拷贝过程中响应取消，避免大备份无法中断。
func copyContextAware(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, 1<<20)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, readErr := src.Read(buf)
		if n > 0 {
			written, writeErr := dst.Write(buf[:n])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}
