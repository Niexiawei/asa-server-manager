package githubreleases

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/imroc/req/v3"
)

// GitHubRelease 表示 GitHub Release 的基本信息
type GitHubRelease struct {
	URL         string    `json:"url"`
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []Asset   `json:"assets"`
	HTMLURL     string    `json:"html_url"`
}

// Asset 表示 Release 中的文件资产
type Asset struct {
	URL                string `json:"url"`
	Name               string `json:"name"`
	ContentType        string `json:"content_type"`
	Size               int    `json:"size"`
	DownloadCount      int    `json:"download_count"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// DownloadProgressCallback 是下载进度回调函数类型，直接接收格式化的进度字符串
// 格式示例: "downloaded 50.00%"
type DownloadProgressCallback func(progress string)

// GitHubReleasesClient 是 GitHub Releases API 的客户端
type GitHubReleasesClient struct {
	client    *http.Client
	reqClient *req.Client
}

// NewGitHubReleasesClient 创建一个新的 GitHub Releases 客户端
func NewGitHubReleasesClient() *GitHubReleasesClient {
	reqClient := req.C()
	reqClient.SetTimeout(30 * time.Second)

	return &GitHubReleasesClient{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		reqClient: reqClient,
	}
}

// GetLatestRelease 获取指定仓库的最新 Release
// repoPath 格式为 "owner/repo"，例如 "syncthing/syncthing"
func (c *GitHubReleasesClient) GetLatestRelease(repoPath string) (*GitHubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repoPath)

	resp, err := c.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to get latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API returned non-200 status: %d, body: %s", resp.StatusCode, string(body))
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("failed to decode release response: %w", err)
	}

	return &release, nil
}

// GetLatestReleaseByURL 从完整的 GitHub releases URL 获取最新 Release
// 例如 "https://github.com/syncthing/syncthing/releases"
func (c *GitHubReleasesClient) GetLatestReleaseByURL(releasesURL string) (*GitHubRelease, error) {
	// 从 URL 中提取 repoPath (owner/repo)
	parts := strings.Split(releasesURL, "/")
	if len(parts) < 5 {
		return nil, fmt.Errorf("invalid GitHub releases URL format: %s", releasesURL)
	}

	repoPath := fmt.Sprintf("%s/%s", parts[3], parts[4])
	return c.GetLatestRelease(repoPath)
}

// GetAllReleases 获取指定仓库的所有 Releases
func (c *GitHubReleasesClient) GetAllReleases(repoPath string) ([]*GitHubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases", repoPath)

	resp, err := c.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to get releases: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API returned non-200 status: %d, body: %s", resp.StatusCode, string(body))
	}

	var releases []*GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("failed to decode releases response: %w", err)
	}

	return releases, nil
}

// GetAssetDownloadURL 获取指定 Release 中匹配文件名的下载 URL
func (c *GitHubReleasesClient) GetAssetDownloadURL(release *GitHubRelease, fileNamePattern string) (string, error) {
	for _, asset := range release.Assets {
		if strings.Contains(asset.Name, fileNamePattern) {
			return asset.BrowserDownloadURL, nil
		}
	}

	return "", fmt.Errorf("asset matching pattern %s not found in release %s", fileNamePattern, release.TagName)
}

// GetLatestReleaseAssetURL 直接获取指定仓库最新 Release 中匹配文件名的下载 URL
func (c *GitHubReleasesClient) GetLatestReleaseAssetURL(repoPath string, fileNamePattern string) (string, error) {
	release, err := c.GetLatestRelease(repoPath)
	if err != nil {
		return "", err
	}

	return c.GetAssetDownloadURL(release, fileNamePattern)
}

// GetLatestReleaseAssetURLByURL 从完整的 GitHub releases URL 获取最新 Release 中匹配文件名的下载 URL
func (c *GitHubReleasesClient) GetLatestReleaseAssetURLByURL(releasesURL string, fileNamePattern string) (string, error) {
	release, err := c.GetLatestReleaseByURL(releasesURL)
	if err != nil {
		return "", err
	}

	return c.GetAssetDownloadURL(release, fileNamePattern)
}

// FilterReleasesByTagName 按标签名过滤 Releases
func FilterReleasesByTagName(releases []*GitHubRelease, tagNamePattern string) []*GitHubRelease {
	var filtered []*GitHubRelease
	for _, release := range releases {
		if strings.Contains(release.TagName, tagNamePattern) {
			filtered = append(filtered, release)
		}
	}
	return filtered
}

// SortReleasesByPublishedDate 按发布日期对 Releases 进行排序
func SortReleasesByPublishedDate(releases []*GitHubRelease, ascending bool) {
	sort.Slice(releases, func(i, j int) bool {
		if ascending {
			return releases[i].PublishedAt.Before(releases[j].PublishedAt)
		}
		return releases[i].PublishedAt.After(releases[j].PublishedAt)
	})
}

// DownloadAsset 下载指定 URL 的文件到指定路径
// proxy 是可选的代理地址，格式为 "https://proxy2.imooto.cc:85"
// callback 是可选的下载进度回调函数
func (c *GitHubReleasesClient) DownloadAsset(url, filePath string, proxy ...string) error {
	return c.DownloadAssetWithCallback(url, filePath, nil, proxy...)
}

// DownloadAssetWithCallback 下载指定 URL 的文件到指定路径，并支持下载进度回调
// callback 是下载进度回调函数，可用于记录下载进度到日志，直接接收格式化的进度字符串
// proxy 是可选的代理地址，格式为 "https://proxy2.imooto.cc:85"
func (c *GitHubReleasesClient) DownloadAssetWithCallback(url, filePath string, callback DownloadProgressCallback, proxy ...string) error {
	// 如果提供了代理地址，构建完整的代理 URL
	if len(proxy) > 0 && proxy[0] != "" {
		// 检查是否已经包含目标 URL 前缀
		if !strings.Contains(proxy[0], "https://") && !strings.Contains(proxy[0], "http://") {
			return fmt.Errorf("invalid proxy URL format: %s", proxy[0])
		}

		// 如果代理地址已经包含目标 URL 前缀（如示例中的格式），直接使用
		if strings.HasSuffix(proxy[0], url) {
			url = proxy[0]
		} else {
			// 否则构建完整的代理 URL
			proxyURL := proxy[0]
			if !strings.HasSuffix(proxyURL, "/") {
				proxyURL += "/"
			}
			url = proxyURL + url
		}
	}

	// 使用 req 库的下载功能
	reqClient := c.reqClient.R()
	reqClient.SetOutputFile(filePath)

	// 如果提供了回调函数，设置下载回调
	if callback != nil {
		reqClient.SetDownloadCallback(func(info req.DownloadInfo) {
			if info.Response != nil && info.Response.ContentLength > 0 {
				progress := float64(info.DownloadedSize) / float64(info.Response.ContentLength) * 100.0
				progressStr := fmt.Sprintf("downloaded %.2f%%", progress)
				callback(progressStr)
			}
		})
	}

	// 发送下载请求
	resp, err := reqClient.Get(url)
	if err != nil {
		return fmt.Errorf("failed to send download request: %w", err)
	}

	if !resp.IsSuccessState() {
		return fmt.Errorf("download request failed with status: %d", resp.StatusCode)
	}

	return nil
}

// DownloadLatestReleaseAsset 下载指定仓库最新 Release 中匹配文件名的文件
// repoPath 格式为 "owner/repo"
// fileNamePattern 是文件名的匹配模式
// filePath 是保存文件的路径
// proxy 是可选的代理地址
func (c *GitHubReleasesClient) DownloadLatestReleaseAsset(repoPath, fileNamePattern, filePath string, proxy ...string) error {
	return c.DownloadLatestReleaseAssetWithCallback(repoPath, fileNamePattern, filePath, nil, proxy...)
}

// DownloadLatestReleaseAssetWithCallback 下载指定仓库最新 Release 中匹配文件名的文件，并支持下载进度回调
// repoPath 格式为 "owner/repo"
// fileNamePattern 是文件名的匹配模式
// filePath 是保存文件的路径
// callback 是下载进度回调函数，直接接收格式化的进度字符串
// proxy 是可选的代理地址
func (c *GitHubReleasesClient) DownloadLatestReleaseAssetWithCallback(repoPath, fileNamePattern, filePath string, callback DownloadProgressCallback, proxy ...string) error {
	// 获取最新 Release
	release, err := c.GetLatestRelease(repoPath)
	if err != nil {
		return err
	}

	// 获取匹配的资产下载 URL
	downloadURL, err := c.GetAssetDownloadURL(release, fileNamePattern)
	if err != nil {
		return err
	}

	// 下载文件
	return c.DownloadAssetWithCallback(downloadURL, filePath, callback, proxy...)
}

// DownloadLatestReleaseAssetByURL 通过完整的 GitHub releases URL 下载最新 Release 中匹配文件名的文件
// releasesURL 格式为 "https://github.com/owner/repo/releases"
// fileNamePattern 是文件名的匹配模式
// filePath 是保存文件的路径
// proxy 是可选的代理地址
func (c *GitHubReleasesClient) DownloadLatestReleaseAssetByURL(releasesURL, fileNamePattern, filePath string, proxy ...string) error {
	return c.DownloadLatestReleaseAssetByURLWithCallback(releasesURL, fileNamePattern, filePath, nil, proxy...)
}

// DownloadLatestReleaseAssetByURLWithCallback 通过完整的 GitHub releases URL 下载最新 Release 中匹配文件名的文件，并支持下载进度回调
// releasesURL 格式为 "https://github.com/owner/repo/releases"
// fileNamePattern 是文件名的匹配模式
// filePath 是保存文件的路径
// callback 是下载进度回调函数，直接接收格式化的进度字符串
// proxy 是可选的代理地址
func (c *GitHubReleasesClient) DownloadLatestReleaseAssetByURLWithCallback(releasesURL, fileNamePattern, filePath string, callback DownloadProgressCallback, proxy ...string) error {
	// 获取最新 Release
	release, err := c.GetLatestReleaseByURL(releasesURL)
	if err != nil {
		return err
	}

	// 获取匹配的资产下载 URL
	downloadURL, err := c.GetAssetDownloadURL(release, fileNamePattern)
	if err != nil {
		return err
	}

	// 下载文件
	return c.DownloadAssetWithCallback(downloadURL, filePath, callback, proxy...)
}

// GetAssetFileName 从下载 URL 中提取文件名
func GetAssetFileName(url string) string {
	return filepath.Base(url)
}
