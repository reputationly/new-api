package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/require"
)

const testScore = "X:1\nM:4/4\nL:1/16\nK:C\nV: Vocal\nE2G2A2G2|\n"

func withNFSRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	settings := system_setting.GetMediaStorageSettings()
	oldRoot := settings.NFSOutputRoot
	settings.NFSOutputRoot = root
	t.Cleanup(func() { settings.NFSOutputRoot = oldRoot })
	return root
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, data, 0o600))
}

func TestReadScoreSidecarReturnsTheScoreNextToTheSong(t *testing.T) {
	root := withNFSRoot(t)
	song := filepath.Join(root, "t2m-yue2", "2026", "09", "23", "1", "task.mp3")
	writeFile(t, song, []byte("ID3fake"))
	writeFile(t, strings.TrimSuffix(song, ".mp3")+".abc", []byte(testScore))

	require.Equal(t, testScore, ReadScoreSidecar(song))
}

func TestReadScoreSidecarIsEmptyWithoutAScore(t *testing.T) {
	root := withNFSRoot(t)
	song := filepath.Join(root, "task.mp3")
	writeFile(t, song, []byte("ID3fake"))

	// Other engines, and YuE2 with cot=off, write no .abc.
	require.Equal(t, "", ReadScoreSidecar(song))
	require.Equal(t, "", ReadScoreSidecar(""))
}

func TestReadScoreSidecarStaysUnderTheMountRoot(t *testing.T) {
	root := withNFSRoot(t)
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "task.abc"), []byte(testScore))

	// A path outside the root, and a symlinked sidecar that resolves outside it.
	require.Equal(t, "", ReadScoreSidecar(filepath.Join(outside, "task.mp3")))
	link := filepath.Join(root, "linked.abc")
	require.NoError(t, os.Symlink(filepath.Join(outside, "task.abc"), link))
	require.Equal(t, "", ReadScoreSidecar(filepath.Join(root, "linked.mp3")))
}

func TestReadScoreSidecarRejectsOversizedAndBinaryFiles(t *testing.T) {
	root := withNFSRoot(t)
	big := filepath.Join(root, "big.mp3")
	writeFile(t, strings.TrimSuffix(big, ".mp3")+".abc", []byte(strings.Repeat("a", maxScoreSidecarBytes+1)))
	require.Equal(t, "", ReadScoreSidecar(big))

	binary := filepath.Join(root, "binary.mp3")
	writeFile(t, strings.TrimSuffix(binary, ".mp3")+".abc", []byte{0xff, 0xfe, 0x00, 0x80})
	require.Equal(t, "", ReadScoreSidecar(binary))
}

func TestReadScoreSidecarNeverReturnsTheResultItself(t *testing.T) {
	root := withNFSRoot(t)
	score := filepath.Join(root, "task.abc")
	writeFile(t, score, []byte(testScore))
	// A result that is itself an .abc has no sidecar distinct from it.
	require.Equal(t, "", ReadScoreSidecar(score))
}
