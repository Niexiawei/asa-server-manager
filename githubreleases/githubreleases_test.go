package githubreleases

import (
	"testing"
)

func TestGetLatestRelease(t *testing.T) {
	client := NewGitHubReleasesClient()

	// Test with a known repository
	release, err := client.GetLatestRelease("syncthing/syncthing")
	if err != nil {
		t.Errorf("GetLatestRelease failed: %v", err)
		return
	}

	if release.TagName == "" {
		t.Error("Expected a non-empty tag name")
	}

	if release.HTMLURL == "" {
		t.Error("Expected a non-empty HTML URL")
	}

	t.Logf("Latest release of syncthing/syncthing: %s - %s", release.TagName, release.Name)
}

func TestGetLatestReleaseByURL(t *testing.T) {
	client := NewGitHubReleasesClient()

	// Test with a known repository URL
	release, err := client.GetLatestReleaseByURL("https://github.com/syncthing/syncthing/releases")
	if err != nil {
		t.Errorf("GetLatestReleaseByURL failed: %v", err)
		return
	}

	if release.TagName == "" {
		t.Error("Expected a non-empty tag name")
	}

	t.Logf("Latest release from URL: %s - %s", release.TagName, release.Name)
}

func TestGetLatestReleaseAssetURL(t *testing.T) {
	client := NewGitHubReleasesClient()

	// Test with a known repository and asset pattern
	downloadURL, err := client.GetLatestReleaseAssetURL("syncthing/syncthing", "syncthing-windows-amd64")
	if err != nil {
		t.Errorf("GetLatestReleaseAssetURL failed: %v", err)
		return
	}

	if downloadURL == "" {
		t.Error("Expected a non-empty download URL")
	}

	if !contains(downloadURL, "syncthing-windows-amd64") {
		t.Errorf("Expected download URL to contain 'syncthing-windows-amd64', got %s", downloadURL)
	}

	t.Logf("Asset download URL: %s", downloadURL)
}

func TestGetLatestReleaseAssetURLByURL(t *testing.T) {
	client := NewGitHubReleasesClient()

	// Test with a known repository URL and asset pattern
	downloadURL, err := client.GetLatestReleaseAssetURLByURL(
		"https://github.com/syncthing/syncthing/releases",
		"syncthing-windows-amd64",
	)
	if err != nil {
		t.Errorf("GetLatestReleaseAssetURLByURL failed: %v", err)
		return
	}

	if downloadURL == "" {
		t.Error("Expected a non-empty download URL")
	}

	t.Logf("Asset download URL from URL: %s", downloadURL)
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
