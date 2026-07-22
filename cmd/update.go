package cmd

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func init() {
	root.AddCommand(&cobra.Command{
		Use:   "update",
		Short: "Update repoguide to the latest version",
		Run:   runUpdate,
	})
}

const updateRepo = "repoguide-dev/repoguide-releases"

func runUpdate(_ *cobra.Command, _ []string) {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "update failed: %v\n", err)
		os.Exit(1)
	}
	if strings.Contains(exe, "Cellar") {
		fmt.Println("Installed via Homebrew — run `brew upgrade repoguide` instead.")
		return
	}

	fmt.Println("==> Checking latest repoguide release...")
	updated, latest, err := selfUpdate(exe, 30*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "update failed: %v\n", err)
		os.Exit(1)
	}
	if !updated {
		fmt.Printf("Already up to date (%s).\n", Version)
		return
	}
	fmt.Printf("==> Updated to %s\n", latest)
}

// autoUpdateOutdatedCLI is called from the background sync path once the
// backend reports the running CLI is below its required minimum version. It
// applies the update silently; the new binary takes effect on next launch.
func autoUpdateOutdatedCLI(required string) {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nrepoguide auto-update failed: %v. Run `repoguide update` manually.\n\n", err)
		return
	}
	if strings.Contains(exe, "Cellar") {
		fmt.Fprintf(os.Stderr, "\nWarning: your repoguide CLI (%s) is outdated (required: %s). Run `brew upgrade repoguide`.\n\n", Version, required)
		return
	}
	updated, latest, err := selfUpdate(exe, 30*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nrepoguide auto-update failed: %v. Run `repoguide update` manually.\n\n", err)
		return
	}
	if updated {
		fmt.Fprintf(os.Stderr, "\nUpdated repoguide CLI from %s to %s. Restart to use the new version.\n\n", Version, latest)
	}
}

// selfUpdate downloads and installs the latest release if it is newer than
// Version, replacing exe in place. It reports whether an update was applied.
func selfUpdate(exe string, timeout time.Duration) (updated bool, latest string, err error) {
	latest, err = latestReleaseVersion(timeout)
	if err != nil {
		return false, "", err
	}
	if !semverLess(Version, latest) {
		return false, latest, nil
	}

	arch := runtime.GOARCH
	if arch != "amd64" && arch != "arm64" {
		return false, latest, fmt.Errorf("unsupported architecture %s", arch)
	}
	goos := runtime.GOOS
	ext := "tar.gz"
	if goos == "windows" || goos == "darwin" {
		ext = "zip"
	}
	binName := "repoguide"
	if goos == "windows" {
		binName = "repoguide.exe"
	}
	archive := fmt.Sprintf("repoguide_%s_%s_%s.%s", latest, goos, arch, ext)
	base := fmt.Sprintf("https://github.com/%s/releases/download/v%s", updateRepo, latest)

	data, err := httpGet(base+"/"+archive, timeout)
	if err != nil {
		return false, latest, err
	}
	checksums, err := httpGet(base+"/checksums.txt", timeout)
	if err != nil {
		return false, latest, err
	}
	if err := verifyChecksum(data, checksums, archive); err != nil {
		return false, latest, err
	}
	binData, err := extractBinary(data, ext, binName)
	if err != nil {
		return false, latest, err
	}
	if err := replaceExecutable(exe, binData); err != nil {
		return false, latest, err
	}
	return true, latest, nil
}

// latestReleaseVersion returns the newest published release version (without
// the "v" prefix). timeout bounds the GitHub API call.
func latestReleaseVersion(timeout time.Duration) (string, error) {
	data, err := httpGet(fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", updateRepo), timeout)
	if err != nil {
		return "", err
	}
	var out struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", err
	}
	version := strings.TrimPrefix(out.TagName, "v")
	if version == "" {
		return "", fmt.Errorf("could not determine latest version")
	}
	return version, nil
}

func httpGet(url string, timeout time.Duration) ([]byte, error) {
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "repoguide-cli")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func verifyChecksum(archiveData, checksumsFile []byte, archiveName string) error {
	for _, line := range strings.Split(string(checksumsFile), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == archiveName {
			sum := sha256.Sum256(archiveData)
			got := hex.EncodeToString(sum[:])
			if got != fields[0] {
				return fmt.Errorf("checksum mismatch for %s", archiveName)
			}
			return nil
		}
	}
	return fmt.Errorf("no checksum entry for %s", archiveName)
}

func extractBinary(archiveData []byte, ext, binName string) ([]byte, error) {
	if ext == "zip" {
		zr, err := zip.NewReader(bytes.NewReader(archiveData), int64(len(archiveData)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if f.Name == binName {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(rc)
			}
		}
		return nil, fmt.Errorf("%s not found in archive", binName)
	}

	gr, err := gzip.NewReader(bytes.NewReader(archiveData))
	if err != nil {
		return nil, err
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Name == binName {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("%s not found in archive", binName)
}

// replaceExecutable atomically swaps the running binary for newData. On
// Windows the running exe can't be overwritten directly, so the old file is
// renamed aside first; this works identically on all platforms.
func replaceExecutable(exe string, newData []byte) error {
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".repoguide-update-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(newData); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return err
	}

	oldPath := exe + ".old"
	os.Remove(oldPath)
	if err := os.Rename(exe, oldPath); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, exe); err != nil {
		os.Rename(oldPath, exe)
		return err
	}
	os.Remove(oldPath)
	return nil
}
