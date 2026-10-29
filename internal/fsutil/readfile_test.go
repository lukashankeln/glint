package fsutil

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadFileCapped_Limits(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	require.NoError(t, os.WriteFile(p, []byte("12345"), 0o644))

	data, err := ReadFileCapped(p, 6)
	require.NoError(t, err)
	assert.Equal(t, "12345", string(data))

	data, err = ReadFileCapped(p, 5)
	require.NoError(t, err)
	assert.Equal(t, "12345", string(data))

	_, err = ReadFileCapped(p, 4)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrTooLarge))
	assert.Contains(t, err.Error(), "file is 5 bytes, over the 4-byte limit")
}

func TestReadFileCapped_Missing(t *testing.T) {
	_, err := ReadFileCapped(filepath.Join(t.TempDir(), "missing"), 4)
	require.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist))
}

func TestReadFileCapped_SymlinkToRegular(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	require.NoError(t, os.WriteFile(target, []byte("abc"), 0o644))
	link := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink(target, link))
	data, err := ReadFileCapped(link, 10)
	require.NoError(t, err)
	assert.Equal(t, "abc", string(data))
}

func TestReadFileCapped_SymlinkToDevZero(t *testing.T) {
	if _, err := os.Stat("/dev/zero"); err != nil {
		t.Skip("/dev/zero not available")
	}
	link := filepath.Join(t.TempDir(), "zero")
	require.NoError(t, os.Symlink("/dev/zero", link))
	_, err := ReadFileCapped(link, 1024)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotRegular))
}

func TestCheckFileCapped_Limits(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	require.NoError(t, os.WriteFile(p, []byte("12345"), 0o644))

	require.NoError(t, CheckFileCapped(p, 6))
	require.NoError(t, CheckFileCapped(p, 5))

	err := CheckFileCapped(p, 4)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrTooLarge))
	assert.Contains(t, err.Error(), "file is 5 bytes, over the 4-byte limit")
}

func TestCheckFileCapped_Missing(t *testing.T) {
	err := CheckFileCapped(filepath.Join(t.TempDir(), "missing"), 4)
	require.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist))
}

func TestCheckFileCapped_SymlinkToRegular(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	require.NoError(t, os.WriteFile(target, []byte("abc"), 0o644))
	link := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink(target, link))
	require.NoError(t, CheckFileCapped(link, 10))
}

func TestCheckFileCapped_SymlinkToDevZero(t *testing.T) {
	if _, err := os.Stat("/dev/zero"); err != nil {
		t.Skip("/dev/zero not available")
	}
	link := filepath.Join(t.TempDir(), "zero")
	require.NoError(t, os.Symlink("/dev/zero", link))
	err := CheckFileCapped(link, 1024)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotRegular))
}
