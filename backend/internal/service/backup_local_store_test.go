//go:build unit

package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLocalBackupStore_RoundTripAndPermissions(t *testing.T) {
	root := t.TempDir()
	store, err := NewLocalBackupStore(root)
	require.NoError(t, err)

	payload := []byte("-- pg dump payload")
	n, err := store.Upload(context.Background(), "backups/2026/11/13/a.sql.gz", bytes.NewReader(payload), "application/gzip")
	require.NoError(t, err)
	require.Equal(t, int64(len(payload)), n)

	target := filepath.Join(root, "backups/2026/11/13/a.sql.gz")
	info, err := os.Stat(target)
	require.NoError(t, err)
	require.False(t, info.IsDir())

	reader, err := store.Download(context.Background(), "backups/2026/11/13/a.sql.gz")
	require.NoError(t, err)
	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, payload, got)

	require.NoError(t, store.Delete(context.Background(), "backups/2026/11/13/a.sql.gz"))
	_, err = os.Stat(target)
	require.True(t, os.IsNotExist(err), "deleted object must not exist")

	// 删除不存在的对象不报错：保留策略会重复清理同一条记录。
	require.NoError(t, store.Delete(context.Background(), "backups/2026/11/13/a.sql.gz"))
}

func TestLocalBackupStore_ContainsEscapingKeys(t *testing.T) {
	root := t.TempDir()
	store, err := NewLocalBackupStore(root)
	require.NoError(t, err)

	// key 里的 ".."/前导 "/" 会被规范化，但结果必须始终落在 root 内，
	// 不能写到备份目录之外（这是配置写错时最危险的失败模式）。
	for _, key := range []string{"../../etc/passwd", "backups/../../../escape", "/abs/path.sql"} {
		_, err := store.Upload(context.Background(), key, bytes.NewReader([]byte("x")), "")
		require.NoError(t, err, "key %q", key)
	}

	require.FileExists(t, filepath.Join(root, "etc/passwd"))
	require.FileExists(t, filepath.Join(root, "escape"))
	require.FileExists(t, filepath.Join(root, "abs/path.sql"))

	// root 的父目录不应出现任何新文件。
	parent := filepath.Dir(root)
	entries, err := os.ReadDir(parent)
	require.NoError(t, err)
	for _, entry := range entries {
		require.NotEqual(t, "escape", entry.Name())
		require.NotEqual(t, "etc", entry.Name())
	}
}

func TestLocalBackupStore_NoPartialFilesLeftAfterFailure(t *testing.T) {
	root := t.TempDir()
	store, err := NewLocalBackupStore(root)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	reader := &failingReader{cancel: cancel}
	_, err = store.Upload(ctx, "backups/x.sql.gz", reader, "")
	require.Error(t, err)

	entries, err := os.ReadDir(filepath.Join(root, "backups"))
	require.NoError(t, err)
	for _, entry := range entries {
		require.NotEqual(t, "x.sql.gz", entry.Name(), "failed upload must not leave the final object")
	}
}

type failingReader struct {
	cancel context.CancelFunc
	read   bool
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.read {
		r.cancel()
		return 0, io.ErrUnexpectedEOF
	}
	r.read = true
	copy(p, []byte("partial"))
	return len("partial"), nil
}

func TestBackupService_LocalTargetTakesPriority(t *testing.T) {
	root := t.TempDir()
	repo := newMockSettingRepo()
	dumper := &mockDumper{dumpData: []byte("dump-body")}
	svc := newTestBackupService(repo, dumper, newMockObjectStore())

	_, err := svc.UpdateLocalBackupDir(context.Background(), root)
	require.NoError(t, err)

	target, err := svc.resolveBackupTarget(context.Background())
	require.NoError(t, err)
	require.True(t, target.isLocal)

	record, err := svc.CreateBackup(context.Background(), "manual", 3)
	require.NoError(t, err)
	require.Equal(t, "completed", record.Status)
	require.Equal(t, backupTargetLocalType, record.BackupType, "record must pin its storage backend")

	// 归档真的落在目录里，且是合法 gzip。
	archive := filepath.Join(root, record.S3Key)
	file, err := os.Open(archive)
	require.NoError(t, err)
	defer func() { _ = file.Close() }()
	gz, err := gzip.NewReader(file)
	require.NoError(t, err)
	body, err := io.ReadAll(gz)
	require.NoError(t, err)
	require.Equal(t, []byte("dump-body"), body)

	// 记录归属本机后，删除应清掉本机文件而不是去找 S3。
	require.NoError(t, svc.DeleteBackup(context.Background(), record.ID))
	_, err = os.Stat(archive)
	require.True(t, os.IsNotExist(err))
}

func TestBackupService_LocalDirValidation(t *testing.T) {
	repo := newMockSettingRepo()
	svc := newTestBackupService(repo, &mockDumper{}, newMockObjectStore())

	_, err := svc.UpdateLocalBackupDir(context.Background(), "/nonexistent/definitely-missing-dir")
	require.Error(t, err, "missing directory must be rejected at save time")

	_, err = svc.UpdateLocalBackupDir(context.Background(), t.TempDir())
	require.NoError(t, err)

	// 清空即关闭本机目标；此时既无 S3 也无目录，备份应明确失败而不是静默跳过。
	_, err = svc.UpdateLocalBackupDir(context.Background(), "")
	require.NoError(t, err)
	_, err = svc.resolveBackupTarget(context.Background())
	require.ErrorIs(t, err, ErrBackupS3NotConfigured)
}

func TestBackupService_S3StillUsedWhenLocalDirEmpty(t *testing.T) {
	repo := newMockSettingRepo()
	seedS3Config(t, repo)
	store := newMockObjectStore()
	svc := newTestBackupService(repo, &mockDumper{dumpData: []byte("x")}, store)

	target, err := svc.resolveBackupTarget(context.Background())
	require.NoError(t, err)
	require.False(t, target.isLocal)
	require.Same(t, store, target.store)
}
