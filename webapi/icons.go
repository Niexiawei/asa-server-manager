package webapi

import (
	"asa-server/icon"
	"io/fs"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

var (
	reSpecial = regexp.MustCompile(`[^a-zA-Z0-9_]`)
	reMulti   = regexp.MustCompile(`_+`)
	iconFS    = icon.EmbeddedFS
)

func normalizeIconName(name string) string {
	s := reSpecial.ReplaceAllString(name, "_")
	s = reMulti.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	return s + ".png"
}

func (s *APIServer) getCreatureIcon(c *gin.Context) {
	serveIcon(c, "creature/"+normalizeIconName(c.Query("name")))
}

func (s *APIServer) getItemIcon(c *gin.Context) {
	serveIcon(c, "items/"+normalizeIconName(c.Query("name")))
}

func serveIcon(c *gin.Context, path string) {
	data, err := fs.ReadFile(iconFS, path)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	c.Data(http.StatusOK, "image/png", data)
}
