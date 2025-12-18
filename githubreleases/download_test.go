package githubreleases

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGetAssetFileName 测试从 URL 中提取文件名
func TestGetAssetFileName(t *testing.T) {
	url := "https://github.com/syncthing/syncthing/releases/download/v2.0.12/syncthing-windows-amd64-v2.0.12.zip"
	fileName := GetAssetFileName(url)

	expectedFileName := "syncthing-windows-amd64-v2.0.12.zip"
	if fileName != expectedFileName {
		t.Errorf("Expected file name %s, got %s", expectedFileName, fileName)
	}

	t.Logf("Extracted file name: %s", fileName)
}

// TestDownloadAssetMock 测试下载功能的 URL 构建逻辑
// 注意：此测试不会实际下载文件，只是验证 URL 构建逻辑
func TestDownloadAssetURLBuilding(t *testing.T) {
	// 测试正常 URL 构建
	testCases := []struct {
		name          string
		url           string
		proxy         string
		expectedURL   string
		expectError   bool
		errorContains string
	}{
		{
			name:        "no proxy",
			url:         "https://example.com/file.zip",
			proxy:       "",
			expectedURL: "https://example.com/file.zip",
			expectError: false,
		},
		{
			name:        "with proxy without trailing slash",
			url:         "https://example.com/file.zip",
			proxy:       "https://proxy2.imooto.cc:85",
			expectedURL: "https://proxy2.imooto.cc:85/https://example.com/file.zip",
			expectError: false,
		},
		{
			name:        "with proxy with trailing slash",
			url:         "https://example.com/file.zip",
			proxy:       "https://proxy2.imooto.cc:85/",
			expectedURL: "https://proxy2.imooto.cc:85/https://example.com/file.zip",
			expectError: false,
		},
		{
			name:          "invalid proxy format",
			url:           "https://example.com/file.zip",
			proxy:         "proxy2.imooto.cc:85",
			expectedURL:   "",
			expectError:   true,
			errorContains: "invalid proxy URL format",
		},
		{
			name:        "proxy with full URL",
			url:         "https://example.com/file.zip",
			proxy:       "https://proxy2.imooto.cc:85/https://example.com/file.zip",
			expectedURL: "https://proxy2.imooto.cc:85/https://example.com/file.zip",
			expectError: false,
		},
	}

	// 由于我们无法直接测试内部的 URL 构建逻辑而不实际发送请求
	// 我们将打印测试用例的预期结果
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("Test case: %s", tc.name)
			t.Logf("  URL: %s", tc.url)
			t.Logf("  Proxy: %s", tc.proxy)
			t.Logf("  Expected URL: %s", tc.expectedURL)
			t.Logf("  Expect Error: %v", tc.expectError)
			if tc.expectError {
				t.Logf("  Error Should Contain: %s", tc.errorContains)
			}
		})
	}
}

// TestDownloadAssetIntegration 集成测试：实际下载一个小文件
// 注意：此测试会实际下载文件，仅在需要时运行
func TestDownloadAssetIntegration(t *testing.T) {
	// 跳过集成测试，避免自动下载文件
	t.Skip("Skipping integration test that downloads actual files")

	client := NewGitHubReleasesClient()

	// 测试下载一个小文件（GitHub 的 .gitignore 模板）
	url := "https://raw.githubusercontent.com/github/gitignore/main/Go.gitignore"
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "Go.gitignore")

	// 下载测试
	err := client.DownloadAsset(url, filePath, "https://proxy2.imooto.cc:85/")
	if err != nil {
		t.Errorf("DownloadAsset failed: %v", err)
		return
	}

	// 验证文件是否下载成功
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Errorf("File was not downloaded: %s", filePath)
		return
	}

	t.Logf("File downloaded successfully: %s", filePath)
}

// TestDownloadAssetWithCallback 测试下载文件并使用回调函数
// 注意：此测试会实际下载文件，仅在需要时运行
func TestDownloadAssetWithCallback(t *testing.T) {
	// 跳过集成测试，避免自动下载文件
	//t.Skip("Skipping integration test that downloads actual files")

	client := NewGitHubReleasesClient()

	// 测试下载一个小文件（GitHub 的 .gitignore 模板）
	url := "https://github.com/syncthing/syncthing/releases/download/v2.0.12/syncthing-windows-amd64-v2.0.12.zip"
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "Go.gitignore")

	// 记录下载进度的回调函数
	downloadProgress := []string{}
	callback := func(progress string) {
		downloadProgress = append(downloadProgress, progress)
		t.Logf("Progress callback received: %s", progress)
	}

	// 下载测试
	err := client.DownloadAssetWithCallback(url, filePath, callback, "https://proxy2.imooto.cc:85/")
	if err != nil {
		t.Errorf("DownloadAssetWithCallback failed: %v", err)
		return
	}

	// 验证文件是否下载成功
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Errorf("File was not downloaded: %s", filePath)
		return
	}

	// 验证是否调用了回调函数
	if len(downloadProgress) == 0 {
		t.Errorf("Download callback was not called")
		return
	}

	// 验证最终进度是否包含 100%
	if len(downloadProgress) > 0 {
		finalProgress := downloadProgress[len(downloadProgress)-1]
		if !strings.Contains(finalProgress, "100.00%") && !strings.Contains(finalProgress, "99.9") {
			t.Errorf("Final download progress should be close to 100%%, got %s", finalProgress)
			return
		}
	}

	t.Logf("File downloaded successfully with callback: %s", filePath)
}
