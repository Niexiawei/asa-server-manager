package iconapi

import (
	"asa-server/icon"
	"github.com/gin-gonic/gin"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
)

type Handler struct{}

func NewHandler() *Handler { return &Handler{} }

var (
	reSpecial = regexp.MustCompile(`[^a-zA-Z0-9_]`)
	reMulti   = regexp.MustCompile(`_+`)
	iconFS    = icon.EmbeddedFS
)

func (h *Handler) RegisterRouter(r *gin.Engine) {
	r.GET("/api/icons/creature", h.getCreatureIcon)
	r.GET("/api/icons/items", h.getItemIcon)
}

func (h *Handler) getCreatureIcon(c *gin.Context) {
	serveIcon(c, "creature/"+normalizeIconName(c.Query("name")))
}

func (h *Handler) getItemIcon(c *gin.Context) {
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

func normalizeIconName(name string) string {
	s := reSpecial.ReplaceAllString(name, "_")
	s = reMulti.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	return s + ".png"
}
